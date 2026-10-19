package music

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMapEmptyTags(t *testing.T) {
	if got := MapHydration(nil, ""); !got.Empty() {
		t.Fatalf("nil tags must map to empty Hydration, got %+v", got)
	}
	if got := MapHydration(map[string]string{}, ""); !got.Empty() {
		t.Fatalf("empty tags must map to empty Hydration, got %+v", got)
	}
	// Whitespace-only values are dropped, leaving nothing to hydrate.
	if got := MapHydration(map[string]string{"title": "  ", "track": ""}, ""); !got.Empty() {
		t.Fatalf("blank tag values must map to empty Hydration, got %+v", got)
	}
}

func TestMapFolderFallback(t *testing.T) {
	h := MapHydration(nil, "/music/Radiohead/OK Computer/CD1/01 - Airbag.flac")
	if h.Title != "Airbag" || h.Album != "OK Computer" || h.AlbumArtist != "Radiohead" || h.TrackNumber != 1 || h.DiscNumber != 1 {
		t.Fatalf("folder fallback = %+v", h)
	}
	if len(h.Credits) != 1 || h.Credits[0].Artist != "Radiohead" {
		t.Fatalf("folder artist credits = %+v", h.Credits)
	}
	for _, field := range []string{"title", "album", "albumartist", "artist", "track-number", "disc-number"} {
		if h.Sources[field] != HydrationSourcePath {
			t.Errorf("Sources[%q] = %q, want %q", field, h.Sources[field], HydrationSourcePath)
		}
	}

	h = MapHydration(map[string]string{"TITLE": "Tagged title"}, "/music/Boards of Canada/Music Has the Right to Children (1998)/02.flac")
	if h.Album != "Music Has the Right to Children" || h.ReleaseYear != 1998 {
		t.Fatalf("album-year folder fallback = %+v", h)
	}
}

func TestMapRemainingStandardTags(t *testing.T) {
	h := MapHydration(map[string]string{
		"TCOM":                       "Alice Composer",
		"TCMP":                       "1",
		"TSOP":                       "Artist, The",
		"TSOA":                       "Album, The",
		"TSO2":                       "Various Artists",
		"USLT":                       "Words from tags",
		"TBPM":                       "123",
		"POPM":                       "196/255",
		"COMM":                       "liner note",
		"MUSICBRAINZ_RELEASEGROUPID": "release-group-1",
		"MUSICBRAINZ_ARTISTID":       "artist-1",
		"MUSICBRAINZ_ALBUMARTISTID":  "album-artist-1",
	}, "/music/Artist/Album/01 - Song.mp3")
	if h.Composer != "Alice Composer" || !h.Compilation || h.ArtistSort != "Artist, The" || h.AlbumSort != "Album, The" || h.AlbumArtistSort != "Various Artists" {
		t.Fatalf("composer/compilation/sort tags = %+v", h)
	}
	if h.Lyrics != "Words from tags" || h.BPM != 123 || h.Rating != 196 || h.Comment != "liner note" {
		t.Fatalf("lyrics/bpm/rating/comment tags = %+v", h)
	}
	if h.ReleaseGroupMBID != "release-group-1" || h.ArtistMBID != "artist-1" || h.AlbumArtistMBID != "album-artist-1" {
		t.Fatalf("extended MBIDs = %+v", h)
	}

	mp4 := MapHydration(map[string]string{
		"©wrt": "MP4 Composer", "cpil": "1", "soar": "Artist Sort", "soal": "Album Sort",
		"soaa": "Album Artist Sort", "©lyr": "MP4 Lyrics", "tmpo": "99",
		"rtng": "5", "©cmt": "MP4 Comment",
	}, "/music/Artist/Album/01 - Song.m4a")
	if mp4.Composer != "MP4 Composer" || !mp4.Compilation || mp4.ArtistSort != "Artist Sort" ||
		mp4.AlbumSort != "Album Sort" || mp4.AlbumArtistSort != "Album Artist Sort" ||
		mp4.Lyrics != "MP4 Lyrics" || mp4.BPM != 99 || mp4.Rating != 5 || mp4.Comment != "MP4 Comment" {
		t.Fatalf("MP4 aliases = %+v", mp4)
	}
}

func TestMapSiblingLRCFallback(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "01 - Airbag.flac")
	if err := os.WriteFile(filepath.Join(dir, "01 - Airbag.lrc"), []byte("[00:01.00]In the next world war"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := MapHydration(nil, audio)
	if h.Lyrics != "[00:01.00]In the next world war" || h.Sources["lyrics"] != HydrationSourceSidecar {
		t.Fatalf("sidecar lyrics = %q source = %q", h.Lyrics, h.Sources["lyrics"])
	}
}

func TestMapCoreFields(t *testing.T) {
	h := MapHydration(map[string]string{
		"TITLE":                "Paranoid Android",
		"ALBUM":                "OK Computer",
		"TRACK":                "2/12",
		"DISC":                 "1/1",
		"DATE":                 "1997-05-21",
		"ARTIST":               "Radiohead",
		"ALBUMARTIST":          "Radiohead",
		"GENRE":                "Alternative Rock",
		"MUSICBRAINZ_TRACKID":  "rec-123",
		"MUSICBRAINZ_ALBUMID":  "rel-456",
		"MUSICBRAINZ_ARTISTID": "art-789",
	}, "/music/radiohead/ok-computer/02.flac")

	if h.Title != "Paranoid Android" {
		t.Errorf("Title = %q", h.Title)
	}
	if h.Album != "OK Computer" {
		t.Errorf("Album = %q", h.Album)
	}
	if h.TrackNumber != 2 {
		t.Errorf("TrackNumber = %d, want 2 (from \"2/12\")", h.TrackNumber)
	}
	if h.DiscNumber != 1 {
		t.Errorf("DiscNumber = %d, want 1", h.DiscNumber)
	}
	if h.ReleaseYear != 1997 {
		t.Errorf("ReleaseYear = %d, want 1997", h.ReleaseYear)
	}
	want := time.Date(1997, 5, 21, 0, 0, 0, 0, time.UTC)
	if !h.ReleaseDate.Equal(want) {
		t.Errorf("ReleaseDate = %v, want %v", h.ReleaseDate, want)
	}
	if len(h.Credits) != 1 || h.Credits[0].Artist != "Radiohead" || h.Credits[0].CreditedAs != "Radiohead" {
		t.Errorf("Credits = %+v, want one Radiohead credit", h.Credits)
	}
	if h.AlbumArtist != "Radiohead" {
		t.Errorf("AlbumArtist = %q", h.AlbumArtist)
	}
	if len(h.Genres) != 1 || h.Genres[0] != "Alternative Rock" {
		t.Errorf("Genres = %v", h.Genres)
	}
	if h.RecordingMBID != "rec-123" || h.ReleaseMBID != "rel-456" || h.ArtistMBID != "art-789" {
		t.Errorf("MBIDs = (%q,%q,%q)", h.RecordingMBID, h.ReleaseMBID, h.ArtistMBID)
	}
}

func TestMapCaseAndSpellingInsensitive(t *testing.T) {
	// Vorbis-style lowercase + alternate album_artist spelling.
	h := MapHydration(map[string]string{
		"title":        "Song",
		"tracknumber":  "5",
		"discnumber":   "2",
		"album_artist": "VA",
	}, "/x.ogg")
	if h.Title != "Song" || h.TrackNumber != 5 || h.DiscNumber != 2 || h.AlbumArtist != "VA" {
		t.Fatalf("case/spelling-insensitive lookup failed: %+v", h)
	}
}

func TestMapMultiValuedArtistsAndGenresLiteral(t *testing.T) {
	// NUL-separated repeated frames (FFmpeg) and ";"-joined values split; no
	// "feat." splitting — values stay literal.
	h := MapHydration(map[string]string{
		"ARTIST": "Run the Jewels\x00Zack de la Rocha",
		"GENRE":  "Hip-Hop; Rap",
		"TITLE":  "Close Your Eyes (feat. Zack de la Rocha)",
	}, "/x.mp3")
	if len(h.Credits) != 2 {
		t.Fatalf("Credits = %+v, want 2", h.Credits)
	}
	if h.Credits[0].Artist != "Run the Jewels" || h.Credits[1].Artist != "Zack de la Rocha" {
		t.Errorf("Credits = %+v", h.Credits)
	}
	if len(h.Genres) != 2 || h.Genres[0] != "Hip-Hop" || h.Genres[1] != "Rap" {
		t.Errorf("Genres = %v", h.Genres)
	}
	// "feat." in the title is preserved verbatim — no split into a credit.
	if h.Title != "Close Your Eyes (feat. Zack de la Rocha)" {
		t.Errorf("Title must stay literal, got %q", h.Title)
	}
}

func TestSplitTagValuesSlashIsNotADelimiter(t *testing.T) {
	// "/" is NOT a delimiter for splitTagValues — only NUL and ";" split.
	got := splitTagValues("A/B")
	if len(got) != 1 || got[0] != "A/B" {
		t.Errorf("splitTagValues(%q) = %v, want single-element [%q]", "A/B", got, "A/B")
	}
}

func TestMapBareYearAndPartialDate(t *testing.T) {
	// Bare year → year only, zero date.
	h := MapHydration(map[string]string{"DATE": "1985", "TITLE": "t"}, "/x.flac")
	if h.ReleaseYear != 1985 || !h.ReleaseDate.IsZero() {
		t.Errorf("bare year: year=%d date=%v, want 1985 / zero", h.ReleaseYear, h.ReleaseDate)
	}
	// Year-month → full date kept.
	h = MapHydration(map[string]string{"DATE": "2003-09", "TITLE": "t"}, "/x.flac")
	if h.ReleaseYear != 2003 || h.ReleaseDate.IsZero() {
		t.Errorf("year-month: year=%d date=%v", h.ReleaseYear, h.ReleaseDate)
	}
	// Messy taggers: leading year in a noisy string.
	h = MapHydration(map[string]string{"DATE": "1998 (Remastered)", "TITLE": "t"}, "/x.flac")
	if h.ReleaseYear != 1998 {
		t.Errorf("noisy date: year=%d, want 1998", h.ReleaseYear)
	}
}

// TestMapDiscNumberAbsentDefaultsToZero: a single-disc release commonly carries
// no DISC/DISCNUMBER tag at all. MapHydration must leave DiscNumber at its
// zero value in that case (not synthesize disc 1) while still hydrating the
// track's other present fields — a regression that defaulted absent discs to
// 1 would misrepresent an unknown ordinal as a known one.
func TestMapDiscNumberAbsentDefaultsToZero(t *testing.T) {
	h := MapHydration(map[string]string{
		"TITLE": "Idioteque",
		"TRACK": "8",
		"ALBUM": "Kid A",
	}, "/music/radiohead/kid-a/08.flac")
	if h.DiscNumber != 0 {
		t.Errorf("DiscNumber = %d, want 0 (no disc tag present)", h.DiscNumber)
	}
	if h.TrackNumber != 8 || h.Title != "Idioteque" || h.Album != "Kid A" {
		t.Errorf("other fields not hydrated: %+v", h)
	}
	if h.Empty() {
		t.Error("Hydration must not report Empty when title/track/album are present")
	}
}

// TestNormalizeTagsCollisionDeterministic: two source spellings that collide
// after key normalization ("TITLE"/"title") must resolve the same way on every
// call — the first non-empty value in sorted source-key order wins ("TITLE"
// sorts before "title"), a fixed rule rather than map iteration order. The
// sdk.FilenameParser determinism contract reaches MapHydration through
// Parser.Parse, so a random winner here would make Parse itself flaky.
func TestNormalizeTagsCollisionDeterministic(t *testing.T) {
	tags := map[string]string{"TITLE": "From Uppercase", "title": "From Lowercase"}
	for i := 0; i < 32; i++ {
		h := MapHydration(tags, "/x.flac")
		if h.Title != "From Uppercase" {
			t.Fatalf("iteration %d: Title = %q, want %q (first non-empty value in sorted source-key order)",
				i, h.Title, "From Uppercase")
		}
	}
}

func TestLeadingInt(t *testing.T) {
	for in, want := range map[string]int{"5": 5, "5/12": 5, "05": 5, "": 0, "x": 0, "12/12": 12} {
		if got := leadingInt(in); got != want {
			t.Errorf("leadingInt(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestDecideHydrationWrite(t *testing.T) {
	cases := []struct {
		name              string
		recorded, current string
		want              HydrationWrite
	}{
		{"never hydrated", "", "sha256:aaa", HydrationWriteFresh},
		{"hydrated before fingerprints were recorded", "", "", HydrationWriteFresh},
		{"unchanged bytes", "sha256:aaa", "sha256:aaa", HydrationWriteSkip},
		{"no current fingerprint cannot prove a change", "sha256:aaa", "", HydrationWriteSkip},
		{"re-tagged bytes", "sha256:aaa", "sha256:bbb", HydrationWriteReplace},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecideHydrationWrite(tc.recorded, tc.current); got != tc.want {
				t.Fatalf("DecideHydrationWrite(%q, %q) = %d, want %d", tc.recorded, tc.current, got, tc.want)
			}
		})
	}
}

// The re-tag path must be a write-free no-op for unchanged bytes: a seam that
// writes only on Fresh/Replace performs zero writes across repeated rescans of
// the same fingerprint and exactly one when the fingerprint moves.
func TestDecideHydrationWriteCountsWritesOnlyOnChange(t *testing.T) {
	writes := 0
	recorded := ""
	hydrate := func(current string) {
		switch DecideHydrationWrite(recorded, current) {
		case HydrationWriteFresh, HydrationWriteReplace:
			writes++
			recorded = current
		case HydrationWriteSkip:
		}
	}
	hydrate("sha256:v1")
	hydrate("sha256:v1")
	hydrate("sha256:v1")
	if writes != 1 {
		t.Fatalf("unchanged rescans must not write: writes = %d, want 1", writes)
	}
	hydrate("sha256:v2")
	if writes != 2 {
		t.Fatalf("a moved fingerprint must write exactly once: writes = %d, want 2", writes)
	}
}
