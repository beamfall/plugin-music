package music

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	sdk "github.com/beamfall/plugin-sdk"
)

const navidromeFixture = `{
  "playlists": [
    {
      "name": "Chill Evenings",
      "tracks": [
        {"artist": "Boards of Canada", "title": "Roygbiv", "album": "Music Has the Right to Children"},
        {"artist": "Aphex Twin", "title": "Xtal", "album": "Selected Ambient Works 85-92"}
      ]
    }
  ],
  "plays": [
    {
      "artist": "Boards of Canada",
      "track": "Roygbiv",
      "album": "Music Has the Right to Children",
      "play_count": 3,
      "last_played": "2024-06-06T21:30:00Z",
      "mbid": "mbid-roygbiv"
    }
  ]
}`

// TestParseNavidromeJSONPlaylistBecomesStaticSmartCollection proves a
// Navidrome playlist imports as an sdk.SmartCollection static snapshot
// (name + member count) — the seam's only collections mechanism — carrying no Path/Scrobble.
func TestParseNavidromeJSONPlaylistBecomesStaticSmartCollection(t *testing.T) {
	got, err := ParseNavidromeJSON(strings.NewReader(navidromeFixture))
	if err != nil {
		t.Fatalf("ParseNavidromeJSON returned error: %v", err)
	}

	var collections int
	for _, item := range got {
		if !item.SmartCollection.Present {
			continue
		}
		collections++
		if item.SmartCollection.Name != "Chill Evenings" {
			t.Errorf("SmartCollection.Name = %q, want Chill Evenings", item.SmartCollection.Name)
		}
		if item.SmartCollection.MemberCount != 2 {
			t.Errorf("SmartCollection.MemberCount = %d, want 2", item.SmartCollection.MemberCount)
		}
		if item.Scrobble.Present {
			t.Error("playlist item carries a Present scrobble, want none")
		}
	}
	if collections != 1 {
		t.Fatalf("collections = %d, want 1", collections)
	}
}

// TestParseNavidromeJSONPlayCountExpandsToOneItemPerPlay proves a play row
// with play_count N contributes N scrobble-shaped foreign items — additive
// through the existing scrobble match/tally chain — each carrying the
// recorded last-played instant and the recording MBID external ID.
func TestParseNavidromeJSONPlayCountExpandsToOneItemPerPlay(t *testing.T) {
	got, err := ParseNavidromeJSON(strings.NewReader(navidromeFixture))
	if err != nil {
		t.Fatalf("ParseNavidromeJSON returned error: %v", err)
	}

	var scrobbles int
	want := time.Date(2024, 6, 6, 21, 30, 0, 0, time.UTC)
	for _, item := range got {
		if !item.Scrobble.Present {
			continue
		}
		scrobbles++
		if item.Scrobble.Artist != "Boards of Canada" || item.Scrobble.Track != "Roygbiv" || item.Scrobble.Album != "Music Has the Right to Children" {
			t.Errorf("scrobble = %+v", item.Scrobble)
		}
		if !item.Scrobble.PlayedAt.Equal(want) {
			t.Errorf("PlayedAt = %v, want %v", item.Scrobble.PlayedAt, want)
		}
		if len(item.ExternalIDs) != 1 || item.ExternalIDs[0].Scheme != "musicbrainz-recording" || item.ExternalIDs[0].Value != "mbid-roygbiv" {
			t.Errorf("external IDs = %#v", item.ExternalIDs)
		}
	}
	if scrobbles != 3 {
		t.Fatalf("scrobbles = %d, want 3 (play_count)", scrobbles)
	}
}

// TestParseNavidromeJSONIsIdempotent proves the parser is a pure function of
// its input — no package-level or parse-carried state — so re-running an
// import against the same export deterministically re-yields the identical
// item set rather than growing or drifting on a second pass (the source side
// of "additive, idempotent re-run"; the non-double-apply
// guarantee itself lives in the shared migrate.Handler play tally).
func TestParseNavidromeJSONIsIdempotent(t *testing.T) {
	first, err := ParseNavidromeJSON(strings.NewReader(navidromeFixture))
	if err != nil {
		t.Fatalf("first parse returned error: %v", err)
	}
	second, err := ParseNavidromeJSON(strings.NewReader(navidromeFixture))
	if err != nil {
		t.Fatalf("second parse returned error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("re-parse diverged:\nfirst  = %#v\nsecond = %#v", first, second)
	}
}

// TestParseNavidromeJSONZeroOrMissingPlayCountSkipped proves a play row with
// a zero or missing play_count contributes nothing — while a row with no
// timestamp still counts the play (zero
// PlayedAt, "no last-played" rather than dropping the play).
func TestParseNavidromeJSONZeroOrMissingPlayCountSkipped(t *testing.T) {
	input := `{"plays": [
    {"artist": "A", "track": "B", "play_count": 0},
    {"artist": "A", "track": "C"},
    {"artist": "C", "track": "D", "play_count": 1}
  ]}`
	got, err := ParseNavidromeJSON(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseNavidromeJSON returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d items, want 1", len(got))
	}
	if !got[0].Scrobble.PlayedAt.IsZero() {
		t.Errorf("PlayedAt = %v, want zero (no last_played recorded)", got[0].Scrobble.PlayedAt)
	}
}

// TestParseNavidromeJSONOversizedInputBoundedError feeds a source larger than
// maxImportBytes and asserts ParseNavidromeJSON returns a clean bounded error
// instead of silently truncating or growing memory unbounded.
func TestParseNavidromeJSONOversizedInputBoundedError(t *testing.T) {
	line := `{"artist":"A","track":"B","play_count":1},`
	padding := strings.Repeat(line, maxImportBytes/len(line)+2)
	oversized := strings.NewReader(`{"plays":[` + padding + `{"artist":"Z","track":"Y","play_count":1}]}`)

	_, err := ParseNavidromeJSON(oversized)
	if !errors.Is(err, errImportTooLarge) {
		t.Fatalf("err = %v, want errImportTooLarge", err)
	}
}

// TestParseNavidromeJSONGarbageInputReturnsError feeds a payload that is not
// a JSON object (e.g. an HTML error page saved with a .json extension) and
// asserts a diagnosable error rather than a silent empty result.
func TestParseNavidromeJSONGarbageInputReturnsError(t *testing.T) {
	got, err := ParseNavidromeJSON(strings.NewReader("<html><body>502 Bad Gateway</body></html>"))
	if !errors.Is(err, errNotNavidromeJSON) {
		t.Fatalf("err = %v, want errNotNavidromeJSON", err)
	}
	if got != nil {
		t.Fatalf("got = %#v, want nil", got)
	}
}

// TestParseNavidromeJSONEmptyInputReturnsEmptySlice mirrors the other
// importers' empty-input behavior: an empty file is not an error, just
// nothing to import.
func TestParseNavidromeJSONEmptyInputReturnsEmptySlice(t *testing.T) {
	got, err := ParseNavidromeJSON(strings.NewReader("   "))
	if err != nil {
		t.Fatalf("ParseNavidromeJSON returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d items, want 0", len(got))
	}
}

// TestNavidromeSourceCategoriesIncludeCollectionsAndWatchState pins the
// Categories contract the resolver/handler read to decide which state a run
// applies.
func TestNavidromeSourceCategoriesIncludeCollectionsAndWatchState(t *testing.T) {
	src := NewNavidromeSource("unused.json")
	cats := src.Categories()
	if len(cats) != 2 || cats[0] != "collections" || cats[1] != "watch-state" {
		t.Fatalf("Categories() = %v, want [collections watch-state]", cats)
	}
}

func TestParseNavidromeJSONRejectsInvalidCounts(t *testing.T) {
	tests := []struct {
		name  string
		count string
		want  error
	}{
		{name: "negative", count: "-1", want: ErrNavidromeInvalidCount},
		{name: "fraction", count: "1.5", want: ErrNavidromeInvalidCount},
		{name: "exponent", count: "1e2", want: ErrNavidromeInvalidCount},
		{name: "overflow", count: "18446744073709551616", want: ErrNavidromeInvalidCount},
		{name: "null", count: "null", want: ErrNavidromeInvalidCount},
		{name: "string", count: `"1"`, want: ErrNavidromeInvalidCount},
		{name: "boolean", count: "true", want: ErrNavidromeInvalidCount},
		{name: "single row over limit", count: "100001", want: ErrNavidromeExpansionLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"plays":[{"artist":"A","track":"B","play_count":%s}]}`, tt.count)
			got, err := ParseNavidromeJSON(strings.NewReader(input))
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want errors.Is(_, %v)", err, tt.want)
			}
			if got != nil {
				t.Fatalf("got = %#v, want nil", got)
			}
		})
	}
}

func TestParseNavidromeJSONRejectsAggregateOverLimit(t *testing.T) {
	input := `{"plays":[
		{"artist":"A","track":"B","play_count":50000},
		{"artist":"C","track":"D","play_count":50001}
	]}`
	got, err := ParseNavidromeJSON(strings.NewReader(input))
	if !errors.Is(err, ErrNavidromeExpansionLimit) {
		t.Fatalf("err = %v, want ErrNavidromeExpansionLimit", err)
	}
	if got != nil {
		t.Fatalf("got = %#v, want nil", got)
	}
}

func TestParseNavidromeJSONExactExpansionLimit(t *testing.T) {
	input := `{"plays":[{"artist":"A","track":"B","play_count":100000}]}`
	got, err := ParseNavidromeJSON(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseNavidromeJSON returned error: %v", err)
	}
	if len(got) != maxNavidromeExpandedItems {
		t.Fatalf("len(got) = %d, want %d", len(got), maxNavidromeExpandedItems)
	}
}

func TestParseNavidromeJSONExactExpansionUsesOneBoundedOutputAllocation(t *testing.T) {
	input := `{"plays":[{"artist":"A","track":"B","play_count":100000}]}`
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	got, err := ParseNavidromeJSON(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseNavidromeJSON returned error: %v", err)
	}
	runtime.KeepAlive(got)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	outputBytes := uint64(unsafe.Sizeof(sdk.ForeignItem{})) * maxNavidromeExpandedItems
	ceiling := outputBytes + outputBytes/4 + 2<<20
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > ceiling {
		t.Fatalf("allocated %d bytes, ceiling %d (exact output %d); possible intermediate output slice", allocated, ceiling, outputBytes)
	}
}

func TestNavidromeSourceCategorySelection(t *testing.T) {
	path := writeNavidromeFixture(t)
	tests := []struct {
		name        string
		selected    []string
		wantCats    []string
		collections int
		scrobbles   int
	}{
		{
			name:        "collections only",
			selected:    []string{sdk.CategoryCollections},
			wantCats:    []string{sdk.CategoryCollections},
			collections: 1,
		},
		{
			name:      "watch state only",
			selected:  []string{sdk.CategoryWatchState},
			wantCats:  []string{sdk.CategoryWatchState},
			scrobbles: 3,
		},
		{
			name:        "both canonicalized",
			selected:    []string{sdk.CategoryWatchState, sdk.CategoryCollections},
			wantCats:    []string{sdk.CategoryCollections, sdk.CategoryWatchState},
			collections: 1,
			scrobbles:   3,
		},
		{name: "empty", selected: []string{}, wantCats: []string{}},
		{name: "nil", selected: nil, wantCats: []string{}},
		{name: "unknown", selected: []string{"unknown"}, wantCats: []string{}},
		{
			name:        "duplicates",
			selected:    []string{sdk.CategoryWatchState, sdk.CategoryCollections, sdk.CategoryWatchState},
			wantCats:    []string{sdk.CategoryCollections, sdk.CategoryWatchState},
			collections: 1,
			scrobbles:   3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := NewNavidromeSourceForCategories(path, tt.selected)
			if got := source.Categories(); !reflect.DeepEqual(got, tt.wantCats) {
				t.Fatalf("Categories() = %v, want %v", got, tt.wantCats)
			}
			items, err := source.Items(t.Context()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
			if err != nil {
				t.Fatalf("Items returned error: %v", err)
			}
			collections, scrobbles := navidromeItemKinds(items)
			if collections != tt.collections || scrobbles != tt.scrobbles {
				t.Fatalf("items = %d collections + %d scrobbles, want %d + %d", collections, scrobbles, tt.collections, tt.scrobbles)
			}
		})
	}
}

func TestNavidromeSourceCategoriesDefensiveCopy(t *testing.T) {
	selected := []string{
		sdk.CategoryCollections,
		sdk.CategoryWatchState,
	}
	source := NewNavidromeSourceForCategories("unused.json", selected)
	selected[0] = "mutated-input"
	first := source.Categories()
	first[0] = "mutated"
	want := []string{sdk.CategoryCollections, sdk.CategoryWatchState}
	if got := source.Categories(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Categories() after caller mutation = %v, want %v", got, want)
	}
}

func TestNavidromeEmptySelectionStillValidatesEntireExport(t *testing.T) {
	path := t.TempDir() + "/navidrome.json"
	input := `{"plays":[{"artist":"A","track":"B","play_count":100001}]}`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	items, err := NewNavidromeSourceForCategories(path, nil).Items(t.Context()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
	if !errors.Is(err, ErrNavidromeExpansionLimit) {
		t.Fatalf("err = %v, want ErrNavidromeExpansionLimit", err)
	}
	if items != nil {
		t.Fatalf("items = %#v, want nil", items)
	}
}

func TestNavidromeCompatibilityAPIsRemainEquivalent(t *testing.T) {
	path := writeNavidromeFixture(t)
	parsed, err := ParseNavidromeJSON(strings.NewReader(navidromeFixture))
	if err != nil {
		t.Fatalf("ParseNavidromeJSON returned error: %v", err)
	}
	fromSource, err := NewNavidromeSource(path).Items(t.Context()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
	if err != nil {
		t.Fatalf("NewNavidromeSource.Items returned error: %v", err)
	}
	if !reflect.DeepEqual(fromSource, parsed) {
		t.Fatalf("compatibility APIs differ:\nsource = %#v\nparse  = %#v", fromSource, parsed)
	}
}

func TestNavidromeSourceHonorsPreCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewNavidromeSource("does-not-exist.json").Items(ctx) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled before file open", err)
	}
}

func TestParseNavidromeJSONHonorsCancellationAfterDecode(t *testing.T) {
	ctx := newCancelOnCheckContext(2)
	got, err := parseNavidromeJSON(ctx, strings.NewReader(navidromeFixture), []string{
		sdk.CategoryCollections,
		sdk.CategoryWatchState,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got != nil {
		t.Fatalf("got = %#v, want nil", got)
	}
}

func TestParseNavidromeJSONHonorsCancellationDuringEmission(t *testing.T) {
	ctx := newCancelOnCheckContext(4)
	input := `{"plays":[{"artist":"A","track":"B","play_count":100000}]}`
	got, err := parseNavidromeJSON(ctx, strings.NewReader(input), []string{sdk.CategoryWatchState})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got != nil {
		t.Fatalf("got has %d items, want nil on cancellation", len(got))
	}
	if ctx.checks > 4 {
		t.Fatalf("context checked %d times, want cancellation at periodic check 4", ctx.checks)
	}
}

func TestParseNavidromeJSONCountProperty(t *testing.T) {
	cases := []struct {
		count uint64
		mode  uint8
	}{
		{count: 0, mode: 0},
		{count: 100_000, mode: 7},
		{count: 100_001, mode: 7},
		{count: ^uint64(0), mode: 7},
		{count: 1, mode: 2},
	}
	state := uint64(0x9e3779b97f4a7c15)
	for i := range 2_000 {
		state = state*6364136223846793005 + 1442695040888963407
		cases = append(cases, struct {
			count uint64
			mode  uint8
		}{count: state, mode: uint8(i)})
	}
	for _, tc := range cases {
		count, mode := tc.count, tc.mode
		var encoded string
		switch mode % 8 {
		case 0:
			encoded = fmt.Sprintf("%d", count%1001)
		case 1:
			encoded = fmt.Sprintf("-%d", count%1001+1)
		case 2:
			encoded = fmt.Sprintf("%q", fmt.Sprintf("%d", count))
		case 3:
			encoded = fmt.Sprintf("%d.5", count)
		case 4:
			encoded = fmt.Sprintf("%de1", count)
		case 5:
			encoded = "null"
		case 6:
			encoded = "true"
		case 7:
			encoded = fmt.Sprintf("%d", count)
		}
		input := fmt.Sprintf(`{"plays":[{"artist":"A","track":"B","play_count":%s}]}`, encoded)
		items, err := ParseNavidromeJSON(strings.NewReader(input))
		if err == nil {
			if len(items) > maxNavidromeExpandedItems {
				t.Fatalf("successful parse emitted %d items", len(items))
			}
			continue
		}
		if !errors.Is(err, ErrNavidromeInvalidCount) && !errors.Is(err, ErrNavidromeExpansionLimit) {
			t.Fatalf("untyped count error for %q: %v", encoded, err)
		}
	}
}

func writeNavidromeFixture(t *testing.T) string {
	t.Helper()
	path := t.TempDir() + "/navidrome.json"
	if err := os.WriteFile(path, []byte(navidromeFixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func navidromeItemKinds(items []sdk.ForeignItem) (collections, scrobbles int) {
	for _, item := range items {
		if item.SmartCollection.Present {
			collections++
		}
		if item.Scrobble.Present {
			scrobbles++
		}
	}
	return collections, scrobbles
}

type cancelOnCheckContext struct {
	cancelAt int
	checks   int
	done     chan struct{}
	once     sync.Once
}

func newCancelOnCheckContext(cancelAt int) *cancelOnCheckContext {
	return &cancelOnCheckContext{cancelAt: cancelAt, done: make(chan struct{})}
}

func (c *cancelOnCheckContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelOnCheckContext) Done() <-chan struct{}       { return c.done }
func (c *cancelOnCheckContext) Value(any) any               { return nil }

func (c *cancelOnCheckContext) Err() error {
	c.checks++
	if c.checks < c.cancelAt {
		return nil
	}
	c.once.Do(func() { close(c.done) })
	return context.Canceled
}
