package music

import "testing"

func TestParseReleaseType(t *testing.T) {
	cases := map[string]string{
		"":                         "",
		"  ":                       "",
		"album":                    ReleaseTypeAlbum,
		"Album":                    ReleaseTypeAlbum,
		"Single":                   ReleaseTypeSingle,
		"EP":                       ReleaseTypeEP,
		"album; live":              ReleaseTypeLive,
		"album/compilation":        ReleaseTypeCompilation,
		"album; compilation; live": ReleaseTypeLive,
		"album, soundtrack":        ReleaseTypeSoundtrack,
		"single; remix":            ReleaseTypeRemix,
		"broadcast":                ReleaseTypeOther,
		"album; dj-mix":            ReleaseTypeAlbum,
		"bogus":                    "",
	}
	for tag, want := range cases {
		if got := ParseReleaseType(tag); got != want {
			t.Errorf("ParseReleaseType(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestReleaseTypeGroupsCoverParsedValues(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range ReleaseTypeGroups() {
		if g.Value == "" || g.Label["en"] == "" || g.Label["fr"] == "" {
			t.Fatalf("incomplete group %+v", g)
		}
		if seen[g.Value] {
			t.Fatalf("duplicate group %q", g.Value)
		}
		seen[g.Value] = true
	}
	for _, v := range []string{ReleaseTypeAlbum, ReleaseTypeEP, ReleaseTypeSingle, ReleaseTypeLive,
		ReleaseTypeCompilation, ReleaseTypeSoundtrack, ReleaseTypeRemix, ReleaseTypeOther} {
		if !seen[v] {
			t.Errorf("no group for %q", v)
		}
	}
}

func TestMapHydrationReleaseType(t *testing.T) {
	if got := MapHydration(map[string]string{"RELEASETYPE": "album; live"}, "").ReleaseType; got != ReleaseTypeLive {
		t.Errorf("RELEASETYPE live: got %q", got)
	}
	if got := MapHydration(map[string]string{"MusicBrainz Album Type": "single"}, "").ReleaseType; got != ReleaseTypeSingle {
		t.Errorf("MusicBrainz Album Type single: got %q", got)
	}
	if got := MapHydration(map[string]string{"TITLE": "x", "COMPILATION": "1"}, "").ReleaseType; got != ReleaseTypeCompilation {
		t.Errorf("compilation fallback: got %q", got)
	}
	if got := MapHydration(map[string]string{"TITLE": "x"}, "").ReleaseType; got != "" {
		t.Errorf("untagged: got %q", got)
	}
	if MapHydration(map[string]string{"RELEASETYPE": "ep"}, "").Empty() {
		t.Error("a release type alone is not empty")
	}
}
