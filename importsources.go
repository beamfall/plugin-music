// Music compiled migration sources:
// implements the sdk.Source seam for music sources the plugin can import
// behavioral state from — Last.fm scrobble CSV and ListenBrainz JSON/JSONL
// exports. The importer hooks are the Music plugin's own code, so they live
// with the bundle rather than in core.
//
// A scrobble is a metadata-keyed play event (artist + track + album +
// timestamp) with no file path, so this maps each row to an sdk.ForeignItem
// carrying an sdk.ScrobbleRecord (not a Path). Core's migrate.Handler resolves each
// scrobble to a Media by metadata and applies a play through the play-count/
// last-played projection. Source-system names ("lastfm", "listenbrainz") live
// here and in the bundle's ImporterSpec, never in core.
package music

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	sdk "github.com/beamfall/plugin-sdk"
)

// PluginID matches Bundle()'s `ID: "music"` and the migrate import-provenance
// discriminator core writes ("import: music"). The app's
// migrationSourceResolver keys (pluginID, importerID) onto the constructors
// below using this id.
const PluginID = "music"

const musicImportPageItems = 256

// LastfmImporterID is the music plugin's Last.fm importer slug — the (PluginID,
// LastfmImporterID) the wizard enqueues and the resolver dispatches here.
const LastfmImporterID = "lastfm"

// scrobbleSource adapts a Last.fm scrobble CSV export to sdk.Source. It carries
// play history (counts + last-played), declared under the watch-state
// engagement category (engagement events). The operator
// supplies the exported CSV file path (export-file input).
type scrobbleSource struct {
	csvPath string
}

// Categories: a scrobble import carries engagement (play history), declared
// under the watch-state category — the fixed ImportCategories member for
// "engagement events". The handler routes a Scrobble-bearing
// item through the metadata match + play-count apply rather than the resume
// path, so a music run never writes a spurious resume position.
func (scrobbleSource) Categories() []string { return []string{sdk.CategoryWatchState} }

func (s scrobbleSource) Items(ctx context.Context) ([]sdk.ForeignItem, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(s.csvPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	scrobbles, err := ParseLastfmCSV(f)
	if err != nil {
		return nil, err
	}
	items := make([]sdk.ForeignItem, 0, len(scrobbles))
	for _, sc := range scrobbles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		items = append(items, sdk.ForeignItem{Scrobble: sc})
	}
	return items, nil
}

func (s scrobbleSource) Stream(
	ctx context.Context,
	checkpoint string,
	emit func(context.Context, []sdk.ForeignItem, string) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(s.csvPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > maxImportBytes {
		return errImportTooLarge
	}

	pager, err := newMusicPageEmitter(ctx, checkpoint, emit)
	if err != nil {
		return err
	}
	reader := csv.NewReader(io.LimitReader(f, maxImportBytes+1))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	first, err := reader.Read()
	if err == io.EOF {
		return pager.finish()
	}
	if err != nil {
		return err
	}
	columns, header := lastfmColumns(first)
	if !header {
		item := lastfmForeignItem(first, columns)
		if item.Scrobble.Present {
			if err := pager.add(item); err != nil {
				return err
			}
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		row, err := reader.Read()
		if err == io.EOF {
			return pager.finish()
		}
		if err != nil {
			return err
		}
		item := lastfmForeignItem(row, columns)
		if !item.Scrobble.Present {
			continue
		}
		if err := pager.add(item); err != nil {
			return err
		}
	}
}

func lastfmForeignItem(row []string, columns columnMap) sdk.ForeignItem {
	artist := strings.TrimSpace(field(row, columns.artist))
	track := strings.TrimSpace(field(row, columns.track))
	if artist == "" && track == "" {
		return sdk.ForeignItem{}
	}
	return sdk.ForeignItem{Scrobble: sdk.ScrobbleRecord{
		Present:  true,
		Artist:   artist,
		Track:    track,
		Album:    strings.TrimSpace(field(row, columns.album)),
		PlayedAt: parseScrobbleTime(strings.TrimSpace(field(row, columns.timestamp))),
	}}
}

type musicPageEmitter struct {
	ctx    context.Context
	emit   func(context.Context, []sdk.ForeignItem, string) error
	resume int
	seen   int
	page   []sdk.ForeignItem
}

func newMusicPageEmitter(
	ctx context.Context,
	checkpoint string,
	emit func(context.Context, []sdk.ForeignItem, string) error,
) (*musicPageEmitter, error) {
	resume, err := musicImportCheckpoint(checkpoint)
	if err != nil {
		return nil, err
	}
	return &musicPageEmitter{
		ctx:    ctx,
		emit:   emit,
		resume: resume,
		page:   make([]sdk.ForeignItem, 0, musicImportPageItems),
	}, nil
}

func (p *musicPageEmitter) add(item sdk.ForeignItem) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	p.seen++
	if p.seen <= p.resume {
		return nil
	}
	p.page = append(p.page, item)
	if len(p.page) < musicImportPageItems {
		return nil
	}
	return p.flush()
}

func (p *musicPageEmitter) finish() error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if p.seen < p.resume {
		return fmt.Errorf("music import checkpoint %d is beyond the source", p.resume)
	}
	return p.flush()
}

func (p *musicPageEmitter) flush() error {
	if len(p.page) == 0 {
		return nil
	}
	if err := p.emit(p.ctx, p.page, strconv.Itoa(p.seen)); err != nil {
		return err
	}
	if err := p.ctx.Err(); err != nil {
		return err
	}
	p.page = make([]sdk.ForeignItem, 0, musicImportPageItems)
	return nil
}

func musicImportCheckpoint(checkpoint string) (int, error) {
	if checkpoint == "" {
		return 0, nil
	}
	index, err := strconv.Atoi(checkpoint)
	if err != nil || index < 0 {
		return 0, fmt.Errorf("invalid music import checkpoint %q", checkpoint)
	}
	return index, nil
}

// NewLastfmSource returns the Last.fm scrobble migration Source over an exported
// scrobble CSV at csvPath. The resolver maps (music, "lastfm") here.
func NewLastfmSource(csvPath string) sdk.Source { //nolint:staticcheck // deliberate SDK 0.x compatibility path for pre-streaming consumers
	return scrobbleSource{csvPath: csvPath}
}
