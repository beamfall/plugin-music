package music

import (
	"context"
	"errors"
	"strings"
)

// Pack target kinds for the Music plugin. These are the opaque kind strings
// registered into an offlinepack.Service via a core adapter.
const (
	PackKindAlbum      = "music-album"
	PackKindCollection = "music-collection"
	PackKindTracks     = "music-tracks"
	PackKindStation    = "music-station"
)

// ErrStationGeneratorUnavailable is returned by StubStationSnapshotSource until
// the play-queue generator lands. The core adapter propagates it; BuildPack
// creates no pack row.
var ErrStationGeneratorUnavailable = errors.New("music: station generator unavailable")

// PackResolved is the output of a PackResolver: the ordered visible Media IDs
// that belong in the pack plus a manifest-safe display label. All IDs are plain
// strings; the core adapter translates them to store.MediaID.
type PackResolved struct {
	MediaIDs []string
	Label    string
}

// PackMediaReader is the filter-bound store view the resolvers read. The core
// adapter constructs one per Resolve call, binding the caller's LibraryFilter
// so the resolvers never see the filter directly.
type PackMediaReader interface {
	// EntityMembers returns the ordered visible Media IDs for an entity ref.
	EntityMembers(ctx context.Context, entityRef string) ([]string, error)
	// CollectionMembers returns the ordered visible Media IDs for a collection ref.
	CollectionMembers(ctx context.Context, collectionRef string) ([]string, error)
	// VisibleSubset returns the subset of ids that are visible, preserving order.
	VisibleSubset(ctx context.Context, ids []string) ([]string, error)
}

// PackResolver translates an opaque target ref into the visible Media that
// belong in the pack. Implemented by the four unexported resolver types below.
type PackResolver interface {
	Resolve(ctx context.Context, targetRef string, r PackMediaReader) (PackResolved, error)
}

// StationSnapshotSource runs the Station play-queue generator once and returns
// the selected batch. A Station pack runs the generator once and stores the
// selected batch as the pack manifest source; it does not regenerate while
// offline. The returned ids are visibility-filtered downstream
// by the resolver, so the source may return the raw generated batch.
type StationSnapshotSource interface {
	Snapshot(ctx context.Context, stationRef string) ([]string, error)
}

// StubStationSnapshotSource is the no-generator stub. It always errors, so a
// Station pack-build attempt fails cleanly and leaves no half-built pack.
type StubStationSnapshotSource struct{}

// Snapshot always returns ErrStationGeneratorUnavailable.
func (StubStationSnapshotSource) Snapshot(_ context.Context, _ string) ([]string, error) {
	return nil, ErrStationGeneratorUnavailable
}

// albumResolver resolves an Album Entity to its ordered visible member Media.
type albumResolver struct{}

func (r albumResolver) Resolve(ctx context.Context, targetRef string, rd PackMediaReader) (PackResolved, error) {
	ids, err := rd.EntityMembers(ctx, targetRef)
	if err != nil {
		return PackResolved{}, err
	}
	return PackResolved{MediaIDs: ids, Label: "Album"}, nil
}

// collectionResolver resolves a static Collection/Playlist to its ordered
// visible member Media.
type collectionResolver struct{}

func (r collectionResolver) Resolve(ctx context.Context, targetRef string, rd PackMediaReader) (PackResolved, error) {
	ids, err := rd.CollectionMembers(ctx, targetRef)
	if err != nil {
		return PackResolved{}, err
	}
	return PackResolved{MediaIDs: ids, Label: "Playlist"}, nil
}

// tracksResolver resolves an explicit comma/space-separated Media id list to the
// visible subset, preserving order. Duplicate ids are deduplicated by
// parsePackRefs (mirror of core offlinepack.ParseMediaRefs).
type tracksResolver struct{}

func (r tracksResolver) Resolve(ctx context.Context, targetRef string, rd PackMediaReader) (PackResolved, error) {
	ids := parsePackRefs(targetRef)
	vis, err := rd.VisibleSubset(ctx, ids)
	if err != nil {
		return PackResolved{}, err
	}
	return PackResolved{MediaIDs: vis, Label: "Tracks"}, nil
}

// stationResolver resolves a Station to a frozen snapshot: it runs the generator
// once, then visibility-filters the batch.
type stationResolver struct {
	source StationSnapshotSource
}

func (r stationResolver) Resolve(ctx context.Context, targetRef string, rd PackMediaReader) (PackResolved, error) {
	batch, err := r.source.Snapshot(ctx, targetRef)
	if err != nil {
		return PackResolved{}, err
	}
	vis, err := rd.VisibleSubset(ctx, batch)
	if err != nil {
		return PackResolved{}, err
	}
	return PackResolved{MediaIDs: vis, Label: "Station"}, nil
}

// parsePackRefs splits a comma/space/newline/tab-separated list of Media ids
// into a deduplicated, order-preserving slice. Mirrors core
// offlinepack.ParseMediaRefs exactly (including the seen-map dedup) so repeated
// track ids do not produce duplicate OfflinePackItem rows.
func parsePackRefs(ref string) []string {
	fields := strings.FieldsFunc(ref, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		id := strings.TrimSpace(f)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// PackResolvers returns a map of target kind → PackResolver for all four Music
// pack target kinds. If station is nil, StubStationSnapshotSource is used.
func PackResolvers(station StationSnapshotSource) map[string]PackResolver {
	if station == nil {
		station = StubStationSnapshotSource{}
	}
	return map[string]PackResolver{
		PackKindAlbum:      albumResolver{},
		PackKindCollection: collectionResolver{},
		PackKindTracks:     tracksResolver{},
		PackKindStation:    stationResolver{source: station},
	}
}
