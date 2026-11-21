package music

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	sdk "github.com/beamfall/plugin-sdk"
)

// Album genre enrichment (CORE-2884). Most files carry no GENRE tag, so a
// library's categories come mostly from MusicBrainz: each Album is matched to
// its release group, and the release group's community genre votes (or, when
// it has none, its artist's) classify the Album's Tracks.

const (
	// AlbumGenreRelation is the genre kind's Media relation for genres sourced
	// from the release group. Tag genres keep `classifies`, so tag hydration and
	// enrichment each replace only their own rows.
	AlbumGenreRelation = "classifies-release"
	// albumArtistGenreRelation marks the artist's genre votes in a lookup
	// result. It is never written: SelectAlbumGenres only reads it.
	albumArtistGenreRelation = "artist-genre"

	musicBrainzScraperID = "musicbrainz"
	maxAlbumGenres       = 3
)

// AlbumGenreScraper derives the release-group search and lookup from the
// bundle's musicbrainz scraper. It keeps that scraper's ID, endpoint and
// request interval, so both share one throttle against MusicBrainz.
func AlbumGenreScraper() (sdk.ScraperSpec, bool) {
	for _, s := range Bundle().Scrapers {
		if s.ID != musicBrainzScraperID {
			continue
		}
		return sdk.ScraperSpec{
			ID:                   s.ID,
			DisplayName:          s.DisplayName,
			Kind:                 s.Kind,
			Endpoint:             s.Endpoint,
			AuthRef:              s.AuthRef,
			MinRequestIntervalMS: s.MinRequestIntervalMS,
			SearchTemplate:       "/release-group?query={query}&fmt=json&limit=10",
			CandidatesPath:       "release-groups",
			CandidateIDPath:      "id",
			SearchFieldMap: map[string]string{
				"title":  "title",
				"artist": "artist-credit.0.name",
			},
			LookupTemplate: "/release-group/{id}?fmt=json&inc=genres+artist-credits",
			FieldMap:       map[string]string{"title": "title"},
			EntityMaps: []sdk.EntityMap{
				{Kind: "genre", Relation: AlbumGenreRelation, Path: "genres", NamePath: "name",
					FieldMap: map[string]string{"count": "count"}},
				{Kind: "genre", Relation: albumArtistGenreRelation, Path: "artist-credit.0.artist.genres", NamePath: "name",
					FieldMap: map[string]string{"count": "count"}},
			},
		}, true
	}
	return sdk.ScraperSpec{}, false
}

// AlbumGenreQuery builds the release-group search query. A known release MBID
// pins the release group exactly; otherwise the album title and its artists
// narrow the search.
func AlbumGenreQuery(album, releaseMBID string, artists []string) string {
	if id := strings.TrimSpace(releaseMBID); mbidPattern.MatchString(id) {
		return "reid:" + id
	}
	title := strings.TrimSpace(album)
	if title == "" {
		return ""
	}
	q := `releasegroup:"` + luceneQuoted(title) + `"`
	for _, a := range artists {
		if a = strings.TrimSpace(a); a != "" {
			q += ` AND artist:"` + luceneQuoted(a) + `"`
			break
		}
	}
	return q
}

var mbidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// luceneQuoted escapes a phrase for a double-quoted Lucene term.
func luceneQuoted(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// AlbumCandidate is one release-group search result.
type AlbumCandidate struct {
	ID     string
	Title  string
	Artist string
}

// PickAlbumCandidate returns the first candidate whose title and artist match
// the album. Edition suffixes such as "(Deluxe Edition)" are ignored on both
// sides. A release-MBID search is exact, so its single result is taken as is.
func PickAlbumCandidate(album, releaseMBID string, artists []string, candidates []AlbumCandidate) (string, bool) {
	if mbidPattern.MatchString(strings.TrimSpace(releaseMBID)) && len(candidates) > 0 && candidates[0].ID != "" {
		return candidates[0].ID, true
	}
	title := albumTitleKey(album)
	if title == "" {
		return "", false
	}
	for _, c := range candidates {
		if c.ID == "" || albumTitleKey(c.Title) != title {
			continue
		}
		if artistMatches(c.Artist, artists) {
			return c.ID, true
		}
	}
	return "", false
}

var editionSuffix = regexp.MustCompile(`(?i)\s*[\(\[][^\)\]]*(edition|remaster|remastered|deluxe|expanded|anniversary|bonus|version|reissue)[^\)\]]*[\)\]]\s*$`)

func albumTitleKey(s string) string {
	for {
		trimmed := editionSuffix.ReplaceAllString(s, "")
		if trimmed == s {
			break
		}
		s = trimmed
	}
	return normalizeTitle(s)
}

func artistMatches(candidate string, artists []string) bool {
	if len(artists) == 0 {
		return true
	}
	c := normalizeTitle(strings.TrimPrefix(strings.TrimSpace(candidate), "The "))
	for _, a := range artists {
		if a := normalizeTitle(strings.TrimPrefix(strings.TrimSpace(a), "The ")); a != "" && a == c {
			return true
		}
	}
	return false
}

// GenreVote is one genre a release group or its artist was tagged with.
type GenreVote struct {
	Name       string
	Count      int
	FromArtist bool
}

// AlbumGenreVotes reads the votes from a lookup result's entities.
func AlbumGenreVotes(entities []AlbumGenreEntity) []GenreVote {
	votes := make([]GenreVote, 0, len(entities))
	for _, e := range entities {
		count, _ := strconv.Atoi(strings.TrimSpace(e.Count))
		switch e.Relation {
		case AlbumGenreRelation:
			votes = append(votes, GenreVote{Name: e.Name, Count: count})
		case albumArtistGenreRelation:
			votes = append(votes, GenreVote{Name: e.Name, Count: count, FromArtist: true})
		}
	}
	return votes
}

// AlbumGenreEntity is the part of a lookup entity AlbumGenreVotes reads.
type AlbumGenreEntity struct {
	Relation string
	Name     string
	Count    string
}

// SelectAlbumGenres keeps the release group's genres voted at least half as
// often as its top genre, at most three. Without any it falls back to the
// artist's genres on the same rule. Names are title-cased.
func SelectAlbumGenres(votes []GenreVote) []string {
	var album, artist []GenreVote
	for _, v := range votes {
		if strings.TrimSpace(v.Name) == "" {
			continue
		}
		if v.FromArtist {
			artist = append(artist, v)
		} else {
			album = append(album, v)
		}
	}
	if picked := topGenres(album); len(picked) > 0 {
		return picked
	}
	return topGenres(artist)
}

func topGenres(votes []GenreVote) []string {
	if len(votes) == 0 {
		return nil
	}
	sorted := append([]GenreVote(nil), votes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Count > sorted[j].Count })
	top := sorted[0].Count
	seen := map[string]bool{}
	var out []string
	for _, v := range sorted {
		if len(out) == maxAlbumGenres || v.Count*2 < top {
			break
		}
		name := genreTitle(v.Name)
		key := strings.ToLower(name)
		if name == "" || seen[key] || placeholderGenres[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	return out
}

var genreAcronyms = map[string]string{
	"r&b": "R&B", "uk": "UK", "us": "US", "edm": "EDM", "idm": "IDM", "ebm": "EBM",
	"dj": "DJ", "lo-fi": "Lo-Fi", "j-pop": "J-Pop", "k-pop": "K-Pop", "j-rock": "J-Rock",
}

var genreMinorWords = map[string]bool{"and": true, "n": true, "of": true, "the": true}

// genreTitle title-cases a MusicBrainz genre name ("alternative rock" →
// "Alternative Rock", "drum and bass" → "Drum and Bass", "r&b" → "R&B").
func genreTitle(name string) string {
	words := strings.Fields(strings.TrimSpace(name))
	for i, w := range words {
		lower := strings.ToLower(w)
		if a, ok := genreAcronyms[lower]; ok {
			words[i] = a
			continue
		}
		if i > 0 && genreMinorWords[lower] {
			words[i] = lower
			continue
		}
		parts := strings.Split(lower, "-")
		for j, p := range parts {
			if r := []rune(p); len(r) > 0 {
				parts[j] = string(unicode.ToUpper(r[0])) + string(r[1:])
			}
		}
		words[i] = strings.Join(parts, "-")
	}
	return strings.Join(words, " ")
}
