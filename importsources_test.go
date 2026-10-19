package music

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	sdk "github.com/beamfall/plugin-sdk"
	"github.com/beamfall/plugin-sdk/sdktest"
)

func TestImporterStreamingCancellationAndCheckpoint(t *testing.T) {
	dir := t.TempDir()
	lastfm := writeImportFixture(t, dir, "lastfm.csv", "1700000000,Artist,Album,Track\n")
	listenbrainz := writeImportFixture(t, dir, "listenbrainz.json", `{"payload":{"listens":[{"listened_at":1700000000,"track_metadata":{"artist_name":"Artist","track_name":"Track"}}]}}`)
	navidrome := writeImportFixture(t, dir, "navidrome.json", `{"playlists":[{"name":"Mix","tracks":[{"artist":"Artist","title":"Track"}]}],"plays":[{"artist":"Artist","track":"Track","play_count":1}]}`)
	track := writeImportFixture(t, dir, "Track.mp3", "")
	m3u := writeImportFixture(t, dir, "Mix.m3u", track+"\n")

	sources := []struct {
		name       string
		source     sdk.Source //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
		categories []string
	}{
		{LastfmImporterID, NewLastfmSource(lastfm), []string{sdk.CategoryWatchState}},
		{ListenBrainzImporterID, NewListenBrainzSource(listenbrainz), []string{sdk.CategoryWatchState}},
		{NavidromeImporterID, NewNavidromeSource(navidrome), []string{sdk.CategoryCollections, sdk.CategoryWatchState}},
		{LocalM3UImporterID, NewLocalM3USource(m3u), []string{sdk.CategoryCollections}},
	}
	vectors := make([]sdktest.ImporterVector, 0, len(sources))
	for _, source := range sources {
		items, err := source.source.Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
		if err != nil {
			t.Fatalf("%s fixture: %v", source.name, err)
		}
		streamed, err := collectMusicStream(source.source)
		if err != nil {
			t.Fatalf("%s stream: %v", source.name, err)
		}
		if !reflect.DeepEqual(streamed, items) {
			t.Fatalf("%s streamed items differ from compatibility Items:\nstream: %#v\nitems:  %#v", source.name, streamed, items)
		}
		vectors = append(vectors, sdktest.ImporterVector{
			Name:           source.name,
			Source:         source.source,
			WantCategories: source.categories,
			WantItems:      items,
		})
	}
	sdktest.RunImporterConformance(t, vectors)
}

func collectMusicStream(source sdk.Source) ([]sdk.ForeignItem, error) { //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
	streaming := source.(interface {
		Stream(context.Context, string, func(context.Context, []sdk.ForeignItem, string) error) error
	})
	items := []sdk.ForeignItem{}
	err := streaming.Stream(context.Background(), "", func(
		_ context.Context,
		page []sdk.ForeignItem,
		_ string,
	) error {
		items = append(items, page...)
		return nil
	})
	return items, err
}

func TestJSONImporterStreamsMatchCompatibilityForNullCollections(t *testing.T) {
	dir := t.TempDir()
	sources := []sdk.Source{ //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
		NewListenBrainzSource(writeImportFixture(t, dir, "listenbrainz-null.json", `{"listens":null,"payload":null}`)),
		NewListenBrainzSource(writeImportFixture(t, dir, "listenbrainz.jsonl", "{\"artist_name\":\"A\",\"track_name\":\"One\"}\n{\"artist_name\":\"A\",\"track_name\":\"Two\"}\n")),
		NewNavidromeSource(writeImportFixture(t, dir, "navidrome-null.json", `{"playlists":null,"plays":null}`)),
	}
	for _, source := range sources {
		items, err := source.Items(context.Background()) //nolint:staticcheck // deliberate SDK 0.x compatibility assertion
		if err != nil {
			t.Fatalf("compatibility Items: %v", err)
		}
		streamed, err := collectMusicStream(source)
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		if !reflect.DeepEqual(streamed, items) {
			t.Fatalf("streamed items differ from compatibility Items: stream=%#v items=%#v", streamed, items)
		}
	}
}

func writeImportFixture(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}
