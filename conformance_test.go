// Package music_test pins the Music plugin bundle against the SDK wire
// contract. Any bundle violation — bad ID slug,
// non-semver Version, missing "en" locale anchor, or MediaClass outside the
// allowed set — surfaces here as a test failure rather than a runtime panic.
//
// The plugin also implements sdk.FilenameParser and sdk.MatchScorer: the parse- and score-conformance vectors below pin the
// tag-poor-rip filename heuristics and the match-confidence model against the
// SDK scan contract, alongside the focused Last.fm / ListenBrainz import-parser
// tests in the main package.
package music_test

import (
	"testing"

	sdk "github.com/beamfall/plugin-sdk"
	sdktest "github.com/beamfall/plugin-sdk/sdktest"

	music "github.com/beamfall/plugin-music"
)

func TestBundleConformance(t *testing.T) {
	sdktest.RunBundleConformance(t, music.Bundle())
	sdktest.RequireEntityKindVocab(t, music.Bundle(), []sdktest.KindVocab{
		{Name: "artist", Archetype: "person"},
		{Name: "album", Archetype: "container"},
		{Name: "genre", Archetype: "category"},
	})
}

// TestParseConformance pins the Music filename parser against the SDK scan
// contract. It exercises the tag-poor-rip fallback (track-ordinal
// prefixes, "NN - Artist - Title" convention, folded-in album year, classical
// work/movement text, non-Latin titles) and the tag-authoritative path.
func TestParseConformance(t *testing.T) {
	sdktest.RunConformance(t, music.NewParser(), []sdktest.ParseVector{
		{
			Name:   "tag-poor rip: track prefix + artist-title",
			Path:   "05 - Miles Davis - So What.flac",
			Want:   sdk.ParsedMedia{Title: "So What", Class: "audio", Index: 5},
			WantOK: true,
		},
		{
			Name:   "tag-poor rip: bare track prefix, title only",
			Path:   "01. Blue in Green.mp3",
			Want:   sdk.ParsedMedia{Title: "Blue in Green", Class: "audio", Index: 1},
			WantOK: true,
		},
		{
			Name:   "disc-track prefix",
			Path:   "1-05 Flamenco Sketches.flac",
			Want:   sdk.ParsedMedia{Title: "Flamenco Sketches", Class: "audio", Sequence: "1", Index: 5},
			WantOK: true,
		},
		{
			Name: "classical work/movement as title text",
			Path: "02 - Beethoven - Symphony No. 5 - I. Allegro con brio.flac",
			// Dots normalise to spaces (rip separator convention), so "I." → "I";
			// the last " - " segment is the movement title.
			Want:   sdk.ParsedMedia{Title: "I Allegro con brio", Class: "audio", Index: 2},
			WantOK: true,
		},
		{
			Name:   "folded-in album year bounds the title",
			Path:   "03 - Kind of Blue (1959).flac",
			Want:   sdk.ParsedMedia{Title: "Kind of Blue", Class: "audio", Index: 3, Year: 1959},
			WantOK: true,
		},
		{
			Name:   "non-Latin title, no ordinal",
			Path:   "夜に駆ける.mp3",
			Want:   sdk.ParsedMedia{Title: "夜に駆ける", Class: "audio"},
			WantOK: true,
		},
		{
			Name: "tag TITLE is authoritative over the filename",
			Path: "05 - So What.flac",
			Probe: sdk.ParseProbe{HasAudio: true, Tags: map[string]string{
				"title": "So What", "track": "5", "disc": "1", "date": "1959",
			}},
			Want:   sdk.ParsedMedia{Title: "So What", Class: "audio", Index: 5, Sequence: "1", Year: 1959},
			WantOK: true,
		},
		{
			Name:   "no recoverable title",
			Path:   "07.flac",
			Want:   sdk.ParsedMedia{},
			WantOK: false,
		},
	})
}

// TestScoreConformance pins the Music match scorer's confidence model:
// title similarity dominates, year corroborates.
func TestScoreConformance(t *testing.T) {
	soWhat := sdk.ParsedMedia{Title: "So What", Year: 1959}
	sdktest.RunScoreConformance(t, music.NewScorer(), []sdktest.ScoreVector{
		{Name: "exact title + year", CandTitle: "So What", CandYear: 1959, Parsed: soWhat, Want: 1.0},
		{Name: "exact title, +/-1 year", CandTitle: "So What", CandYear: 1960, Parsed: soWhat, Want: 0.85},
		{Name: "exact title, conflicting year", CandTitle: "So What", CandYear: 1970, Parsed: soWhat, Want: 0.7},
		{Name: "title miss is zero", CandTitle: "Blue in Green", CandYear: 1959, Parsed: soWhat, Want: 0.0},
		{Name: "no parsed year: title carries", CandTitle: "So What", CandYear: 1959,
			Parsed: sdk.ParsedMedia{Title: "So What"}, Want: 1.0},
		{Name: "containment (no year) is docked below auto-apply", CandTitle: "So What (Remastered)", CandYear: 0,
			Parsed: sdk.ParsedMedia{Title: "So What"}, Want: 0.60},
		{Name: "non-Latin exact title", CandTitle: "夜に駆ける", CandYear: 0,
			Parsed: sdk.ParsedMedia{Title: "夜に駆ける"}, Want: 1.0},
	})
}

// TestMusicBrainzScraperSpec pins the MusicBrainz scraper's EntityMaps and
// SearchFieldMap structure. These declarations drive entity
// hydration and search-candidate field mapping at identification time; a
// regression in their shape would silently no-op artist extraction.
func TestMusicBrainzScraperSpec(t *testing.T) {
	b := music.Bundle()
	var mb *struct {
		EntityMaps     int
		SearchFieldMap map[string]string
	}
	for _, s := range b.Scrapers {
		if s.ID != "musicbrainz" {
			continue
		}
		mb = &struct {
			EntityMaps     int
			SearchFieldMap map[string]string
		}{
			EntityMaps:     len(s.EntityMaps),
			SearchFieldMap: s.SearchFieldMap,
		}
		// --- EntityMaps: artist hydration ---
		if len(s.EntityMaps) == 0 {
			t.Error("musicbrainz scraper: EntityMaps is empty; artist hydration will not run")
		}
		foundArtist := false
		for _, em := range s.EntityMaps {
			if em.Kind != "artist" {
				continue
			}
			foundArtist = true
			if em.Relation != "appears-on" {
				t.Errorf("artist EntityMap: Relation = %q, want appears-on", em.Relation)
			}
			if em.Path != "artist-credit" {
				t.Errorf("artist EntityMap: Path = %q, want artist-credit", em.Path)
			}
			if em.NamePath != "artist.name" {
				t.Errorf("artist EntityMap: NamePath = %q, want artist.name", em.NamePath)
			}
			if em.FieldMap["musicbrainz-id"] != "artist.id" {
				t.Errorf("artist EntityMap: FieldMap[musicbrainz-id] = %q, want artist.id", em.FieldMap["musicbrainz-id"])
			}
			if em.FieldMap["sort-name"] != "artist.sort-name" {
				t.Errorf("artist EntityMap: FieldMap[sort-name] = %q, want artist.sort-name", em.FieldMap["sort-name"])
			}
		}
		if !foundArtist {
			t.Error("musicbrainz scraper: no EntityMap with Kind=artist")
		}
		// --- SearchFieldMap: explicit search-candidate field mapping ---
		if len(s.SearchFieldMap) == 0 {
			t.Error("musicbrainz scraper: SearchFieldMap is empty; search-candidate mapping falls through to FieldMap only")
		}
		for _, key := range []string{"title", "year", "summary"} {
			if s.SearchFieldMap[key] == "" {
				t.Errorf("musicbrainz scraper SearchFieldMap: missing key %q", key)
			}
		}
		break
	}
	_ = mb
	if mb == nil {
		t.Fatal("musicbrainz scraper not found in bundle")
	}
}
