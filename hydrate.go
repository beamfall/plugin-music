package music

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// HydratedField is the declared flag Media field the hydration seam sets
// absent-only once a track's tags are hydrated. It is the
// single source of truth for the marker name: the app-side write site and the
// scan backfill stale-set query both reference it, so the marker can never
// drift between the writer and the reader.
const HydratedField = "music-hydrated"

// HydratedFingerprintField is the text Media field recording the content
// fingerprint a track's tags were last hydrated from. It sits beside
// HydratedField: the marker says a track was hydrated once, the fingerprint
// says from which bytes — so a rescan can tell a re-tagged file (fingerprint
// moved: Picard/Mp3tag rewrote the tags) from an unchanged one (same
// fingerprint: no write) instead of treating every hydrated track as final.
// Declared in the bundle so the bundle-validated write path persists it.
const HydratedFingerprintField = "music-hydrated-fingerprint"

// HydrationWrite is how the hydration seam must land a mapped Hydration,
// decided from the fingerprint recorded at the last hydration and the one the
// current scan computed (DecideHydrationWrite).
type HydrationWrite int

const (
	// HydrationWriteFresh: nothing recorded yet — write absent-only and
	// record the current fingerprint beside the marker.
	HydrationWriteFresh HydrationWrite = iota
	// HydrationWriteSkip: same bytes as the last hydration — no write at all.
	HydrationWriteSkip
	// HydrationWriteReplace: the bytes changed — replace tag-sourced values
	// through the source-ranked upsert (manual/operator values win,
	// hydrated values are replaced) and record the new fingerprint.
	HydrationWriteReplace
)

// DecideHydrationWrite keys hydration on the file's content fingerprint. An
// empty recorded fingerprint is a track hydrated before fingerprints were
// recorded (or never hydrated): it gets the absent-only write. When the scan
// has no fingerprint for a recorded track the decision is Skip — without a
// current fingerprint a change cannot be proven, and a blind replace would
// churn tag-sourced values on every rescan.
func DecideHydrationWrite(recordedFingerprint, currentFingerprint string) HydrationWrite {
	if recordedFingerprint == "" {
		return HydrationWriteFresh
	}
	if currentFingerprint == "" {
		return HydrationWriteSkip
	}
	if recordedFingerprint == currentFingerprint {
		return HydrationWriteSkip
	}
	return HydrationWriteReplace
}

// Credit is one track-level performance credit: the artist name plus the
// optional credited-as attribute (the raw tag spelling, e.g. "feat." text the
// embedded tag carried — never split by this hydrator).
type Credit struct {
	Artist     string
	CreditedAs string
	MBID       string
}

const (
	HydrationSourceTag     = "tag"
	HydrationSourcePath    = "path"
	HydrationSourceSidecar = "sidecar"
)

// Hydration is the neutral intent a set of audio tags maps to. Every field is
// best-effort: an absent tag leaves the corresponding member zero, and the
// hydration seam writes only the members that are present (absent-only at the
// store layer too). It names no store or plugin types so the parser stays pure.
type Hydration struct {
	// Title overrides the path-derived title when the tag carries one.
	Title string
	// Album is the per-track album title (TALB / ALBUM).
	Album string
	// TrackNumber / DiscNumber are the ordinals (TRCK / TPOS, "n" or "n/of").
	TrackNumber int
	DiscNumber  int
	// ReleaseYear / ReleaseDate come from TDRC / DATE (full date when present,
	// year derived from it or from a bare year tag).
	ReleaseYear int
	ReleaseDate time.Time
	// Credits are the track-level performance credits (ARTIST, possibly
	// multi-valued). AlbumArtist is the ALBUMARTIST tag (the album's credit).
	Credits         []Credit
	AlbumArtist     string
	AlbumArtistMBID string
	// Genres are the GENRE tag values (possibly multi-valued).
	Genres          []string
	Composer        string
	ArtistSort      string
	AlbumSort       string
	AlbumArtistSort string
	Lyrics          string
	BPM             float64
	BPMSet          bool
	Rating          float64
	RatingSet       bool
	Comment         string
	Compilation     bool
	CompilationSet  bool
	// RecordingMBID / ReleaseMBID / ArtistMBID are MusicBrainz identifiers
	// embedded by Picard. RecordingMBID is written through the generic media
	// external ID substrate so scrobble adapters can read it.
	RecordingMBID    string
	ReleaseMBID      string
	ReleaseGroupMBID string
	ArtistMBID       string
	// Sources records field-level provenance for values derived outside tags.
	// In particular, folder fallbacks are explicitly marked "path" so callers
	// can distinguish inferred grouping metadata from embedded metadata.
	Sources map[string]string
}

// Empty reports whether tags, a sidecar, and path fallback produced nothing
// worth writing. The seam skips the write and hydration marker when true.
func (h Hydration) Empty() bool {
	return h.Title == "" && h.Album == "" && h.TrackNumber == 0 && h.DiscNumber == 0 &&
		h.ReleaseYear == 0 && h.ReleaseDate.IsZero() && len(h.Credits) == 0 &&
		h.AlbumArtist == "" && len(h.Genres) == 0 &&
		h.Composer == "" && h.ArtistSort == "" && h.AlbumSort == "" &&
		h.AlbumArtistSort == "" && h.Lyrics == "" && !h.BPMSet &&
		!h.RatingSet && h.Comment == "" && !h.CompilationSet &&
		h.RecordingMBID == "" && h.ReleaseMBID == "" &&
		h.ReleaseGroupMBID == "" && h.ArtistMBID == ""
}

// MapHydration reads the audio Tags map and produces the Hydration intent. Tag keys are
// matched case-insensitively across the common ID3/Vorbis/MP4 spellings FFmpeg
// surfaces. Embedded tags are authoritative; missing grouping and ordinal
// values fall back to the conventional Artist/Album[/CD N]/Track path shape.
func MapHydration(tags map[string]string, path string) Hydration {
	return mapHydration(tags, path, true)
}

func mapHydration(tags map[string]string, path string, includeSidecar bool) Hydration {
	lookup := normalizeTags(tags)
	h := Hydration{Sources: map[string]string{}}
	h.Title = firstTagValue(lookup, "title")
	h.Album = firstTagValue(lookup, "album")
	h.TrackNumber = leadingInt(firstTagValue(lookup, "track", "tracknumber"))
	h.DiscNumber = leadingInt(firstTagValue(lookup, "disc", "discnumber", "discnum"))

	if date, year := parseReleaseDate(firstTagValue(lookup, "date", "originaldate", "year", "originalyear")); !date.IsZero() || year != 0 {
		h.ReleaseDate = date
		h.ReleaseYear = year
	}

	artists := splitTagValues(firstTagValue(lookup, "artist"))
	artistMBIDs := splitTagValues(firstTagValue(lookup, "musicbrainz_artistid", "musicbrainz artist id"))
	for i, artist := range artists {
		credit := Credit{Artist: artist, CreditedAs: artist}
		if i < len(artistMBIDs) {
			credit.MBID = artistMBIDs[i]
		}
		h.Credits = append(h.Credits, credit)
	}
	// ffprobe surfaces the album-artist frame as "album_artist" (ID3/MP4 TPE2/
	// aART) or "albumartist" (Vorbis ALBUMARTIST) — never a space-separated
	// spelling, since (unlike the MusicBrainz TXXX free-text frames below) it
	// is always a standard, non-free-text frame.
	h.AlbumArtist = firstTagValue(lookup, "album_artist", "albumartist")
	h.AlbumArtistMBID = firstTagValue(lookup, "musicbrainz_albumartistid", "musicbrainz album artist id")
	h.Genres = splitTagValues(firstTagValue(lookup, "genre"))
	h.Composer = firstTagValue(lookup, "composer", "tcom", "writer", "©wrt")
	h.ArtistSort = firstTagValue(lookup, "artistsort", "artist_sort", "sort_artist", "tsop", "soar")
	h.AlbumSort = firstTagValue(lookup, "albumsort", "album_sort", "sort_album", "tsoa", "soal")
	h.AlbumArtistSort = firstTagValue(lookup, "albumartistsort", "album_artist_sort", "sort_album_artist", "tso2", "soaa")
	h.Lyrics = firstTagValue(lookup, "lyrics", "unsyncedlyrics", "unsynchronized_lyrics", "uslt", "©lyr")
	h.BPM, h.BPMSet = parseTagNumber(firstTagValue(lookup, "bpm", "tbpm", "tmpo"))
	h.Rating, h.RatingSet = parseTagNumber(firstTagValue(lookup, "rating", "popularimeter", "popm", "rtng"))
	h.Comment = firstTagValue(lookup, "comment", "comm", "description", "©cmt")
	h.Compilation, h.CompilationSet = parseTagBool(firstTagValue(lookup, "compilation", "tcmp", "cpil"))

	h.RecordingMBID = firstTagValue(lookup, "musicbrainz_trackid", "musicbrainz track id", "musicbrainz_recordingid")
	h.ReleaseMBID = firstTagValue(lookup, "musicbrainz_albumid", "musicbrainz album id")
	h.ReleaseGroupMBID = firstTagValue(lookup, "musicbrainz_releasegroupid", "musicbrainz release group id")
	h.ArtistMBID = firstTagValue(lookup, "musicbrainz_artistid", "musicbrainz artist id")

	deriveHydrationFromPath(&h, path)
	if includeSidecar && h.Lyrics == "" {
		if lyrics := readSiblingLyrics(path); lyrics != "" {
			h.Lyrics = lyrics
			h.Sources["lyrics"] = HydrationSourceSidecar
		}
	}
	if strings.EqualFold(h.AlbumArtist, "Various Artists") {
		h.Compilation = true
		h.CompilationSet = true
	}
	if h.Compilation && (h.AlbumArtist == "" || h.Sources["albumartist"] == HydrationSourcePath) {
		h.AlbumArtist = "Various Artists"
		h.Sources["albumartist"] = HydrationSourceTag
	}
	if len(h.Sources) == 0 {
		h.Sources = nil
	}
	return h
}

func deriveHydrationFromPath(h *Hydration, path string) {
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	rest, filenameDisc, track := stripTrackPrefix(stem)
	if h.Title == "" {
		title := cleanTitle(rest)
		if hasLetter(title) {
			h.Title = title
			h.Sources["title"] = HydrationSourcePath
		}
	}
	if h.TrackNumber == 0 && track > 0 {
		h.TrackNumber = track
		h.Sources["track-number"] = HydrationSourcePath
	}
	if h.DiscNumber == 0 && filenameDisc > 0 {
		h.DiscNumber = filenameDisc
		h.Sources["disc-number"] = HydrationSourcePath
	}

	albumDir := filepath.Dir(filepath.Clean(path))
	if disc := discFolderNumber(filepath.Base(albumDir)); disc > 0 {
		if h.DiscNumber == 0 {
			h.DiscNumber = disc
			h.Sources["disc-number"] = HydrationSourcePath
		}
		albumDir = filepath.Dir(albumDir)
	}
	artist := filepath.Base(filepath.Dir(albumDir))
	if !usablePathComponent(artist) {
		return
	}
	album, year := splitAlbumFolder(filepath.Base(albumDir))
	if h.Album == "" && usablePathComponent(album) {
		h.Album = album
		h.Sources["album"] = HydrationSourcePath
	}
	if h.ReleaseYear == 0 && year > 0 {
		h.ReleaseYear = year
		h.Sources["release-year"] = HydrationSourcePath
	}
	if h.AlbumArtist == "" && usablePathComponent(artist) {
		h.AlbumArtist = artist
		h.Sources["albumartist"] = HydrationSourcePath
	}
	if len(h.Credits) == 0 && usablePathComponent(artist) {
		h.Credits = []Credit{{Artist: artist, CreditedAs: artist}}
		h.Sources["artist"] = HydrationSourcePath
	}
}

func discFolderNumber(name string) int {
	match := reDiscFolder.FindStringSubmatch(strings.TrimSpace(name))
	if match == nil {
		return 0
	}
	return atoi(match[1])
}

func splitAlbumFolder(name string) (string, int) {
	match := reAlbumFolderYear.FindStringSubmatch(strings.TrimSpace(name))
	if match == nil {
		return strings.TrimSpace(name), 0
	}
	return strings.TrimSpace(match[1]), atoi(match[2])
}

func usablePathComponent(value string) bool {
	return value != "" && value != "." && value != string(filepath.Separator)
}

func readSiblingLyrics(path string) string {
	if path == "" {
		return ""
	}
	stem := strings.TrimSuffix(path, filepath.Ext(path))
	data, err := os.ReadFile(stem + ".lrc")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func parseTagBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func parseTagNumber(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	if slash := strings.IndexByte(value, '/'); slash >= 0 {
		value = value[:slash]
	}
	if fields := strings.Fields(value); len(fields) != 0 {
		value = fields[0]
	}
	number, err := strconv.ParseFloat(value, 64)
	return number, err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)
}

// normalizeTags lower-cases keys and trims values so lookups are spelling- and
// case-insensitive. When two source keys collide after normalization the first
// non-empty value in sorted source-key order wins (FFmpeg generally emits one
// canonical spelling) — a fixed rule rather than map iteration order, so
// MapHydration (and Parser.Parse through it) stays deterministic for the same
// tags, the sdk.FilenameParser contract.
func normalizeTags(tags map[string]string) map[string]string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(tags))
	for _, k := range keys {
		key := strings.ToLower(strings.TrimSpace(k))
		val := strings.TrimSpace(tags[k])
		if key == "" || val == "" {
			continue
		}
		if _, ok := out[key]; !ok {
			out[key] = val
		}
	}
	return out
}

// firstTagValue returns the first present, non-empty value among the candidate
// keys (already normalized to lowercase).
func firstTagValue(lookup map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := lookup[k]; v != "" {
			return v
		}
	}
	return ""
}

// leadingInt parses the leading integer of a tag like "5", "5/12", or "05".
// Returns 0 when the value has no leading digits (the absent/unknown ordinal).
func leadingInt(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	end := 0
	for end < len(v) && v[end] >= '0' && v[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	n, err := strconv.Atoi(v[:end])
	if err != nil {
		return 0
	}
	return n
}

// parseReleaseDate parses a release-date tag into a full date (when the tag
// carries YYYY-MM-DD or YYYY-MM) and the derived year. A bare "YYYY" yields a
// zero date and the year only. Returns (zero, 0) when no year can be read.
func parseReleaseDate(v string) (time.Time, int) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, 0
	}
	for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
		if t, err := time.Parse(layout, v); err == nil {
			if layout == "2006" {
				return time.Time{}, t.Year()
			}
			return t.UTC(), t.Year()
		}
	}
	// Tag may be a longer string with a leading year (e.g. "1998-00-00" some
	// taggers emit, or "1998 (Remastered)"). Fall back to the leading 4 digits.
	if len(v) >= 4 {
		if year, err := strconv.Atoi(v[:4]); err == nil && year > 0 {
			return time.Time{}, year
		}
	}
	return time.Time{}, 0
}

// splitTagValues splits a multi-valued tag on the NUL separator FFmpeg uses for
// repeated frames and the ";" delimiter, trimming and dropping empties. "/" is
// NOT a delimiter (e.g. "A/B" stays one value). A single-valued tag returns one
// element. No "feat." splitting — the values are taken literally.
func splitTagValues(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	fields := strings.FieldsFunc(v, func(r rune) bool {
		return r == '\x00' || r == ';'
	})
	var out []string
	seen := map[string]bool{}
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" || seen[strings.ToLower(f)] {
			continue
		}
		seen[strings.ToLower(f)] = true
		out = append(out, f)
	}
	return out
}
