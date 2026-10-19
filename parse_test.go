package music

import (
	"testing"

	sdk "github.com/beamfall/plugin-sdk"
)

// TestStripTrackPrefix covers the leading disc-track / bare-track ordinal
// prefix peel used by the tag-poor rip fallback (parse.go).
func TestStripTrackPrefix(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantRest  string
		wantDisc  int
		wantTrack int
	}{
		{name: "bare-track-dash", input: "05 - Title.flac", wantRest: "Title.flac", wantDisc: 0, wantTrack: 5},
		{name: "bare-track-dot", input: "5. Title.flac", wantRest: "Title.flac", wantDisc: 0, wantTrack: 5},
		{name: "disc-track", input: "1-05 Title.flac", wantRest: "Title.flac", wantDisc: 1, wantTrack: 5},
		{name: "disc-track-dot-sep", input: "1.05 Title.flac", wantRest: "Title.flac", wantDisc: 1, wantTrack: 5},
		{name: "no-prefix", input: "Title.flac", wantRest: "Title.flac", wantDisc: 0, wantTrack: 0},
		{
			// A number embedded in the title, not at the start, must not be
			// mistaken for a track ordinal.
			name: "number-inside-title-not-prefix", input: "Symphony No. 5.flac",
			wantRest: "Symphony No. 5.flac", wantDisc: 0, wantTrack: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rest, disc, track := stripTrackPrefix(c.input)
			if rest != c.wantRest || disc != c.wantDisc || track != c.wantTrack {
				t.Errorf("stripTrackPrefix(%q) = (%q, %d, %d), want (%q, %d, %d)",
					c.input, rest, disc, track, c.wantRest, c.wantDisc, c.wantTrack)
			}
		})
	}
}

// TestSplitYear covers the 1900-2099 range guard on the folded-in album-year
// token: a plausible year is peeled off and cuts the title at its start; an
// out-of-range 4-digit run is left as title text instead.
func TestSplitYear(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantTitle string
		wantYear  int
	}{
		{name: "year-present", input: "Title 1999", wantTitle: "Title ", wantYear: 1999},
		{name: "year-lower-bound", input: "Title 1900", wantTitle: "Title ", wantYear: 1900},
		{name: "year-upper-bound", input: "Title 2099", wantTitle: "Title ", wantYear: 2099},
		{name: "no-year", input: "Title Only", wantTitle: "Title Only", wantYear: 0},
		{
			// Out of the 1900-2099 range: not treated as a year, left in the title.
			name: "out-of-range-not-a-year", input: "Title 1899", wantTitle: "Title 1899", wantYear: 0,
		},
		{
			name: "out-of-range-future", input: "Title 2100", wantTitle: "Title 2100", wantYear: 0,
		},
		{
			// Multi-byte case pairs before the year must not shift the token
			// offsets: the match runs over the raw string, never a case-folded
			// copy ("İ" is 2 bytes but its ToLower form "i" is 1, so folded
			// offsets would mis-slice raw). Regression for the
			// case-folded-offset bug class.
			name: "multibyte-case-pair-shrinks-when-folded", input: "İstanbul 1999",
			wantTitle: "İstanbul ", wantYear: 1999,
		},
		{
			// "Ⱥ" (2 bytes) case-folds to "ⱥ" (3 bytes), so offsets from a
			// folded copy would run past the end of raw and panic.
			name: "multibyte-case-pair-grows-when-folded", input: "Ⱥurora 1999",
			wantTitle: "Ⱥurora ", wantYear: 1999,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title, year := splitYear(c.input)
			if title != c.wantTitle || year != c.wantYear {
				t.Errorf("splitYear(%q) = (%q, %d), want (%q, %d)", c.input, title, year, c.wantTitle, c.wantYear)
			}
		})
	}
}

// TestCleanTitle pins the documented " - " dash-split rip convention: the
// LAST " - " segment wins as the title, discarding any leading artist/track
// credit fields. This is a known tradeoff (a track literally titled
// "Livin On A Prayer - Live" loses its first half) that is deliberately NOT
// being changed here — it matches real-world rip naming where the trailing
// segment is the live/remix/version qualifier readers care about, and the
// leading segments are redundant artist/track-title repetition.
func TestCleanTitle(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			// Documented tradeoff: last " - " segment wins, even though this
			// specific track's real title is the whole "Livin On A Prayer - Live"
			// string. Do not change this behavior without an ADR — many "rip"
			// tags rely on the trailing segment being the meaningful qualifier.
			name:  "last-dash-segment-wins-documented-tradeoff",
			input: "Livin On A Prayer - Live", want: "Live",
		},
		{
			// Two dash-segments ("Artist - Title"): the last segment still wins.
			// The true three-segment "NN - Artist - Title" rip form is covered
			// end-to-end below (TestParseThreeSegmentNNArtistTitleForm), since the
			// leading "NN" ordinal is peeled off by stripTrackPrefix before
			// cleanTitle ever sees the string.
			name: "artist-title-two-segment-form", input: "Bon Jovi - Livin On A Prayer", want: "Livin On A Prayer",
		},
		{
			name: "hyphenated-word-not-split", input: "Jean-Luc", want: "Jean-Luc",
		},
		{
			name: "dots-and-underscores-become-spaces", input: "My_Great.Song", want: "My Great Song",
		},
		{
			name: "whitespace-collapsed", input: "  Extra   Space  ", want: "Extra Space",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := cleanTitle(c.input); got != c.want {
				t.Errorf("cleanTitle(%q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// TestHasLetter covers the bare-ordinal-residue guard: a digits-only or empty
// string has no letter to score on and must fail the parse; a real (possibly
// non-Latin) title has at least one.
func TestHasLetter(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "empty", input: "", want: false},
		{name: "digits-only", input: "07", want: false},
		{name: "ascii-letters", input: "Title", want: true},
		{name: "non-latin-letters", input: "交響曲", want: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasLetter(c.input); got != c.want {
				t.Errorf("hasLetter(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

// TestParseFilenameOnlyTrackPrefixAndYear is an end-to-end sanity check (no
// TITLE tag present) that a track-number prefix and a folded-in year both
// land in the parsed result via the filename fallback path.
func TestParseFilenameOnlyTrackPrefixAndYear(t *testing.T) {
	p := NewParser()
	got, ok := p.Parse("05 - Great Song 1999.flac", sdk.ParseProbe{})
	if !ok {
		t.Fatalf("Parse() ok = false, want true")
	}
	if got.Index != 5 {
		t.Errorf("Index = %d, want 5", got.Index)
	}
	if got.Year != 1999 {
		t.Errorf("Year = %d, want 1999", got.Year)
	}
	if got.Title != "Great Song" {
		t.Errorf("Title = %q, want %q", got.Title, "Great Song")
	}
}

// TestParseThreeSegmentNNArtistTitleForm covers the full "NN - Artist -
// Title" rip convention end-to-end: stripTrackPrefix peels the leading track
// ordinal, then cleanTitle's last-dash-segment rule takes "Title" from the
// remaining "Artist - Title" text.
func TestParseThreeSegmentNNArtistTitleForm(t *testing.T) {
	p := NewParser()
	got, ok := p.Parse("05 - Bon Jovi - Livin On A Prayer.flac", sdk.ParseProbe{})
	if !ok {
		t.Fatalf("Parse() ok = false, want true")
	}
	if got.Index != 5 {
		t.Errorf("Index = %d, want 5", got.Index)
	}
	if got.Title != "Livin On A Prayer" {
		t.Errorf("Title = %q, want %q", got.Title, "Livin On A Prayer")
	}
}
