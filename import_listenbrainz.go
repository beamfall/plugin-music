package music

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	sdk "github.com/beamfall/plugin-sdk"
)

// errNotListenBrainzJSON is returned when the trimmed payload starts with
// neither '[' nor '{' — the input is not a JSON array, object, or JSONL
// export (e.g. an HTML error page saved with a .json extension). Without this
// check the parse silently falls through with a nil listens slice, returning
// an empty item list and a nil error — the operator sees "0 imported" instead
// of a diagnosable "wrong file" signal.
var errNotListenBrainzJSON = errors.New("listenbrainz import: input is not a JSON array, object, or JSONL export")

// recordingMBIDScheme is the media_external_id scheme recording MBIDs are keyed
// under. It must equal music.SchemeMusicBrainzRecording (scrobble.go)
// and the scheme Core's music hydration writes,
// since migrate.matchByExternalIDs compares the scheme exactly — an imported
// scrobble only matches a hydrated Media when both use this spelling.
// SchemeMusicBrainzRecording is defined in this package (scrobble.go), so it is
// referenced directly.
const recordingMBIDScheme = SchemeMusicBrainzRecording

const ListenBrainzImporterID = "listenbrainz"

type listenBrainzSource struct {
	jsonPath string
}

func (listenBrainzSource) Categories() []string { return []string{sdk.CategoryWatchState} }

func (s listenBrainzSource) Items(ctx context.Context) ([]sdk.ForeignItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(s.jsonPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ParseListenBrainzJSON(f)
}

func (s listenBrainzSource) Stream(
	ctx context.Context,
	checkpoint string,
	emit func(context.Context, []sdk.ForeignItem, string) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	pager, err := newMusicPageEmitter(ctx, checkpoint, emit)
	if err != nil {
		return err
	}
	kind, values, err := inspectListenBrainzFile(ctx, s.jsonPath)
	if err != nil {
		return err
	}
	if values == 0 {
		return pager.finish()
	}
	switch kind {
	case listenBrainzArray:
		err = streamListenBrainzArrayFile(ctx, s.jsonPath, pager)
	case listenBrainzObject:
		err = streamListenBrainzObjectFile(ctx, s.jsonPath, pager)
	case listenBrainzJSONLines:
		err = streamListenBrainzJSONLinesFile(ctx, s.jsonPath, pager)
	default:
		err = errNotListenBrainzJSON
	}
	if err != nil {
		return err
	}
	return pager.finish()
}

func NewListenBrainzSource(jsonPath string) sdk.Source { //nolint:staticcheck // deliberate SDK 0.x compatibility path for pre-streaming consumers
	return listenBrainzSource{jsonPath: jsonPath}
}

type listenBrainzFileKind uint8

const (
	listenBrainzInvalid listenBrainzFileKind = iota
	listenBrainzArray
	listenBrainzObject
	listenBrainzJSONLines
)

func inspectListenBrainzFile(ctx context.Context, path string) (listenBrainzFileKind, int, error) {
	f, err := openCappedMusicImport(path)
	if err != nil {
		return listenBrainzInvalid, 0, err
	}
	defer func() { _ = f.Close() }()
	decoder := json.NewDecoder(io.LimitReader(f, maxImportBytes+1))
	values := 0
	firstKind := listenBrainzInvalid
	for {
		if err := ctx.Err(); err != nil {
			return listenBrainzInvalid, 0, err
		}
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return listenBrainzInvalid, 0, err
		}
		delim, ok := token.(json.Delim)
		if !ok || (delim != '[' && delim != '{') {
			return listenBrainzInvalid, 0, errNotListenBrainzJSON
		}
		if values == 0 {
			if delim == '[' {
				firstKind = listenBrainzArray
			} else {
				firstKind = listenBrainzObject
			}
		}
		if err := skipJSONContainer(decoder, delim); err != nil {
			return listenBrainzInvalid, 0, err
		}
		values++
	}
	if values > 1 {
		if firstKind != listenBrainzObject {
			return listenBrainzInvalid, 0, errNotListenBrainzJSON
		}
		return listenBrainzJSONLines, values, nil
	}
	return firstKind, values, nil
}

func streamListenBrainzArrayFile(ctx context.Context, path string, pager *musicPageEmitter) error {
	f, err := openCappedMusicImport(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	decoder := json.NewDecoder(io.LimitReader(f, maxImportBytes+1))
	return streamListenBrainzArray(ctx, decoder, pager)
}

func streamListenBrainzObjectFile(ctx context.Context, path string, pager *musicPageEmitter) error {
	direct, payload, err := inspectListenBrainzObject(ctx, path)
	if err != nil {
		return err
	}
	selection := "single"
	if direct > 0 {
		selection = "direct"
	} else if payload > 0 {
		selection = "payload"
	}
	f, err := openCappedMusicImport(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	decoder := json.NewDecoder(io.LimitReader(f, maxImportBytes+1))
	if selection == "single" {
		var listen listenBrainzListen
		if err := decoder.Decode(&listen); err != nil {
			return err
		}
		return addListenBrainzItem(pager, listen)
	}
	return streamSelectedListenBrainzObject(ctx, decoder, selection, pager)
}

func inspectListenBrainzObject(ctx context.Context, path string) (int, int, error) {
	f, err := openCappedMusicImport(path)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = f.Close() }()
	decoder := json.NewDecoder(io.LimitReader(f, maxImportBytes+1))
	start, err := decoder.Token()
	if err != nil {
		return 0, 0, err
	}
	if start != json.Delim('{') {
		return 0, 0, errNotListenBrainzJSON
	}
	direct, payload := 0, 0
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		key, err := decoder.Token()
		if err != nil {
			return 0, 0, err
		}
		switch key {
		case "listens":
			direct, err = countJSONArray(decoder)
		case "payload":
			payload, err = countListenBrainzPayload(decoder)
		default:
			err = skipJSONValue(decoder)
		}
		if err != nil {
			return 0, 0, err
		}
	}
	_, err = decoder.Token()
	return direct, payload, err
}

func countListenBrainzPayload(decoder *json.Decoder) (int, error) {
	start, err := decoder.Token()
	if err != nil {
		return 0, err
	}
	if start == nil {
		return 0, nil
	}
	if start != json.Delim('{') {
		return 0, errors.New("listenbrainz import: payload is not an object")
	}
	count := 0
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return 0, err
		}
		if key == "listens" {
			count, err = countJSONArray(decoder)
		} else {
			err = skipJSONValue(decoder)
		}
		if err != nil {
			return 0, err
		}
	}
	_, err = decoder.Token()
	return count, err
}

func streamSelectedListenBrainzObject(
	ctx context.Context,
	decoder *json.Decoder,
	selection string,
	pager *musicPageEmitter,
) error {
	start, err := decoder.Token()
	if err != nil {
		return err
	}
	if start != json.Delim('{') {
		return errNotListenBrainzJSON
	}
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		if key == "listens" && selection == "direct" {
			err = streamListenBrainzArray(ctx, decoder, pager)
		} else if key == "payload" && selection == "payload" {
			err = streamListenBrainzPayload(ctx, decoder, pager)
		} else {
			err = skipJSONValue(decoder)
		}
		if err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func streamListenBrainzPayload(ctx context.Context, decoder *json.Decoder, pager *musicPageEmitter) error {
	start, err := decoder.Token()
	if err != nil {
		return err
	}
	if start == nil {
		return nil
	}
	if start != json.Delim('{') {
		return errors.New("listenbrainz import: payload is not an object")
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		if key == "listens" {
			err = streamListenBrainzArray(ctx, decoder, pager)
		} else {
			err = skipJSONValue(decoder)
		}
		if err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func streamListenBrainzArray(ctx context.Context, decoder *json.Decoder, pager *musicPageEmitter) error {
	start, err := decoder.Token()
	if err != nil {
		return err
	}
	if start == nil {
		return nil
	}
	if start != json.Delim('[') {
		return errors.New("listenbrainz import: listens is not an array")
	}
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var listen listenBrainzListen
		if err := decoder.Decode(&listen); err != nil {
			return err
		}
		if err := addListenBrainzItem(pager, listen); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func addListenBrainzItem(pager *musicPageEmitter, listen listenBrainzListen) error {
	item := listen.toForeignItem()
	if !item.Scrobble.Present && len(item.ExternalIDs) == 0 {
		return nil
	}
	return pager.add(item)
}

func streamListenBrainzJSONLinesFile(ctx context.Context, path string, pager *musicPageEmitter) error {
	f, err := openCappedMusicImport(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(io.LimitReader(f, maxImportBytes+1))
	scanner.Buffer(make([]byte, 64<<10), maxListenBrainzLineBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		listens, err := parseListenBrainzObject(line)
		if err != nil {
			return err
		}
		for _, listen := range listens {
			if err := addListenBrainzItem(pager, listen); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func openCappedMusicImport(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.Size() > maxImportBytes {
		_ = f.Close()
		return nil, errImportTooLarge
	}
	return f, nil
}

func countJSONArray(decoder *json.Decoder) (int, error) {
	start, err := decoder.Token()
	if err != nil {
		return 0, err
	}
	if start == nil {
		return 0, nil
	}
	if start != json.Delim('[') {
		return 0, errors.New("import: expected JSON array")
	}
	count := 0
	for decoder.More() {
		if err := skipJSONValue(decoder); err != nil {
			return 0, err
		}
		count++
	}
	_, err = decoder.Token()
	return count, err
}

func skipJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	return skipJSONContainer(decoder, token)
}

func skipJSONContainer(decoder *json.Decoder, token json.Token) error {
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		for decoder.More() {
			if _, err := decoder.Token(); err != nil {
				return err
			}
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := skipJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("import: unexpected JSON delimiter")
	}
	_, err := decoder.Token()
	return err
}

// ParseListenBrainzJSON reads a ListenBrainz export (a JSON array, a single
// `{"listens": [...]}` / `{"payload": {"listens": [...]}}` object, or
// newline-delimited JSON objects of either shape) and returns one
// sdk.ForeignItem per listen. A payload that starts with neither '[' nor '{'
// (e.g. an HTML error page saved as .json) returns errNotListenBrainzJSON
// rather than a silent empty result.
//
// The input is capped at maxImportBytes to protect the in-process core from
// unbounded memory growth on a malicious or corrupted export.
func ParseListenBrainzJSON(r io.Reader) ([]sdk.ForeignItem, error) {
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
	var listens []listenBrainzListen
	switch raw[0] {
	case '[':
		if err := json.Unmarshal(raw, &listens); err != nil {
			return nil, err
		}
	case '{':
		// A leading '{' is ambiguous between a single wrapper object
		// (`{"listens": [...]}` / `{"payload": {"listens": [...]}}`) and
		// newline-delimited JSON objects, which also start with '{'. Try the
		// wrapper-object parse first; only fall back to JSONL if that parse
		// fails and the input contains a newline (a lone object can never be
		// valid JSONL, so the newline check avoids masking a genuine
		// single-object parse error with a confusing JSONL one).
		listens, err = parseListenBrainzObject(raw)
		if err != nil && bytes.Contains(raw, []byte("\n")) {
			listens, err = parseListenBrainzJSONLines(raw)
		}
		if err != nil {
			return nil, err
		}
	default:
		return nil, errNotListenBrainzJSON
	}
	items := make([]sdk.ForeignItem, 0, len(listens))
	for _, listen := range listens {
		item := listen.toForeignItem()
		if !item.Scrobble.Present && len(item.ExternalIDs) == 0 {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

// maxListenBrainzLineBytes bounds a single JSONL line. bufio.Scanner's default
// token limit is 64 KB (bufio.MaxScanTokenSize), which a single oversized
// listen line can legitimately exceed; scanner.Buffer raises that ceiling to a
// generous 2 MB so a normal line never trips it, while an oversized line still
// fails cleanly via bufio.ErrTooLong instead of silently truncating.
const maxListenBrainzLineBytes = 2 << 20

func parseListenBrainzJSONLines(raw []byte) ([]listenBrainzListen, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 64<<10), maxListenBrainzLineBytes)
	var listens []listenBrainzListen
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		one, err := parseListenBrainzObject(line)
		if err != nil {
			return nil, err
		}
		listens = append(listens, one...)
	}
	return listens, scanner.Err()
}

func parseListenBrainzObject(raw []byte) ([]listenBrainzListen, error) {
	var wrapper struct {
		Listens []listenBrainzListen `json:"listens"`
		Payload struct {
			Listens []listenBrainzListen `json:"listens"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, err
	}
	if len(wrapper.Listens) > 0 {
		return wrapper.Listens, nil
	}
	if len(wrapper.Payload.Listens) > 0 {
		return wrapper.Payload.Listens, nil
	}
	var single listenBrainzListen
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, err
	}
	return []listenBrainzListen{single}, nil
}

type listenBrainzListen struct {
	ListenedAt    any `json:"listened_at"`
	TrackMetadata struct {
		ArtistName     string         `json:"artist_name"`
		TrackName      string         `json:"track_name"`
		ReleaseName    string         `json:"release_name"`
		AdditionalInfo map[string]any `json:"additional_info"`
	} `json:"track_metadata"`
	ArtistName     string         `json:"artist_name"`
	TrackName      string         `json:"track_name"`
	ReleaseName    string         `json:"release_name"`
	AdditionalInfo map[string]any `json:"additional_info"`
}

func (l listenBrainzListen) toForeignItem() sdk.ForeignItem {
	artist := firstNonEmpty(l.TrackMetadata.ArtistName, l.ArtistName)
	track := firstNonEmpty(l.TrackMetadata.TrackName, l.TrackName)
	album := firstNonEmpty(l.TrackMetadata.ReleaseName, l.ReleaseName)
	info := l.TrackMetadata.AdditionalInfo
	if len(info) == 0 {
		info = l.AdditionalInfo
	}
	item := sdk.ForeignItem{
		Scrobble: sdk.ScrobbleRecord{
			Present:  strings.TrimSpace(track) != "" || strings.TrimSpace(artist) != "",
			Artist:   strings.TrimSpace(artist),
			Track:    strings.TrimSpace(track),
			Album:    strings.TrimSpace(album),
			PlayedAt: parseListenBrainzTime(l.ListenedAt),
		},
	}
	if mbid := firstRecordingMBID(info); mbid != "" {
		item.ExternalIDs = []sdk.ExternalID{{Scheme: recordingMBIDScheme, Value: mbid}}
	}
	return item
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstRecordingMBID(info map[string]any) string {
	if value, ok := info["recording_mbid"]; ok {
		if s := strings.TrimSpace(stringValue(value)); s != "" {
			return s
		}
	}
	if value, ok := info["recording_mbids"]; ok {
		switch v := value.(type) {
		case []any:
			for _, one := range v {
				if s := strings.TrimSpace(stringValue(one)); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func parseListenBrainzTime(value any) time.Time {
	switch v := value.(type) {
	case float64:
		return time.Unix(int64(v), 0).UTC()
	case string:
		if secs, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return time.Unix(secs, 0).UTC()
		}
		return parseScrobbleTime(v)
	default:
		return time.Time{}
	}
}
