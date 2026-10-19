package music

import (
	"bytes"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/beamfall/plugin-sdk"
)

func FuzzParseFilename(f *testing.F) {
	seeds := []struct {
		path, title, track, disc, date string
	}{
		{path: "05 - Great Song 1999.flac"},
		{path: "1-05 Artist - Title.mp3"},
		{path: "07.flac"},
		{path: "交響曲.flac"},
		{path: "ignored.flac", title: "Tagged Title", track: "2/12", disc: "1/2", date: "2024-06-01"},
		{path: "Ⱥurora 1999.flac"},
	}
	for _, seed := range seeds {
		f.Add(seed.path, seed.title, seed.track, seed.disc, seed.date)
	}

	f.Fuzz(func(t *testing.T, path, title, track, disc, date string) {
		probe := sdk.ParseProbe{Tags: map[string]string{
			"TITLE": title,
			"TRACK": track,
			"DISC":  disc,
			"DATE":  date,
		}}
		parser := NewParser()
		got, ok := parser.Parse(path, probe)
		again, againOK := parser.Parse(path, probe)
		if ok != againOK || !reflect.DeepEqual(got, again) {
			t.Fatalf("Parse is not deterministic: first=(%#v,%v) second=(%#v,%v)", got, ok, again, againOK)
		}
		if !ok {
			if !reflect.DeepEqual(got, sdk.ParsedMedia{}) {
				t.Fatalf("failed parse returned non-zero media: %#v", got)
			}
			return
		}
		if got.Class != "audio" {
			t.Fatalf("successful parse class = %q, want audio", got.Class)
		}
		if got.Title == "" || got.Title != strings.TrimSpace(got.Title) {
			t.Fatalf("successful parse returned invalid title %q", got.Title)
		}
		if got.Index < 0 || got.Year < 0 {
			t.Fatalf("successful parse returned negative ordinal/year: %#v", got)
		}
		if got.Sequence != "" {
			sequence, err := strconv.Atoi(got.Sequence)
			if err != nil || sequence <= 0 {
				t.Fatalf("successful parse returned invalid sequence %q", got.Sequence)
			}
		}
	})
}

func FuzzParseLastfmCSV(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte("artist,track,album,uts\nRadiohead,Paranoid Android,OK Computer,1700000000\n"),
		[]byte("Radiohead,OK Computer,Time,1700000000\n"),
		[]byte("artist,track\nAphex Twin,Xtal\n"),
		[]byte("\"unterminated"),
		[]byte(""),
		[]byte("艺术家,曲目,专辑,not-a-time\n"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := ParseLastfmCSV(bytes.NewReader(data))
		again, againErr := ParseLastfmCSV(bytes.NewReader(data))
		assertDeterministicParse(t, got, err, again, againErr)
		if err != nil {
			if got != nil {
				t.Fatalf("failed CSV parse returned records: %#v", got)
			}
			return
		}
		for index, record := range got {
			if !record.Present {
				t.Fatalf("record %d is not marked present: %#v", index, record)
			}
			if strings.TrimSpace(record.Artist) == "" && strings.TrimSpace(record.Track) == "" {
				t.Fatalf("record %d has neither artist nor track: %#v", index, record)
			}
			if record.Artist != strings.TrimSpace(record.Artist) ||
				record.Track != strings.TrimSpace(record.Track) ||
				record.Album != strings.TrimSpace(record.Album) {
				t.Fatalf("record %d contains untrimmed fields: %#v", index, record)
			}
		}
	})
}

func FuzzParseListenBrainzJSON(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`[{"listened_at":1700000000,"track_metadata":{"artist_name":"Boards of Canada","track_name":"Roygbiv"}}]`),
		[]byte(`{"payload":{"listens":[{"track_metadata":{"track_name":"Xtal","additional_info":{"recording_mbid":"mbid-1"}}}]}}`),
		[]byte("{\"track_name\":\"Bare track\"}\n{\"additional_info\":{\"recording_mbids\":[\"mbid-2\"]}}\n"),
		[]byte(`{"listened_at":{},"additional_info":{"recording_mbid":123}}`),
		[]byte("<html>not json</html>"),
		[]byte(""),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := ParseListenBrainzJSON(bytes.NewReader(data))
		again, againErr := ParseListenBrainzJSON(bytes.NewReader(data))
		assertDeterministicParse(t, got, err, again, againErr)
		if err != nil {
			if got != nil {
				t.Fatalf("failed ListenBrainz parse returned items: %#v", got)
			}
			return
		}
		for index, item := range got {
			if !item.Scrobble.Present && len(item.ExternalIDs) == 0 {
				t.Fatalf("item %d has neither scrobble nor external identity: %#v", index, item)
			}
			if item.Scrobble.Present && strings.TrimSpace(item.Scrobble.Artist) == "" && strings.TrimSpace(item.Scrobble.Track) == "" {
				t.Fatalf("item %d present scrobble has neither artist nor track: %#v", index, item)
			}
			for _, externalID := range item.ExternalIDs {
				if externalID.Scheme != recordingMBIDScheme || externalID.Value == "" || externalID.Value != strings.TrimSpace(externalID.Value) {
					t.Fatalf("item %d returned invalid external ID: %#v", index, externalID)
				}
			}
		}
	})
}

func FuzzParseLastfmEnvelopeJSON(f *testing.F) {
	seeds := []struct {
		data        []byte
		wantSuccess bool
	}{
		{[]byte(`{}`), true},
		{[]byte(`{"scrobbles":{"@attr":{"accepted":1,"ignored":0}}}`), true},
		{[]byte(`{"error":0}`), true},
		{[]byte(`{"error":29,"message":"Rate limit exceeded"}`), false},
		{[]byte(`{"error":null}`), false},
		{[]byte(`null`), false},
		{[]byte(`[]`), false},
		{[]byte(`{"error":"attacker-controlled"}`), false},
		{[]byte(`{"error":999999999999999999999999999999}`), false},
		{[]byte(`<html>not json</html>`), false},
		{[]byte{}, true},
		{bytes.Repeat([]byte("x"), lastfmEnvelopeBodyLimit+1), false},
	}
	seedOutcomes := make(map[string]bool, len(seeds))
	for _, seed := range seeds {
		seedOutcomes[string(seed.data)] = seed.wantSuccess
		f.Add(seed.data)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		parse := func() error {
			return lastfmEnvelopeError(&http.Response{
				Body: io.NopCloser(bytes.NewReader(data)),
			})
		}

		err := parse()
		againErr := parse()
		if wantSuccess, known := seedOutcomes[string(data)]; known && (err == nil) != wantSuccess {
			t.Fatalf("Last.fm envelope seed success=%v, want %v: %q", err == nil, wantSuccess, data)
		}
		if (err == nil) != (againErr == nil) {
			t.Fatalf("Last.fm envelope result is not deterministic: first=%v second=%v", err, againErr)
		}
		if err != nil {
			if err.Error() != againErr.Error() {
				t.Fatalf("Last.fm envelope error is not deterministic: first=%q second=%q", err, againErr)
			}
			if len(err.Error()) > 256 {
				t.Fatalf("Last.fm envelope error is unbounded: %d bytes", len(err.Error()))
			}
		}
		if len(data) == 0 && err != nil {
			t.Fatalf("documented empty success body returned error: %v", err)
		}
		if len(data) > lastfmEnvelopeBodyLimit && err == nil {
			t.Fatal("oversized Last.fm envelope was accepted")
		}
	})
}

func assertDeterministicParse(t *testing.T, got any, err error, again any, againErr error) {
	t.Helper()
	if (err == nil) != (againErr == nil) {
		t.Fatalf("parse error is not deterministic: first=%v second=%v", err, againErr)
	}
	if err != nil && err.Error() != againErr.Error() {
		t.Fatalf("parse error is not deterministic: first=%q second=%q", err, againErr)
	}
	if !reflect.DeepEqual(got, again) {
		t.Fatalf("parse result is not deterministic: first=%#v second=%#v", got, again)
	}
}
