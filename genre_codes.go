package music

import (
	"strconv"
	"strings"
)

// id3v1Genres is the ID3v1 genre table (0-79) with the Winamp extensions
// (80-191). Old taggers write a track's genre as an index into it, as "(17)",
// "17" or "(17)Rock", and ffprobe hands that string through undecoded.
var id3v1Genres = [...]string{
	"Blues", "Classic Rock", "Country", "Dance", "Disco", "Funk", "Grunge", "Hip-Hop",
	"Jazz", "Metal", "New Age", "Oldies", "Other", "Pop", "R&B", "Rap",
	"Reggae", "Rock", "Techno", "Industrial", "Alternative", "Ska", "Death Metal", "Pranks",
	"Soundtrack", "Euro-Techno", "Ambient", "Trip-Hop", "Vocal", "Jazz+Funk", "Fusion", "Trance",
	"Classical", "Instrumental", "Acid", "House", "Game", "Sound Clip", "Gospel", "Noise",
	"Alternative Rock", "Bass", "Soul", "Punk", "Space", "Meditative", "Instrumental Pop", "Instrumental Rock",
	"Ethnic", "Gothic", "Darkwave", "Techno-Industrial", "Electronic", "Pop-Folk", "Eurodance", "Dream",
	"Southern Rock", "Comedy", "Cult", "Gangsta", "Top 40", "Christian Rap", "Pop/Funk", "Jungle",
	"Native American", "Cabaret", "New Wave", "Psychedelic", "Rave", "Showtunes", "Trailer", "Lo-Fi",
	"Tribal", "Acid Punk", "Acid Jazz", "Polka", "Retro", "Musical", "Rock & Roll", "Hard Rock",
	"Folk", "Folk-Rock", "National Folk", "Swing", "Fast Fusion", "Bebop", "Latin", "Revival",
	"Celtic", "Bluegrass", "Avantgarde", "Gothic Rock", "Progressive Rock", "Psychedelic Rock", "Symphonic Rock", "Slow Rock",
	"Big Band", "Chorus", "Easy Listening", "Acoustic", "Humour", "Speech", "Chanson", "Opera",
	"Chamber Music", "Sonata", "Symphony", "Booty Bass", "Primus", "Porn Groove", "Satire", "Slow Jam",
	"Club", "Tango", "Samba", "Folklore", "Ballad", "Power Ballad", "Rhythmic Soul", "Freestyle",
	"Duet", "Punk Rock", "Drum Solo", "A Cappella", "Euro-House", "Dance Hall", "Goa", "Drum & Bass",
	"Club-House", "Hardcore", "Terror", "Indie", "BritPop", "Afro-Punk", "Polsk Punk", "Beat",
	"Christian Gangsta Rap", "Heavy Metal", "Black Metal", "Crossover", "Contemporary Christian", "Christian Rock", "Merengue", "Salsa",
	"Thrash Metal", "Anime", "JPop", "Synthpop", "Abstract", "Art Rock", "Baroque", "Bhangra",
	"Big Beat", "Breakbeat", "Chillout", "Downtempo", "Dub", "EBM", "Eclectic", "Electro",
	"Electroclash", "Emo", "Experimental", "Garage", "Global", "IDM", "Illbient", "Industro-Goth",
	"Jam Band", "Krautrock", "Leftfield", "Lounge", "Math Rock", "New Romantic", "Nu-Breakz", "Post-Punk",
	"Post-Rock", "Psytrance", "Shoegaze", "Space Rock", "Trop Rock", "World Music", "Neoclassical", "Audiobook",
	"Audio Theatre", "Neue Deutsche Welle", "Podcast", "Indie Rock", "G-Funk", "Dubstep", "Garage Rock", "Psybient",
}

// placeholderGenres name no genre at all: "Other" is ID3v1's code 12, the
// default many taggers write when nothing was chosen. A track carrying only
// these gets no genre, so it does not fill the Categories page with a tile
// that groups unrelated music.
var placeholderGenres = map[string]bool{"other": true, "unknown": true, "genre": true}

// decodeGenres turns raw GENRE tag values into genre names: ID3v1/ID3v2.3
// numeric references are decoded ("(17)" and "17" → "Rock", "(17)Rock" →
// "Rock", "(17)(20)" → "Rock", "Alternative"), "(RX)" and "(CR)" become
// "Remix" and "Cover", "((" escapes a literal "(", and placeholders are
// dropped. Order is kept and duplicates removed, case-insensitively.
func decodeGenres(values []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(g string) {
		g = strings.TrimSpace(g)
		key := strings.ToLower(g)
		if g == "" || seen[key] || placeholderGenres[key] {
			return
		}
		seen[key] = true
		out = append(out, g)
	}
	for _, v := range values {
		for _, g := range decodeGenre(v) {
			add(g)
		}
	}
	return out
}

func decodeGenre(v string) []string {
	v = strings.TrimSpace(v)
	if n, err := strconv.Atoi(v); err == nil {
		return []string{id3v1Name(n, v)}
	}
	var refs []string
	for strings.HasPrefix(v, "(") && !strings.HasPrefix(v, "((") {
		end := strings.IndexByte(v, ')')
		if end < 0 {
			break
		}
		ref := v[1:end]
		switch ref {
		case "RX":
			refs = append(refs, "Remix")
		case "CR":
			refs = append(refs, "Cover")
		default:
			n, err := strconv.Atoi(ref)
			if err != nil {
				// Not a reference: "(Live) Rock" is a plain name.
				return append(refs, v)
			}
			refs = append(refs, id3v1Name(n, v[:end+1]))
		}
		v = strings.TrimSpace(v[end+1:])
	}
	v = strings.TrimPrefix(v, "(")
	if v == "" {
		return refs
	}
	// ID3v2.3 writes the refinement after its reference: "(17)Rock" names
	// the same genre twice, and the text is the more specific of the two.
	if len(refs) > 0 {
		refs = refs[:len(refs)-1]
	}
	return append(refs, v)
}

func id3v1Name(n int, raw string) string {
	if n >= 0 && n < len(id3v1Genres) {
		return id3v1Genres[n]
	}
	return raw
}
