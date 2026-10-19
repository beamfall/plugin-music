package music

import (
	"context"
	"testing"
)

// fakeReader is a fake PackMediaReader backed by simple maps.
type fakeReader struct {
	entityMembers     map[string][]string
	collectionMembers map[string][]string
	// visibleSubset returns only ids present in this set.
	visible map[string]bool
}

func (f fakeReader) EntityMembers(_ context.Context, entityRef string) ([]string, error) {
	return f.entityMembers[entityRef], nil
}

func (f fakeReader) CollectionMembers(_ context.Context, collectionRef string) ([]string, error) {
	return f.collectionMembers[collectionRef], nil
}

func (f fakeReader) VisibleSubset(_ context.Context, ids []string) ([]string, error) {
	var out []string
	for _, id := range ids {
		if f.visible[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

func TestAlbumResolverMapsEntityMembersAndLabel(t *testing.T) {
	r := albumResolver{}
	rd := fakeReader{
		entityMembers: map[string][]string{
			"album-1": {"media-alpha", "media-beta"},
		},
	}
	got, err := r.Resolve(context.Background(), "album-1", rd)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Label != "Album" {
		t.Errorf("Label = %q, want Album", got.Label)
	}
	if len(got.MediaIDs) != 2 || got.MediaIDs[0] != "media-alpha" || got.MediaIDs[1] != "media-beta" {
		t.Errorf("MediaIDs = %v, want [media-alpha media-beta]", got.MediaIDs)
	}
}

func TestCollectionResolverMapsCollectionMembersAndLabel(t *testing.T) {
	r := collectionResolver{}
	rd := fakeReader{
		collectionMembers: map[string][]string{
			"coll-1": {"media-alpha"},
		},
	}
	got, err := r.Resolve(context.Background(), "coll-1", rd)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Label != "Playlist" {
		t.Errorf("Label = %q, want Playlist", got.Label)
	}
	if len(got.MediaIDs) != 1 || got.MediaIDs[0] != "media-alpha" {
		t.Errorf("MediaIDs = %v, want [media-alpha]", got.MediaIDs)
	}
}

func TestTracksResolverKeepsVisibleSubsetAndLabel(t *testing.T) {
	r := tracksResolver{}
	rd := fakeReader{
		visible: map[string]bool{"media-alpha": true},
	}
	got, err := r.Resolve(context.Background(), "media-alpha, media-archive", rd)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Label != "Tracks" {
		t.Errorf("Label = %q, want Tracks", got.Label)
	}
	if len(got.MediaIDs) != 1 || got.MediaIDs[0] != "media-alpha" {
		t.Errorf("MediaIDs = %v, want [media-alpha]", got.MediaIDs)
	}
}

func TestTracksResolverDeduplicatesInput(t *testing.T) {
	r := tracksResolver{}
	// Track what ids reach VisibleSubset via a recording reader.
	var got []string
	rec := recordingReader{
		onVisibleSubset: func(ids []string) []string {
			got = append([]string(nil), ids...)
			return ids
		},
	}
	if _, err := r.Resolve(context.Background(), "media-alpha, media-alpha, media-beta", rec); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// parsePackRefs must have deduplicated; only 2 unique ids should reach VisibleSubset.
	if len(got) != 2 {
		t.Errorf("ids passed to VisibleSubset = %v, want 2 unique ids (dedup)", got)
	}
}

func TestStationResolverStubReturnsErrStationGeneratorUnavailable(t *testing.T) {
	r := stationResolver{source: StubStationSnapshotSource{}}
	_, err := r.Resolve(context.Background(), "station-1", fakeReader{})
	if err != ErrStationGeneratorUnavailable {
		t.Errorf("err = %v, want ErrStationGeneratorUnavailable", err)
	}
}

// recordingReader is a PackMediaReader that captures VisibleSubset calls.
type recordingReader struct {
	onVisibleSubset func(ids []string) []string
}

func (r recordingReader) EntityMembers(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (r recordingReader) CollectionMembers(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (r recordingReader) VisibleSubset(_ context.Context, ids []string) ([]string, error) {
	if r.onVisibleSubset != nil {
		return r.onVisibleSubset(ids), nil
	}
	return ids, nil
}
