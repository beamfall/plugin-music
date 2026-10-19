package music

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	sdk "github.com/beamfall/plugin-sdk"
)

// NavidromeImporterID is the music plugin's Navidrome importer slug — the
// (PluginID, NavidromeImporterID) the wizard enqueues and the resolver
// dispatches here.
const NavidromeImporterID = "navidrome"

const (
	maxNavidromeExpandedItems = 100_000
	navidromeCancelInterval   = 256
)

// ErrNavidromeInvalidCount identifies a play_count that is present but is not
// a canonical non-negative base-10 integer.
var ErrNavidromeInvalidCount = errors.New("navidrome import: invalid play_count")

// ErrNavidromeExpansionLimit identifies an export whose declared playlists
// and plays would expand beyond the fixed per-source item limit.
var ErrNavidromeExpansionLimit = errors.New("navidrome import: expansion exceeds 100,000 items")

// errNotNavidromeJSON is returned when the trimmed payload does not start with
// '{' — the input is not the top-level export object (e.g. an HTML error page
// saved with a .json extension). Without this check the parse silently falls
// through to an empty item list instead of a diagnosable "wrong file" signal
// (the same guard ParseListenBrainzJSON applies).
var errNotNavidromeJSON = errors.New("navidrome import: input is not a JSON export object")

type navidromeSource struct {
	jsonPath   string
	categories []string
}

// Categories returns the canonical supported intersection selected for this
// run. The returned slice cannot mutate the source.
func (s navidromeSource) Categories() []string {
	categories := make([]string, len(s.categories))
	copy(categories, s.categories)
	return categories
}

func (s navidromeSource) Items(ctx context.Context) ([]sdk.ForeignItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(s.jsonPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return parseNavidromeJSON(ctx, f, s.categories)
}

func (s navidromeSource) Stream(
	ctx context.Context,
	checkpoint string,
	emit func(context.Context, []sdk.ForeignItem, string) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateNavidromeStream(ctx, s.jsonPath); err != nil {
		return err
	}
	pager, err := newMusicPageEmitter(ctx, checkpoint, emit)
	if err != nil {
		return err
	}
	if categorySelected(s.categories, sdk.CategoryCollections) {
		if err := streamNavidromePlaylists(ctx, s.jsonPath, pager); err != nil {
			return err
		}
	}
	if categorySelected(s.categories, sdk.CategoryWatchState) {
		if err := streamNavidromePlays(ctx, s.jsonPath, pager); err != nil {
			return err
		}
	}
	return pager.finish()
}

// NewNavidromeSource returns the Navidrome migration Source over an exported
// JSON file at jsonPath with both supported categories selected. The resolver
// maps (music, "navidrome") here.
func NewNavidromeSource(jsonPath string) sdk.Source { //nolint:staticcheck // deliberate SDK 0.x compatibility path for pre-streaming consumers
	return NewNavidromeSourceForCategories(jsonPath, []string{
		sdk.CategoryCollections,
		sdk.CategoryWatchState,
	})
}

// NewNavidromeSourceForCategories returns a source restricted to the canonical
// intersection of Navidrome's supported categories and selected. An empty,
// nil, or unknown-only selection is intentionally fail-closed.
func NewNavidromeSourceForCategories(jsonPath string, selected []string) sdk.Source { //nolint:staticcheck // deliberate SDK 0.x compatibility path for pre-streaming consumers
	return navidromeSource{
		jsonPath:   jsonPath,
		categories: navidromeSelectedCategories(selected),
	}
}

func navidromeSelectedCategories(selected []string) []string {
	wanted := make(map[string]bool, len(selected))
	for _, category := range selected {
		wanted[category] = true
	}
	categories := make([]string, 0, 2)
	for _, category := range []string{sdk.CategoryCollections, sdk.CategoryWatchState} {
		if wanted[category] {
			categories = append(categories, category)
		}
	}
	return categories
}

// navidromeExport is the top-level shape of a Navidrome export: the
// operator's playlists (name + ordered track list) and per-track play
// history (play count + last played), matching Navidrome's own data model
// (playlist/playlist_tracks and media_file.play_count/play_date).
type navidromeExport struct {
	Playlists []navidromePlaylist `json:"playlists"`
	Plays     []navidromePlay     `json:"plays"`
}

type navidromePlaylist struct {
	Name               string              `json:"name"`
	Tracks             []navidromeTrackRef `json:"tracks"`
	streamTrackCount   int
	streamCountPresent bool
}

type navidromeTrackRef struct {
	Artist string `json:"artist"`
	Title  string `json:"title"`
	Album  string `json:"album"`
}

type navidromePlay struct {
	Artist     string             `json:"artist"`
	Track      string             `json:"track"`
	Album      string             `json:"album"`
	PlayCount  navidromePlayCount `json:"play_count"`
	LastPlayed string             `json:"last_played"`
	MBID       string             `json:"mbid"`
}

type navidromePlayCount struct {
	value   int
	present bool
}

func validateNavidromeStream(ctx context.Context, path string) error {
	total := 0
	return visitNavidromeExport(
		ctx,
		path,
		func(navidromePlaylist) error {
			total++
			if total > maxNavidromeExpandedItems {
				return ErrNavidromeExpansionLimit
			}
			return nil
		},
		func(play navidromePlay) error {
			if !play.PlayCount.present {
				return nil
			}
			if play.PlayCount.value > maxNavidromeExpandedItems-total {
				return ErrNavidromeExpansionLimit
			}
			total += play.PlayCount.value
			return nil
		},
	)
}

func streamNavidromePlaylists(ctx context.Context, path string, pager *musicPageEmitter) error {
	return visitNavidromeExport(ctx, path, func(playlist navidromePlaylist) error {
		return pager.add(playlist.toForeignItem())
	}, nil)
}

func streamNavidromePlays(ctx context.Context, path string, pager *musicPageEmitter) error {
	return visitNavidromeExport(ctx, path, nil, func(play navidromePlay) error {
		item, ok := play.toForeignItem()
		if !ok {
			return nil
		}
		for range play.PlayCount.value {
			if err := pager.add(item); err != nil {
				return err
			}
		}
		return nil
	})
}

func visitNavidromeExport(
	ctx context.Context,
	path string,
	onPlaylist func(navidromePlaylist) error,
	onPlay func(navidromePlay) error,
) error {
	f, err := openCappedMusicImport(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	decoder := json.NewDecoder(io.LimitReader(f, maxImportBytes+1))
	start, err := decoder.Token()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	if start != json.Delim('{') {
		return errNotNavidromeJSON
	}
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		switch key {
		case "playlists":
			err = visitNavidromePlaylists(ctx, decoder, onPlaylist)
		case "plays":
			err = visitNavidromePlays(ctx, decoder, onPlay)
		default:
			err = skipJSONValue(decoder)
		}
		if err != nil {
			return err
		}
	}
	if _, err = decoder.Token(); err != nil {
		return err
	}
	if _, err = decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("navidrome import: trailing JSON value")
		}
		return err
	}
	return nil
}

func visitNavidromePlaylists(
	ctx context.Context,
	decoder *json.Decoder,
	visit func(navidromePlaylist) error,
) error {
	start, err := decoder.Token()
	if err != nil {
		return err
	}
	if start == nil {
		return nil
	}
	if start != json.Delim('[') {
		return errors.New("navidrome import: playlists is not an array")
	}
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		playlist, err := decodeNavidromePlaylist(decoder)
		if err != nil {
			return err
		}
		if visit != nil {
			if err := visit(playlist); err != nil {
				return err
			}
		}
	}
	_, err = decoder.Token()
	return err
}

func decodeNavidromePlaylist(decoder *json.Decoder) (navidromePlaylist, error) {
	start, err := decoder.Token()
	if err != nil {
		return navidromePlaylist{}, err
	}
	if start == nil {
		return navidromePlaylist{}, nil
	}
	if start != json.Delim('{') {
		return navidromePlaylist{}, errors.New("navidrome import: playlist is not an object")
	}
	playlist := navidromePlaylist{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return navidromePlaylist{}, err
		}
		switch key {
		case "name":
			err = decoder.Decode(&playlist.Name)
		case "tracks":
			var count int
			count, err = countJSONArray(decoder)
			if err == nil {
				playlist.streamTrackCount = count
				playlist.streamCountPresent = true
			}
		default:
			err = skipJSONValue(decoder)
		}
		if err != nil {
			return navidromePlaylist{}, err
		}
	}
	_, err = decoder.Token()
	return playlist, err
}

func visitNavidromePlays(
	ctx context.Context,
	decoder *json.Decoder,
	visit func(navidromePlay) error,
) error {
	start, err := decoder.Token()
	if err != nil {
		return err
	}
	if start == nil {
		return nil
	}
	if start != json.Delim('[') {
		return errors.New("navidrome import: plays is not an array")
	}
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var play navidromePlay
		if err := decoder.Decode(&play); err != nil {
			return err
		}
		if visit != nil {
			if err := visit(play); err != nil {
				return err
			}
		}
	}
	_, err = decoder.Token()
	return err
}

func (c *navidromePlayCount) UnmarshalJSON(raw []byte) error {
	c.present = true
	if len(raw) == 0 {
		return ErrNavidromeInvalidCount
	}
	var count uint64
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return ErrNavidromeInvalidCount
		}
		value := uint64(digit - '0')
		if count > (^uint64(0)-value)/10 {
			return ErrNavidromeInvalidCount
		}
		count = count*10 + value
	}
	maxInt := uint64(^uint(0) >> 1)
	if count > maxInt {
		return ErrNavidromeInvalidCount
	}
	if count > maxNavidromeExpandedItems {
		return ErrNavidromeExpansionLimit
	}
	c.value = int(count)
	return nil
}

// ParseNavidromeJSON reads a Navidrome export (`{"playlists": [...], "plays":
// [...]}`) and returns one sdk.ForeignItem per playlist (an
// sdk.SmartCollection static snapshot — collections membership rules are not
// yet importable) and one sdk.ForeignItem per play (an
// sdk.ScrobbleRecord) for each of a track's recorded plays, so a track with
// play_count N contributes N foreign items and the handler's play tally
// aggregates them back to N. A payload that does not start with '{' (e.g. an
// HTML error page saved as .json) returns errNotNavidromeJSON rather than a
// silent empty result.
//
// The parse is a pure function of its input (no package-level state), so
// re-running it on the same export deterministically re-yields the same
// items — the source side of "additive, idempotent re-run".
//
// The input is capped at maxImportBytes to protect the in-process core from
// unbounded memory growth on a malicious or corrupted export.
func ParseNavidromeJSON(r io.Reader) ([]sdk.ForeignItem, error) {
	return parseNavidromeJSON(context.Background(), r, []string{
		sdk.CategoryCollections,
		sdk.CategoryWatchState,
	})
}

func parseNavidromeJSON(ctx context.Context, r io.Reader, categories []string) ([]sdk.ForeignItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxImportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxImportBytes {
		return nil, errImportTooLarge
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return []sdk.ForeignItem{}, nil
	}
	if raw[0] != '{' {
		return nil, errNotNavidromeJSON
	}
	var export navidromeExport
	if err := json.Unmarshal(raw, &export); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if _, err := navidromeExpandedCount(export); err != nil {
		return nil, err
	}
	collections := categorySelected(categories, sdk.CategoryCollections)
	watchState := categorySelected(categories, sdk.CategoryWatchState)
	capacity := navidromeOutputCount(export, collections, watchState)
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	items := make([]sdk.ForeignItem, 0, capacity)
	if collections {
		for _, playlist := range export.Playlists {
			items = append(items, playlist.toForeignItem())
			if err := navidromeEmissionCanceled(ctx, len(items)); err != nil {
				return nil, err
			}
		}
	}
	if watchState {
		for _, play := range export.Plays {
			item, ok := play.toForeignItem()
			if !ok {
				continue
			}
			for range play.PlayCount.value {
				items = append(items, item)
				if err := navidromeEmissionCanceled(ctx, len(items)); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func navidromeExpandedCount(export navidromeExport) (int, error) {
	total := len(export.Playlists)
	if total > maxNavidromeExpandedItems {
		return 0, ErrNavidromeExpansionLimit
	}
	for _, play := range export.Plays {
		if !play.PlayCount.present {
			continue
		}
		count := play.PlayCount.value
		if count > maxNavidromeExpandedItems-total {
			return 0, ErrNavidromeExpansionLimit
		}
		total += count
	}
	return total, nil
}

func navidromeOutputCount(export navidromeExport, collections, watchState bool) int {
	total := 0
	if collections {
		total = len(export.Playlists)
	}
	if !watchState {
		return total
	}
	for _, play := range export.Plays {
		if !play.PlayCount.present {
			continue
		}
		if play.hasIdentity() {
			total += play.PlayCount.value
		}
	}
	return total
}

func categorySelected(categories []string, wanted string) bool {
	for _, category := range categories {
		if category == wanted {
			return true
		}
	}
	return false
}

func navidromeEmissionCanceled(ctx context.Context, emitted int) error {
	if emitted%navidromeCancelInterval != 0 {
		return nil
	}
	return ctx.Err()
}

func (p navidromePlaylist) toForeignItem() sdk.ForeignItem {
	memberCount := len(p.Tracks)
	if p.streamCountPresent {
		memberCount = p.streamTrackCount
	}
	return sdk.ForeignItem{
		SmartCollection: sdk.SmartCollection{
			Present:     true,
			Name:        strings.TrimSpace(p.Name),
			MemberCount: memberCount,
		},
	}
}

func (p navidromePlay) hasIdentity() bool {
	return strings.TrimSpace(p.Artist) != "" || strings.TrimSpace(p.Track) != ""
}

// toForeignItem builds the value appended PlayCount times. A row missing both
// artist and track contributes nothing.
func (p navidromePlay) toForeignItem() (sdk.ForeignItem, bool) {
	artist := strings.TrimSpace(p.Artist)
	track := strings.TrimSpace(p.Track)
	if artist == "" && track == "" {
		return sdk.ForeignItem{}, false
	}
	scrobble := sdk.ScrobbleRecord{
		Present:  true,
		Artist:   artist,
		Track:    track,
		Album:    strings.TrimSpace(p.Album),
		PlayedAt: parseNavidromeTime(p.LastPlayed),
	}
	var externalIDs []sdk.ExternalID
	if mbid := strings.TrimSpace(p.MBID); mbid != "" {
		externalIDs = []sdk.ExternalID{{Scheme: recordingMBIDScheme, Value: mbid}}
	}
	return sdk.ForeignItem{Scrobble: scrobble, ExternalIDs: externalIDs}, true
}

// parseNavidromeTime parses a Navidrome last-played timestamp: Navidrome's own
// export/API surfaces RFC3339 ("2024-06-06T21:30:00Z"); the plain "2006-01-02
// 15:04:05" layout is tolerated for a hand-assembled or DB-dumped export.
// Returns the zero time when empty or unparseable — the caller treats that as
// "no last-played" without dropping the play.
func parseNavidromeTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC()
	}
	if t, err := time.Parse("2006-01-02 15:04:05", raw); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
