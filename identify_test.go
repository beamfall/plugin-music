package music_test

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/beamfall/plugin-sdk"
	sdktest "github.com/beamfall/plugin-sdk/sdktest"

	music "github.com/beamfall/plugin-music"
)

// impliedClass mirrors core's identify rule (internal/identify): a scraper's
// class is ImpliedMediaFields["media.class"], else ["class"].
func impliedClass(s sdk.ScraperSpec) string {
	if c := s.ImpliedMediaFields["media.class"]; c != "" {
		return c
	}
	return s.ImpliedMediaFields["class"]
}

// searchableScraperForClass mirrors core's scraper selection: the first
// scraper with a search template and candidate ID path whose implied class
// equals the parsed class.
func searchableScraperForClass(t *testing.T, b sdk.Bundle, class string) sdk.ScraperSpec {
	t.Helper()
	for _, s := range b.Scrapers {
		if s.SearchTemplate != "" && s.CandidateIDPath != "" && impliedClass(s) == class {
			return s
		}
	}
	t.Fatalf("no searchable scraper implies class %q: core never identifies a Track", class)
	return sdk.ScraperSpec{}
}

// TestSearchableScraperImpliesParsedClass pins the CORE-2312 routing: the class
// the parser assigns is a declared MediaClass, exactly one searchable scraper
// implies it, and the bundle declares the text field the implied key writes.
func TestSearchableScraperImpliesParsedClass(t *testing.T) {
	b := music.Bundle()
	parsed, ok := music.NewParser().Parse("Radiohead/OK Computer/01 - Airbag.flac", sdk.ParseProbe{})
	if !ok {
		t.Fatal("tag-poor fixture failed to parse")
	}
	if !slices.Contains(b.MediaClasses, parsed.Class) {
		t.Fatalf("parsed class %q not in MediaClasses %v", parsed.Class, b.MediaClasses)
	}
	searchable := 0
	for _, s := range b.Scrapers {
		if s.SearchTemplate != "" && s.CandidateIDPath != "" {
			searchable++
			if got := impliedClass(s); got != parsed.Class {
				t.Errorf("searchable scraper %q implies class %q, want %q", s.ID, got, parsed.Class)
			}
		}
	}
	if searchable != 1 {
		t.Errorf("searchable scrapers = %d, want 1", searchable)
	}
	var classField *sdk.FieldSpec
	for i := range b.MediaFields {
		if b.MediaFields[i].Name == "class" {
			classField = &b.MediaFields[i]
		}
	}
	if classField == nil || classField.Type != "text" {
		t.Fatalf("bundle must declare a text Media field %q for media.class, got %#v", "class", classField)
	}
}

// TestIdentifyTaggedTrackSearchesRecordings runs a tagged Track through the
// plugin side of core's identify path offline: parse → scraper selection →
// compiled search request → candidate scoring → compiled lookup fields. It
// proves identification searches recordings (not releases) and that the
// tagged recording is the unique auto-apply winner (CORE-2312).
func TestIdentifyTaggedTrackSearchesRecordings(t *testing.T) {
	parsed, ok := music.NewParser().Parse("Radiohead/OK Computer/01 - Airbag.flac", sdk.ParseProbe{
		HasAudio: true,
		Tags: map[string]string{
			"title": "Airbag", "artist": "Radiohead", "album": "OK Computer",
			"date": "1997-05-21", "track": "1",
		},
	})
	if !ok || parsed.Title != "Airbag" || parsed.Year != 1997 || parsed.Class != "audio" {
		t.Fatalf("Parse = (%#v, %v), want title Airbag, year 1997, class audio", parsed, ok)
	}
	spec := searchableScraperForClass(t, music.Bundle(), parsed.Class)

	// Recorded shape of MusicBrainz /ws/2/recording?query=…&fmt=json with
	// synthetic IDs: the tagged recording, a later live recording and a
	// same-named recording by another artist from a different year.
	searchDoc := map[string]any{"recordings": []any{
		map[string]any{"id": "rec-airbag-1997", "title": "Airbag", "first-release-date": "1997-05-21"},
		map[string]any{"id": "rec-airbag-live", "title": "Airbag (live)", "first-release-date": "2008-11-03",
			"disambiguation": "live"},
		map[string]any{"id": "rec-airbag-other", "title": "Airbag", "first-release-date": "2015"},
	}}
	lookupDoc := map[string]any{"id": "rec-airbag-1997", "title": "Airbag", "first-release-date": "1997-05-21",
		"releases": []any{map[string]any{"id": "rel-ok-computer", "title": "OK Computer"}},
		"artist-credit": []any{map[string]any{"name": "Radiohead",
			"artist": map[string]any{"id": "artist-radiohead", "name": "Radiohead", "sort-name": "Radiohead"}}}}

	trace := sdktest.CompileScraperTrace(spec, sdktest.TraceInput{
		Query: parsed.Title, Hints: parsed.Hints, ID: "rec-airbag-1997", SearchDoc: searchDoc, LookupDoc: lookupDoc,
	})
	urls := map[string]string{}
	for _, r := range trace.Requests {
		urls[r.Role] = r.URL
	}
	// The tagged artist and album narrow the title search (CORE-2333).
	if got, want := urls["search"], "https://musicbrainz.org/ws/2/recording?query=(Airbag)+AND+artist%3A%22Radiohead%22+AND+release%3A%22OK+Computer%22&fmt=json"; got != want {
		t.Errorf("search URL = %q, want %q (all requests: %v)", got, want, urls)
	}
	if got, want := urls["lookup"], "https://musicbrainz.org/ws/2/recording/rec-airbag-1997?fmt=json&inc=artist-credits+releases+genres"; got != want {
		t.Errorf("lookup URL = %q, want %q", got, want)
	}

	scorer := music.NewScorer()
	best, bestScore, tied := "", -1.0, false
	for _, c := range trace.Candidates {
		year, _ := strconv.Atoi(c.Fields["year"])
		score := scorer.Score(sdk.Candidate{ID: c.ID, Title: c.Fields["title"], Year: year}, parsed)
		switch {
		case score > bestScore:
			best, bestScore, tied = c.ID, score, false
		case score == bestScore:
			tied = true
		}
	}
	if len(trace.Candidates) != 3 {
		t.Fatalf("candidates = %d, want 3: %#v", len(trace.Candidates), trace.Candidates)
	}
	if best != "rec-airbag-1997" || tied || bestScore < music.AutoApplyThreshold {
		t.Fatalf("best = %q score %.2f tied=%v, want unique rec-airbag-1997 >= %.2f",
			best, bestScore, tied, music.AutoApplyThreshold)
	}
	if trace.Fields["title"] != "Airbag" || trace.Fields["year"] != "1997" {
		t.Errorf("lookup fields = %v, want title Airbag, year 1997", trace.Fields)
	}
	// Tracks take Cover Art Archive art from the recording's first release.
	// Core's JSON resolver indexes arrays ("releases.0.id"); sdktest v0.4.0's
	// trace resolver walks objects only, so the slot is pinned on the spec and
	// rendered here the way core's renderArtworkURL does (PathEscape into {value}).
	releaseID := lookupDoc["releases"].([]any)[0].(map[string]any)["id"].(string)
	for slot, want := range map[string]string{
		"poster": "https://coverartarchive.org/release/rel-ok-computer/front",
		"thumb":  "https://coverartarchive.org/release/rel-ok-computer/front-500",
	} {
		if got := spec.ArtworkMap[slot]; got != "releases.0.id" {
			t.Errorf("ArtworkMap[%s] = %q, want releases.0.id", slot, got)
		}
		got := strings.ReplaceAll(spec.ArtworkURLTemplates[slot], "{value}", url.PathEscape(releaseID))
		if got != want {
			t.Errorf("%s artwork = %q, want %q", slot, got, want)
		}
	}
}

// TestHomeLayoutBrowsesAlbumsAndArtists pins the CORE-2327/CORE-1147 rows:
// Albums and Artists shelves on the home Layout, a detail Layout per kind that
// lists its Tracks, and no video-only Source.
func TestHomeLayoutBrowsesAlbumsAndArtists(t *testing.T) {
	b := music.Bundle()
	shelves := map[string]bool{}
	details := map[string]bool{}
	for _, l := range b.Layouts {
		for _, s := range l.Sections {
			if s.Source.Kind == "continue-watching" {
				t.Errorf("layout %q section %q uses video-only continue-watching", l.ID, s.ID)
			}
			if l.ID == "home" && s.Source.Kind == "entity-items" {
				shelves[s.Source.EntityKind] = true
			}
			if l.Role == "detail" && s.Source.Kind == "entity-media" {
				details[l.EntityKind] = true
			}
		}
	}
	for _, kind := range []string{"album", "artist"} {
		if !shelves[kind] {
			t.Errorf("home layout has no entity-items shelf for %q", kind)
		}
		if !details[kind] {
			t.Errorf("no detail layout lists %q Tracks via entity-media", kind)
		}
	}
	sdktest.RequireBundleValid(t, b)
}
