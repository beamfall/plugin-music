package music

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	sdk "github.com/beamfall/plugin-sdk"
)

// Parser implements sdk.FilenameParser for audio Media.
// Unlike the Film & TV parser, embedded tags are authoritative for music: when
// the FFmpeg probe carries a TITLE frame the parser trusts it (and the track /
// disc / date ordinals alongside it), reusing the same tag-mapping the
// hydration seam uses (MapHydration). The filename heuristics below are the
// *tag-poor rip* fallback: a bare "01 - Title.flac"
// or "1-05 Title.flac" with no usable TITLE tag still yields a Title and an
// ordinal to score on. Classical work/movement filenames and various-artists
// compilation tracks are handled as plain title text — this parser labels no
// Work/Movement structure.
type Parser struct{}

// NewParser returns a ready Parser. (Named NewParser, not New, so the two hooks
// read symmetrically at the composition root: NewParser() / NewScorer().)
func NewParser() *Parser { return &Parser{} }

var (
	// A leading disc-track ("1-05", "1.05") or bare track ("05", "5.", "5 -")
	// ordinal prefix, the dominant tag-poor rip convention. The optional first
	// group is the disc; the second is the track. Anchored at the start after
	// separator trimming so a number inside the title (e.g. "Symphony No. 5")
	// is never mistaken for a track ordinal.
	reTrackPrefix = regexp.MustCompile(`^(?:(\d{1,2})[-.])?(\d{1,3})(?:[.\-)]|\s)+`)
	// A 4-digit year token (1900–2099 range-guarded in code), optionally
	// parenthesized — some rips fold the album year into the filename.
	reYear            = regexp.MustCompile(`\(?\b(19\d{2}|20\d{2})\b\)?`)
	reDiscFolder      = regexp.MustCompile(`(?i)^(?:cd|disc)\s*0*(\d{1,2})$`)
	reAlbumFolderYear = regexp.MustCompile(`^(.*?)\s*\((19\d{2}|20\d{2})\)\s*$`)
)

// Parse derives a best-guess ParsedMedia for one audio file. Class is always
// "audio" on a successful parse (the single music MediaClass). ok is true only
// when a non-empty Title was recovered — from a TITLE tag when present, else
// from the filename after stripping the track-ordinal prefix and extension.
func (p *Parser) Parse(path string, probe sdk.ParseProbe) (sdk.ParsedMedia, bool) {
	var parsed sdk.ParsedMedia
	parsed.Class = "audio"

	tags := mapHydration(probe.Tags, path, false)
	parsed.Index = tags.TrackNumber
	if tags.DiscNumber > 0 {
		parsed.Sequence = strconv.Itoa(tags.DiscNumber)
	}
	if tags.ReleaseYear > 0 {
		parsed.Year = tags.ReleaseYear
	}

	// Tag TITLE is authoritative when present; the filename is the fallback.
	if tags.Title != "" && tags.Sources["title"] != HydrationSourcePath {
		parsed.Title = tags.Title
		return parsed, true
	}

	base := filepath.Base(path)
	name := strings.TrimSuffix(base, filepath.Ext(base))

	// Peel a leading disc-track / track ordinal prefix, filling Index/Sequence
	// only when the tags did not already supply them (tags win).
	rest, disc, track := stripTrackPrefix(name)
	if parsed.Index == 0 && track > 0 {
		parsed.Index = track
	}
	if parsed.Sequence == "" && disc > 0 {
		parsed.Sequence = strconv.Itoa(disc)
	}

	// A folded-in album year bounds the title and fills Year when tags lacked it.
	title, year := splitYear(rest)
	if parsed.Year == 0 && year > 0 {
		parsed.Year = year
	}

	parsed.Title = cleanTitle(title)
	if !hasLetter(parsed.Title) {
		// A digits-only or empty residue (e.g. "07.flac") is a bare track
		// ordinal with no title to score on — fail closed to the unmatched
		// queue rather than emit a number as a title.
		return sdk.ParsedMedia{}, false
	}
	return parsed, true
}

// hasLetter reports whether s carries at least one Unicode letter — the signal
// that a residue is a real (possibly non-Latin) title rather than a bare number.
func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// stripTrackPrefix removes a leading "D-TT" / "TT" ordinal prefix, returning the
// remainder plus the parsed disc (0 when absent) and track ordinals. A name with
// no leading ordinal is returned unchanged with (0, 0).
func stripTrackPrefix(name string) (rest string, disc, track int) {
	m := reTrackPrefix.FindStringSubmatchIndex(name)
	if m == nil {
		return name, 0, 0
	}
	if m[2] >= 0 {
		disc = atoi(name[m[2]:m[3]])
	}
	track = atoi(name[m[4]:m[5]])
	return name[m[1]:], disc, track
}

// splitYear returns the title fragment with any album-year token removed and the
// parsed year (0 when none). The first plausible 1900–2099 token wins and cuts
// the title at its start so the year does not leak into the title text. The
// match runs over raw itself, never a case-folded copy: ToLower can change the
// byte length of multi-byte case pairs (e.g. "İ"→"i", "Ⱥ"→"ⱥ"), so offsets from
// a folded copy would mis-slice raw or panic — and the pattern is all digits
// and punctuation, so folding buys nothing anyway.
func splitYear(raw string) (title string, year int) {
	loc := reYear.FindStringSubmatchIndex(raw)
	if loc == nil {
		return raw, 0
	}
	y := atoi(raw[loc[2]:loc[3]])
	if y < 1900 || y > 2099 {
		return raw, 0
	}
	return raw[:loc[0]], y
}

// cleanTitle turns a raw title fragment into a display title: "."/"_" → spaces,
// whitespace collapsed, and a leading "Artist - " credit prefix dropped when the
// name follows the "NN - Artist - Title" rip convention (the last " - " segment
// is the title). Unicode-safe — non-Latin scripts pass through unchanged.
func cleanTitle(raw string) string {
	replaced := strings.NewReplacer(".", " ", "_", " ").Replace(raw)
	collapsed := strings.Join(strings.Fields(replaced), " ")
	if segs := splitDash(collapsed); len(segs) >= 2 {
		return strings.TrimSpace(segs[len(segs)-1])
	}
	return strings.TrimSpace(collapsed)
}

// splitDash splits on " - " (a spaced hyphen), the rip-convention field
// separator, keeping hyphenated words like "Jean-Luc" intact.
func splitDash(s string) []string {
	parts := strings.Split(s, " - ")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
