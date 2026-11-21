package music_test

import (
	"reflect"
	"strings"
	"testing"

	music "github.com/beamfall/plugin-music"
	sdk "github.com/beamfall/plugin-sdk"
)

// TestParseSearchHints covers the musicbrainz search clauses the parser
// derives from tags (CORE-2333): recording MBID, first artist and tagged
// album, Lucene-quoted; an untagged file and an over-long clause give none.
func TestParseSearchHints(t *testing.T) {
	parser := music.NewParser()
	parsed, ok := parser.Parse("x/y/01 - Song.flac", sdk.ParseProbe{HasAudio: true, Tags: map[string]string{
		"title":               "Song",
		"artist":              `The "Quoted" Band`,
		"album":               "Hits",
		"musicbrainz_trackid": "0D3A8E2B-1111-4222-8333-944445555666",
	}})
	if !ok {
		t.Fatal("Parse failed")
	}
	want := map[string]string{
		"recording_clause": " AND rid:0d3a8e2b-1111-4222-8333-944445555666",
		"artist_clause":    ` AND artist:"The \"Quoted\" Band"`,
		"album_clause":     ` AND release:"Hits"`,
	}
	if !reflect.DeepEqual(parsed.Hints, want) {
		t.Errorf("Hints = %#v, want %#v", parsed.Hints, want)
	}
	if err := sdk.ValidateParseHints(parsed); err != nil {
		t.Errorf("ValidateParseHints: %v", err)
	}

	// Path-only: the folder-derived album is not a search filter.
	parsed, _ = parser.Parse("Artist/Album/01 - Song.flac", sdk.ParseProbe{HasAudio: true})
	if parsed.Hints != nil {
		t.Errorf("untagged Hints = %#v, want none", parsed.Hints)
	}

	parsed, _ = parser.Parse("a.flac", sdk.ParseProbe{HasAudio: true, Tags: map[string]string{
		"title": "Song", "artist": strings.Repeat("a", 300),
	}})
	if _, ok := parsed.Hints["artist_clause"]; ok {
		t.Errorf("over-long artist clause kept: %d bytes", len(parsed.Hints["artist_clause"]))
	}
}
