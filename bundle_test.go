package music

import (
	"testing"

	sdk "github.com/beamfall/plugin-sdk"
	"github.com/beamfall/plugin-sdk/scrobble"
)

// TestBundleImporters asserts the Bundle's dispatch keys (plugin ID and
// importer IDs/categories) match the exported constants rather than
// reintroducing raw string literals.
func TestBundleImporters(t *testing.T) {
	b := Bundle()

	if b.ID != PluginID {
		t.Errorf("Bundle().ID = %q, want PluginID %q", b.ID, PluginID)
	}

	if len(b.Importers) != 4 {
		t.Fatalf("len(Bundle().Importers) = %d, want 4", len(b.Importers))
	}

	lastfm := b.Importers[0]
	if lastfm.ID != LastfmImporterID {
		t.Errorf("Importers[0].ID = %q, want LastfmImporterID %q", lastfm.ID, LastfmImporterID)
	}
	if len(lastfm.Categories) != 1 || lastfm.Categories[0] != sdk.CategoryWatchState {
		t.Errorf("Importers[0].Categories = %v, want [%q]", lastfm.Categories, sdk.CategoryWatchState)
	}

	listenbrainz := b.Importers[1]
	if listenbrainz.ID != ListenBrainzImporterID {
		t.Errorf("Importers[1].ID = %q, want ListenBrainzImporterID %q", listenbrainz.ID, ListenBrainzImporterID)
	}
	if len(listenbrainz.Categories) != 1 || listenbrainz.Categories[0] != sdk.CategoryWatchState {
		t.Errorf("Importers[1].Categories = %v, want [%q]", listenbrainz.Categories, sdk.CategoryWatchState)
	}

	navidrome := b.Importers[2]
	if navidrome.ID != NavidromeImporterID {
		t.Errorf("Importers[2].ID = %q, want NavidromeImporterID %q", navidrome.ID, NavidromeImporterID)
	}
	wantCats := []string{sdk.CategoryCollections, sdk.CategoryWatchState}
	if len(navidrome.Categories) != len(wantCats) || navidrome.Categories[0] != wantCats[0] || navidrome.Categories[1] != wantCats[1] {
		t.Errorf("Importers[2].Categories = %v, want %v", navidrome.Categories, wantCats)
	}

	localM3U := b.Importers[3]
	if localM3U.ID != LocalM3UImporterID {
		t.Errorf("Importers[3].ID = %q, want LocalM3UImporterID %q", localM3U.ID, LocalM3UImporterID)
	}
	if len(localM3U.Categories) != 1 || localM3U.Categories[0] != sdk.CategoryCollections {
		t.Errorf("Importers[3].Categories = %v, want [%q]", localM3U.Categories, sdk.CategoryCollections)
	}
}

// TestBundleScrobbleMatch pins the ScrobbleMatch relation slugs core/migrate
// reads to confirm an imported play's artist/album: a
// rename of the `appears-on` or `album-track` relation in EntityKinds without
// updating ScrobbleMatch would silently break scrobble-import matching, since
// core hardcodes no music relation name and trusts these slugs verbatim.
func TestBundleScrobbleMatch(t *testing.T) {
	b := Bundle()

	if b.ScrobbleMatch == nil {
		t.Fatal("Bundle().ScrobbleMatch is nil, want a populated ScrobbleMatchSpec")
	}
	if got := b.ScrobbleMatch.ArtistConfirm; len(got) != 1 || got[0] != "appears-on" {
		t.Errorf("ScrobbleMatch.ArtistConfirm = %v, want [appears-on]", got)
	}
	if got := b.ScrobbleMatch.AlbumTieBreak; len(got) != 1 || got[0] != "album-track" {
		t.Errorf("ScrobbleMatch.AlbumTieBreak = %v, want [album-track]", got)
	}
}

// TestBundleScrobbleTarget pins Music's target declaration to the SDK's
// bounded standalone shape. Core consumes this generic data and supplies no
// implicit Music fallback.
func TestBundleScrobbleTarget(t *testing.T) {
	b := Bundle()
	if b.ScrobbleTarget == nil {
		t.Fatal("Bundle().ScrobbleTarget is nil, want standalone default")
	}
	if got := b.ScrobbleTarget.Default; got != scrobble.TargetStandalone {
		t.Errorf("ScrobbleTarget.Default = %q, want %q", got, scrobble.TargetStandalone)
	}
	if b.ScrobbleTarget.MediaField != "" || len(b.ScrobbleTarget.Mappings) != 0 {
		t.Errorf("ScrobbleTarget = %+v, want default-only standalone declaration", *b.ScrobbleTarget)
	}

	// The SDK owns the closed target vocabulary. A future/unknown value must
	// fail bundle validation instead of becoming an implicit Core fallback.
	b.ScrobbleTarget.Default = scrobble.TargetHint("future")
	if err := sdk.ValidateBundle(b); err == nil {
		t.Fatal("ValidateBundle accepted unknown ScrobbleTarget default")
	}
}

// TestCoverArtArchiveScraperSpec pins the Cover Art Archive scraper's
// ArtworkMap paths (poster/thumb) and asserts the MusicBrainz scraper's
// MinRequestIntervalMS stays at or above 1000ms — MusicBrainz throttles
// anonymous clients to ~1 request/second, so a regression below
// that floor would silently trip their rate limiter.
func TestCoverArtArchiveScraperSpec(t *testing.T) {
	b := Bundle()

	var caa, mb *sdk.ScraperSpec
	for i := range b.Scrapers {
		switch b.Scrapers[i].ID {
		case "cover-art-archive":
			caa = &b.Scrapers[i]
		case "musicbrainz":
			mb = &b.Scrapers[i]
		}
	}
	if caa == nil {
		t.Fatal("cover-art-archive scraper not found in bundle")
	}
	if got := caa.ArtworkMap["poster"]; got != "images.0.image" {
		t.Errorf("cover-art-archive ArtworkMap[poster] = %q, want images.0.image", got)
	}
	if got := caa.ArtworkMap["thumb"]; got != "images.0.thumbnails.500" {
		t.Errorf("cover-art-archive ArtworkMap[thumb] = %q, want images.0.thumbnails.500", got)
	}

	if mb == nil {
		t.Fatal("musicbrainz scraper not found in bundle")
	}
	if mb.MinRequestIntervalMS < 1000 {
		t.Errorf("musicbrainz MinRequestIntervalMS = %d, want >= 1000 (etiquette floor)", mb.MinRequestIntervalMS)
	}
}

func TestBundleDeclaresHydrationMarkerAndFingerprint(t *testing.T) {
	types := map[string]string{}
	for _, field := range Bundle().MediaFields {
		types[field.Name] = field.Type
	}
	if got := types[HydratedField]; got != "flag" {
		t.Fatalf("%s declared as %q, want flag", HydratedField, got)
	}
	if got := types[HydratedFingerprintField]; got != "text" {
		t.Fatalf("%s declared as %q, want text", HydratedFingerprintField, got)
	}
}

func TestBundleDeclaresExtendedHydrationFields(t *testing.T) {
	types := map[string]string{}
	for _, field := range Bundle().MediaFields {
		types[field.Name] = field.Type
	}
	want := map[string]string{
		"composer": "text", "compilation": "flag", "artist-sort": "text",
		"album-sort": "text", "album-artist-sort": "text", "lyrics": "text",
		"bpm": "number", "rating": "number", "comment": "text",
		"musicbrainz-release-group-id": "text",
	}
	for name, fieldType := range want {
		if types[name] != fieldType {
			t.Errorf("field %q type = %q, want %q", name, types[name], fieldType)
		}
	}
}

// wantBundleVersion is the release semver this head carries: the release step
// tags the head `v`+wantBundleVersion, so a bundle Version left behind by a
// tag bump fails here instead of shipping a stale identity.
const wantBundleVersion = "0.1.5"

// TestBundleVersionMatchesReleaseTag pins Bundle().Version to the semver of
// the release tag applied to this head.
func TestBundleVersionMatchesReleaseTag(t *testing.T) {
	if got := Bundle().Version; got != wantBundleVersion {
		t.Fatalf("Bundle().Version = %q, want %q", got, wantBundleVersion)
	}
}

// TestBundleCompatibleWithPinnedSDK pins the declared [SDKFloor, SDKCeiling]
// range against the SDK identity this module pins, so a bundle whose range
// excludes its own pinned SDK fails here rather than at core load time
// (sdk.CompatibleWithSDK).
func TestBundleCompatibleWithPinnedSDK(t *testing.T) {
	b := Bundle()
	if !sdk.CompatibleWithSDK(b, sdk.Version) {
		t.Fatalf("Bundle() compat range [%s, %s] excludes pinned SDK %s", b.SDKFloor, b.SDKCeiling, sdk.Version)
	}
}
