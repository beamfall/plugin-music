package music

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestParseLastfmCSVPositional parses the classic header-less Last.fm export
// shape `artist, album, track, timestamp` (the lastfm-to-csv / Universal
// Scrobbler default), with a unix-seconds timestamp.
func TestParseLastfmCSVPositional(t *testing.T) {
	const uts = 1717707000 // 2024-06-06T20:50:00Z
	csv := "Radiohead,OK Computer,Paranoid Android," + itoa(uts) + "\n" +
		"Radiohead,OK Computer,Karma Police," + itoa(uts+200) + "\n"

	got, err := ParseLastfmCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d scrobbles, want 2", len(got))
	}
	first := got[0]
	if first.Artist != "Radiohead" || first.Track != "Paranoid Android" || first.Album != "OK Computer" {
		t.Fatalf("first scrobble = %+v, want Radiohead / Paranoid Android / OK Computer", first)
	}
	if !first.Present {
		t.Fatal("scrobble not marked Present")
	}
	if want := time.Unix(uts, 0).UTC(); !first.PlayedAt.Equal(want) {
		t.Fatalf("PlayedAt = %v, want %v", first.PlayedAt, want)
	}
}

// TestParseLastfmCSVHeadered parses the headered export shape, where the column
// order differs from positional and the timestamp is the `uts` column among
// several date-ish columns. Confirms the header is consumed (not parsed as a
// scrobble) and that column names drive the mapping.
func TestParseLastfmCSVHeadered(t *testing.T) {
	const uts = 1700000000
	csv := "uts,utc_time,artist,track,album\n" +
		itoa(uts) + ",2023-11-14 22:13,Boards of Canada,Roygbiv,Music Has the Right to Children\n"

	got, err := ParseLastfmCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d scrobbles, want 1 (header consumed)", len(got))
	}
	s := got[0]
	if s.Artist != "Boards of Canada" || s.Track != "Roygbiv" || s.Album != "Music Has the Right to Children" {
		t.Fatalf("scrobble = %+v, want Boards of Canada / Roygbiv / Music Has the Right to Children", s)
	}
	if want := time.Unix(uts, 0).UTC(); !s.PlayedAt.Equal(want) {
		t.Fatalf("PlayedAt = %v (from uts column), want %v", s.PlayedAt, want)
	}
}

// TestParseLastfmCSVDateStringAndBlankRows tolerates a date-string timestamp
// (not unix-seconds) and skips blank/trailing rows.
func TestParseLastfmCSVDateStringAndBlankRows(t *testing.T) {
	csv := "artist,track,album,date\n" +
		"Aphex Twin,Xtal,Selected Ambient Works 85-92,06 Jun 2024 21:30\n" +
		",,,\n" // trailing blank line

	got, err := ParseLastfmCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d scrobbles, want 1 (blank row skipped)", len(got))
	}
	want := time.Date(2024, 6, 6, 21, 30, 0, 0, time.UTC)
	if !got[0].PlayedAt.Equal(want) {
		t.Fatalf("PlayedAt = %v, want %v", got[0].PlayedAt, want)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// TestParseLastfmCSVDataRowWithCoincidentalColumnNameToken: a header-less
// positional export whose FIRST data row carries a cell that coincidentally
// spells a recognized column name (the track title "Time" — a real Last.fm
// staple) must still parse positionally. A single recognized cell must not be
// swallowed as a header row, which would misalign every column and silently
// import zero scrobbles.
func TestParseLastfmCSVDataRowWithCoincidentalColumnNameToken(t *testing.T) {
	const uts = 1717707000
	csv := "Pink Floyd,The Dark Side of the Moon,Time," + itoa(uts) + "\n" +
		"Pink Floyd,The Dark Side of the Moon,Breathe," + itoa(uts+200) + "\n"

	got, err := ParseLastfmCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d scrobbles, want 2 (a data row must not be consumed as a header)", len(got))
	}
	first := got[0]
	if first.Artist != "Pink Floyd" || first.Track != "Time" || first.Album != "The Dark Side of the Moon" {
		t.Fatalf("first scrobble = %+v, want Pink Floyd / Time / The Dark Side of the Moon", first)
	}
	if want := time.Unix(uts, 0).UTC(); !first.PlayedAt.Equal(want) {
		t.Fatalf("PlayedAt = %v, want %v", first.PlayedAt, want)
	}
}

// TestParseLastfmCSVPartialHeaderRow tolerates a row shorter than the header
// (trailing columns — album, uts — omitted entirely from that row rather than
// left blank) — field() must return "" for the missing columns instead of
// panicking or misaligning the remaining fields.
func TestParseLastfmCSVPartialHeaderRow(t *testing.T) {
	csv := "artist,track,album,uts\n" +
		"Radiohead,Paranoid Android\n" // album and uts columns dropped entirely

	got, err := ParseLastfmCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d scrobbles, want 1", len(got))
	}
	s := got[0]
	if s.Artist != "Radiohead" || s.Track != "Paranoid Android" {
		t.Fatalf("scrobble = %+v, want Radiohead / Paranoid Android", s)
	}
	if s.Album != "" {
		t.Errorf("Album = %q, want empty for a row missing the album column", s.Album)
	}
	// The uts column (index 3) is out of range for this short row, so the
	// timestamp is unparseable and PlayedAt stays zero — the play still counts.
	if !s.PlayedAt.IsZero() {
		t.Errorf("PlayedAt = %v, want zero (timestamp column out of range)", s.PlayedAt)
	}
	if !s.Present {
		t.Error("scrobble not marked Present")
	}
}

// TestParseLastfmCSVOversizedInputBoundedError feeds a CSV input larger than
// maxImportBytes and asserts ParseLastfmCSV returns a clean bounded error
// instead of silently truncating the export or growing memory unbounded.
func TestParseLastfmCSVOversizedInputBoundedError(t *testing.T) {
	row := "Radiohead,OK Computer,Paranoid Android,1717707000\n"
	rows := maxImportBytes/len(row) + 2 // guarantee we cross the cap
	oversized := strings.NewReader(strings.Repeat(row, rows))

	_, err := ParseLastfmCSV(oversized)
	if !errors.Is(err, errImportTooLarge) {
		t.Fatalf("err = %v, want errImportTooLarge", err)
	}
}
