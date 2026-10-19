package music

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseListenBrainzJSON(t *testing.T) {
	input := strings.NewReader(`{
  "payload": {
    "listens": [
      {
        "listened_at": 1700000000,
        "track_metadata": {
          "artist_name": "Boards of Canada",
          "track_name": "Roygbiv",
          "release_name": "Music Has the Right to Children",
          "additional_info": {"recording_mbid": "mbid-roygbiv"}
        }
      }
    ]
  }
}`)
	got, err := ParseListenBrainzJSON(input)
	if err != nil {
		t.Fatalf("ParseListenBrainzJSON returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	item := got[0]
	if !item.Scrobble.Present || item.Scrobble.Artist != "Boards of Canada" || item.Scrobble.Track != "Roygbiv" || item.Scrobble.Album != "Music Has the Right to Children" {
		t.Fatalf("scrobble = %+v", item.Scrobble)
	}
	want := time.Unix(1700000000, 0).UTC()
	if !item.Scrobble.PlayedAt.Equal(want) {
		t.Fatalf("played at = %v, want %v", item.Scrobble.PlayedAt, want)
	}
	if len(item.ExternalIDs) != 1 || item.ExternalIDs[0].Scheme != "musicbrainz-recording" || item.ExternalIDs[0].Value != "mbid-roygbiv" {
		t.Fatalf("external IDs = %#v", item.ExternalIDs)
	}
}

func TestParseListenBrainzJSONLines(t *testing.T) {
	input := strings.NewReader(`{"listened_at":"1700000000","track_metadata":{"artist_name":"Aphex Twin","track_name":"Xtal","release_name":"Selected Ambient Works 85-92"}}
{"listened_at":1700003600,"track_metadata":{"artist_name":"Boards of Canada","track_name":"Roygbiv","additional_info":{"recording_mbids":["mbid-roygbiv"]}}}
`)
	got, err := ParseListenBrainzJSON(input)
	if err != nil {
		t.Fatalf("ParseListenBrainzJSON returned error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if got[0].Scrobble.Artist != "Aphex Twin" || got[0].Scrobble.Track != "Xtal" {
		t.Fatalf("first row = %+v", got[0].Scrobble)
	}
	if len(got[1].ExternalIDs) != 1 || got[1].ExternalIDs[0].Value != "mbid-roygbiv" {
		t.Fatalf("second external IDs = %#v", got[1].ExternalIDs)
	}
}

// TestParseListenBrainzJSONBareObject parses a single top-level JSON object
// carrying listen fields directly at the root (no "listens" or "payload"
// wrapper) — the third shape ParseListenBrainzJSON documents. The wrapper
// unmarshal succeeds but yields no listens, so parseListenBrainzObject must
// fall back to unmarshaling the bare object as a single listenBrainzListen.
func TestParseListenBrainzJSONBareObject(t *testing.T) {
	input := strings.NewReader(`{
  "listened_at": 1700000000,
  "track_metadata": {
    "artist_name": "Boards of Canada",
    "track_name": "Roygbiv",
    "release_name": "Music Has the Right to Children",
    "additional_info": {"recording_mbid": "mbid-roygbiv"}
  }
}`)
	got, err := ParseListenBrainzJSON(input)
	if err != nil {
		t.Fatalf("ParseListenBrainzJSON returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	item := got[0]
	if !item.Scrobble.Present || item.Scrobble.Artist != "Boards of Canada" || item.Scrobble.Track != "Roygbiv" || item.Scrobble.Album != "Music Has the Right to Children" {
		t.Fatalf("scrobble = %+v", item.Scrobble)
	}
	want := time.Unix(1700000000, 0).UTC()
	if !item.Scrobble.PlayedAt.Equal(want) {
		t.Fatalf("played at = %v, want %v", item.Scrobble.PlayedAt, want)
	}
	if len(item.ExternalIDs) != 1 || item.ExternalIDs[0].Value != "mbid-roygbiv" {
		t.Fatalf("external IDs = %#v", item.ExternalIDs)
	}
}

// TestParseListenBrainzJSONOversizedInputBoundedError feeds a source larger
// than maxImportBytes and asserts ParseListenBrainzJSON returns a clean
// bounded error instead of silently truncating or growing memory unbounded.
func TestParseListenBrainzJSONOversizedInputBoundedError(t *testing.T) {
	line := `{"listened_at":1700000000,"track_metadata":{"artist_name":"Boards of Canada","track_name":"Roygbiv"}}` + "\n"
	lines := maxImportBytes/len(line) + 2 // guarantee we cross the cap
	oversized := strings.NewReader(strings.Repeat(line, lines))

	_, err := ParseListenBrainzJSON(oversized)
	if !errors.Is(err, errImportTooLarge) {
		t.Fatalf("err = %v, want errImportTooLarge", err)
	}
}

// TestParseListenBrainzJSONGarbageInputReturnsError feeds a payload that is
// neither JSON array nor object (e.g. an HTML error page saved with a .json
// extension) and asserts a diagnosable error rather than a silent empty
// result — without this check the operator sees "0 imported" instead of
// "wrong file".
func TestParseListenBrainzJSONGarbageInputReturnsError(t *testing.T) {
	input := strings.NewReader("<html><body>502 Bad Gateway</body></html>")

	got, err := ParseListenBrainzJSON(input)
	if !errors.Is(err, errNotListenBrainzJSON) {
		t.Fatalf("err = %v, want errNotListenBrainzJSON", err)
	}
	if got != nil {
		t.Fatalf("got = %#v, want nil", got)
	}
}

// TestParseListenBrainzJSONLinesOversizedLineBoundedError feeds a single JSONL
// line larger than the scanner's raised buffer cap and asserts a clean bounded
// error instead of a silent truncation of the line.
func TestParseListenBrainzJSONLinesOversizedLineBoundedError(t *testing.T) {
	pad := strings.Repeat("x", maxListenBrainzLineBytes+1)
	oversizedLine := `{"listened_at":1700000000,"track_metadata":{"artist_name":"` + pad + `","track_name":"Roygbiv"}}` + "\n" +
		`{"listened_at":1700003600,"track_metadata":{"artist_name":"Aphex Twin","track_name":"Xtal"}}` + "\n"

	_, err := parseListenBrainzJSONLines([]byte(oversizedLine))
	if err == nil {
		t.Fatal("err = nil, want a bounded scanner error for the oversized line")
	}
}

// TestFirstRecordingMBIDIgnoresNonStringValues asserts a recording_mbid (or
// recording_mbids element) that decoded to a non-string JSON value — a
// number, bool, or null, which any of additional_info's any-typed fields can
// legally hold from a malformed/hostile export — is safely ignored (returns
// "") rather than panicking on a failed type assertion.
func TestFirstRecordingMBIDIgnoresNonStringValues(t *testing.T) {
	cases := []struct {
		name string
		info map[string]any
	}{
		{name: "recording_mbid-number", info: map[string]any{"recording_mbid": 12345}},
		{name: "recording_mbid-bool", info: map[string]any{"recording_mbid": true}},
		{name: "recording_mbid-null", info: map[string]any{"recording_mbid": nil}},
		{name: "recording_mbids-number-element", info: map[string]any{"recording_mbids": []any{42}}},
		{name: "recording_mbids-bool-element", info: map[string]any{"recording_mbids": []any{false}}},
		{name: "recording_mbids-null-element", info: map[string]any{"recording_mbids": []any{nil}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := firstRecordingMBID(c.info); got != "" {
				t.Errorf("firstRecordingMBID(%#v) = %q, want \"\"", c.info, got)
			}
		})
	}
}

// TestStringValueIgnoresNonStringTypes covers stringValue directly: only the
// string case returns its value, every other JSON-decoded type returns "".
func TestStringValueIgnoresNonStringTypes(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{name: "string", value: "mbid-1", want: "mbid-1"},
		{name: "number", value: float64(7), want: ""},
		{name: "bool", value: true, want: ""},
		{name: "nil", value: nil, want: ""},
		{name: "map", value: map[string]any{"x": 1}, want: ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stringValue(c.value); got != c.want {
				t.Errorf("stringValue(%#v) = %q, want %q", c.value, got, c.want)
			}
		})
	}
}
