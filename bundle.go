// Package music is the first-party Music Domain Plugin: a mostly
// declarative bundle — Entity Kinds, a home Layout, and enrichment scrapers —
// against the SDK's fixed vocabularies, plus the compiled
// migration-source importer hooks (Last.fm CSV / ListenBrainz JSON)
// that implement the sdk.Source seam. The engagement machinery it leans on
// (audio media class, no-resume, play counts, recently-played / most-played
// Source kinds) already lives in Core; this bundle only uses it.
package music

import (
	sdk "github.com/beamfall/plugin-sdk"
	"github.com/beamfall/plugin-sdk/scrobble"
)

// Bundle returns the Music plugin's declarative contribution.
//
// Entity model: a Track is Media
// (display label only) — title, album, track number, disc number, release
// date/year, and MusicBrainz recording/release IDs are Media fields, not a
// Kind. Artist is person-archetype, Album is an ordered container (children
// play in (disc, track) order — play-next semantics), Genre is a flat category.
//
// Relation model retargeted to Media (the hydration contract): track-level performance credits are the Artist `appears-on`
// relation pointed at the Track Media with a credited-as attribute; ordered
// Album membership is the one-cardinality `album-track` relation from Album to
// the Track Media (sort order = (disc, track)); genre classification is the
// `classifies` relation from Genre to the Track Media. `album-artist` stays a
// kind-to-kind Album→Artist relation (from the ALBUMARTIST tag).
func Bundle() sdk.Bundle {
	return sdk.Bundle{
		ID:          PluginID,
		Version:     "0.1.5",
		SDKFloor:    "0.2.0",
		SDKCeiling:  "0.3.2",
		DisplayName: sdk.LocaleString{"en": "Music", "fr": "Musique"},
		// A Track is Media — display label only, never a schema
		// identifier.
		MediaLabel:   sdk.LocaleString{"en": "Track", "fr": "Piste"},
		MediaClasses: []string{"audio"},
		FeatureEligibility: []string{
			sdk.FeatureAudioStation,
		},
		// Track-level Media fields first-scope hydration writes. The plugin
		// declares every Media field hydration writes. `title` is the core built-in, so it needs no declaration
		// here; the rest are this plugin's data, never core vocabulary. Album
		// is the per-track album title (the Album entity carries its own
		// release-date/MBID for enrichment; these are the Track's copies from
		// embedded tags).
		MediaFields: []sdk.FieldSpec{
			{Name: "album", Type: "text", Searchable: true,
				Label: sdk.LocaleString{"en": "Album", "fr": "Album"}},
			{Name: "composer", Type: "text", Searchable: true,
				Label: sdk.LocaleString{"en": "Composer", "fr": "Compositeur"}},
			{Name: "compilation", Type: "flag", Filterable: true,
				Label: sdk.LocaleString{"en": "Compilation", "fr": "Compilation"}},
			{Name: "artist-sort", Type: "text",
				Label: sdk.LocaleString{"en": "Artist Sort", "fr": "Tri de l’artiste"}},
			{Name: "album-sort", Type: "text",
				Label: sdk.LocaleString{"en": "Album Sort", "fr": "Tri de l’album"}},
			{Name: "album-artist-sort", Type: "text",
				Label: sdk.LocaleString{"en": "Album Artist Sort", "fr": "Tri de l’artiste de l’album"}},
			{Name: "lyrics", Type: "text",
				Label: sdk.LocaleString{"en": "Lyrics", "fr": "Paroles"}},
			{Name: "bpm", Type: "number", Sortable: true,
				Label: sdk.LocaleString{"en": "BPM", "fr": "BPM"}},
			{Name: "rating", Type: "number", Sortable: true,
				Label: sdk.LocaleString{"en": "Rating", "fr": "Note"}},
			{Name: "comment", Type: "text",
				Label: sdk.LocaleString{"en": "Comment", "fr": "Commentaire"}},
			{Name: "track-number", Type: "number", Sortable: true,
				Label: sdk.LocaleString{"en": "Track Number", "fr": "Numéro de piste"}},
			{Name: "disc-number", Type: "number", Sortable: true,
				Label: sdk.LocaleString{"en": "Disc Number", "fr": "Numéro de disque"}},
			{Name: "release-year", Type: "number", Sortable: true,
				Label: sdk.LocaleString{"en": "Year", "fr": "Année"}},
			{Name: "release-date", Type: "date", Sortable: true,
				Label: sdk.LocaleString{"en": "Release Date", "fr": "Date de sortie"}},
			{Name: "musicbrainz-recording-id", Type: "text",
				Label: sdk.LocaleString{"en": "MusicBrainz Recording ID", "fr": "Identifiant d'enregistrement MusicBrainz"}},
			{Name: "musicbrainz-release-id", Type: "text",
				Label: sdk.LocaleString{"en": "MusicBrainz Release ID", "fr": "Identifiant de parution MusicBrainz"}},
			{Name: "musicbrainz-release-group-id", Type: "text",
				Label: sdk.LocaleString{"en": "MusicBrainz Release Group ID", "fr": "Identifiant de groupe de parution MusicBrainz"}},
			// Hydration-version marker: set absent-only when a
			// track's tags are hydrated, so a future backfill pass can find
			// stable files imported before hydration shipped (the scan fast path
			// skips re-probing, so unchanged files are not re-hydrated). Declared
			// so the bundle-validated write path persists it rather than dropping
			// it as an unknown field. The name is HydratedField (same package),
			// the single source of truth for the marker the hydration seam writes
			// and backfill queries.
			{Name: HydratedField, Type: "flag",
				Label: sdk.LocaleString{"en": "Hydrated", "fr": "Hydraté"}},
			// The content fingerprint hydration was keyed on (HydratedFingerprintField,
			// same package). A rescan compares it with the file's current fingerprint
			// through DecideHydrationWrite: moved → re-hydrate tag-sourced values
			// under field locks; same → no write. Declared so the write path
			// persists it rather than dropping it as an unknown field.
			{Name: HydratedFingerprintField, Type: "text",
				Label: sdk.LocaleString{"en": "Hydrated From", "fr": "Hydraté depuis"}},
		},
		EntityKinds: []sdk.EntityKind{
			{
				Name:        "artist",
				DisplayName: sdk.LocaleString{"en": "Artist", "fr": "Artiste"},
				Archetype:   "person",
				Fields: []sdk.FieldSpec{
					{Name: "sort-name", Type: "text",
						Label: sdk.LocaleString{"en": "Sort Name", "fr": "Nom de tri"}},
					// Embedded MusicBrainz IDs (Picard) arrive as Parse
					// Hints; stored here so enrichment
					// lookups can key on them.
					{Name: "musicbrainz-id", Type: "text",
						Label: sdk.LocaleString{"en": "MusicBrainz ID", "fr": "Identifiant MusicBrainz"}},
				},
				Relations: []sdk.RelationSpec{
					// Track-level performance credits, retargeted to the Track
					// Media. Multi-valued
					// track-artist tags become multiple relations, taken
					// literally — no "feat." string-splitting.
					{Name: "appears-on", ToKind: sdk.MediaKindName, Attributes: []string{"credited-as"}},
				},
			},
			{
				Name:        "album",
				DisplayName: sdk.LocaleString{"en": "Album", "fr": "Album"},
				Archetype:   "container",
				// Album children carry a defined play order — (disc,
				// track) — so play-next walks the album.
				// Disc number is a sort field, not structure: no Disc
				// entity, no Parts.
				OrderedChildren: true,
				Fields: []sdk.FieldSpec{
					{Name: "sort-name", Type: "text",
						Label: sdk.LocaleString{"en": "Sort Name", "fr": "Nom de tri"}},
					{Name: "release-date", Type: "date",
						Label: sdk.LocaleString{"en": "Release Date", "fr": "Date de sortie"}},
					{Name: "musicbrainz-release-id", Type: "text",
						Label: sdk.LocaleString{"en": "MusicBrainz Release ID", "fr": "Identifiant de parution MusicBrainz"}},
					{Name: "musicbrainz-release-group-id", Type: "text",
						Label: sdk.LocaleString{"en": "MusicBrainz Release Group ID", "fr": "Identifiant de groupe de parution MusicBrainz"}},
					{Name: "compilation", Type: "flag",
						Label: sdk.LocaleString{"en": "Compilation", "fr": "Compilation"}},
				},
				Relations: []sdk.RelationSpec{
					// Ordered Album membership, retargeted to the Track Media
					// Media. One-cardinality: a
					// Track belongs to at most one Album for this relation; the
					// relation row's sort order is (disc, track). This is the
					// Album→Track edge the play queue walks.
					{Name: "album-track", ToKind: sdk.MediaKindName, Cardinality: "one"},
					// From ALBUMARTIST or the compilation/path fallback.
					// "Various Artists" remains an ordinary Artist entity.
					{Name: "album-artist", ToKind: "artist"},
				},
			},
			{
				// Flat — hierarchical genres are a taxonomy tar pit.
				Name:        "genre",
				DisplayName: sdk.LocaleString{"en": "Genre", "fr": "Genre"},
				Archetype:   "category",
				Relations: []sdk.RelationSpec{
					// Genre classification, retargeted to the Track Media
					// Media. A Genre classifies many Tracks; a Track
					// may carry several genres.
					{Name: "classifies", ToKind: sdk.MediaKindName},
				},
			},
		},
		// Home Layout: leads with the engagement-backed
		// Sources built for this plugin. Deliberately no continue-watching
		// Section — audio Media never resumes.
		// Section titles ship the full locale-map form: en + fr at
		// launch, with more locales to follow.
		Layouts: []sdk.LayoutSpec{{ID: "home", Role: "browse", Sections: []sdk.SectionSpec{
			{ID: "recently-played", Kind: "shelf",
				Title:       sdk.LocaleString{"en": "Recently Played", "fr": "Écoutés récemment"},
				ArtworkSlot: "poster", Source: sdk.SourceSpec{Kind: "recently-played"}},
			{ID: "most-played", Kind: "shelf",
				Title:       sdk.LocaleString{"en": "Most Played", "fr": "Les plus écoutés"},
				ArtworkSlot: "poster", Source: sdk.SourceSpec{Kind: "most-played"}},
			{ID: "recently-added", Kind: "shelf",
				Title:       sdk.LocaleString{"en": "Recently Added", "fr": "Ajouts récents"},
				ArtworkSlot: "poster", Source: sdk.SourceSpec{Kind: "recently-added"}},
			{ID: "library", Kind: "grid",
				Title:       sdk.LocaleString{"en": "Library", "fr": "Bibliothèque"},
				ArtworkSlot: "poster", Source: sdk.SourceSpec{Kind: "all-items"}},
		}}},
		// Migration Wizard importers. The Music plugin's
		// launch-scope source is a Last.fm scrobble CSV export: play history
		// (counts + last-played) under the watch-state engagement category,
		// supplied as an exported file, strictly additive on re-run. The
		// compiled parser + scrobble match/apply live in this module's
		// importsources.go / import_lastfm.go / import_listenbrainz.go (they
		// implement the sdk.Source seam); this spec is the declarative
		// half.
		// Scrobble-import match relations: the bare relation
		// slugs core/migrate reads to confirm an imported play's artist/album,
		// supplied as plugin data so core hardcodes no music relation name.
		// Artist confirmation uses `appears-on` (Artist→Track Media); album
		// tie-break uses `album-track` (Album→Track Media, the album kind's only
		// Media-targeted edge — `album-artist` is Album→Artist, not reachable
		// from a Media-keyed lookup, so it is not listed here).
		ScrobbleMatch: &sdk.ScrobbleMatchSpec{
			ArtistConfirm: []string{"appears-on"},
			AlbumTieBreak: []string{"album-track"},
		},
		// Music Tracks are standalone scrobble targets: every play addresses the
		// Track Media itself, never an indexed child inside a larger work. This
		// explicit declaration keeps Core free of a Music-specific default.
		ScrobbleTarget: &sdk.ScrobbleTargetSpec{
			Default: scrobble.TargetStandalone,
		},
		Importers: []sdk.ImporterSpec{
			{
				ID:          LastfmImporterID,
				DisplayName: sdk.LocaleString{"en": "Last.fm scrobbles", "fr": "Écoutes Last.fm"},
				Categories:  []string{sdk.CategoryWatchState},
				Input:       "export-file",
				Conflict:    "additive",
			},
			{
				ID:          ListenBrainzImporterID,
				DisplayName: sdk.LocaleString{"en": "ListenBrainz listens", "fr": "Écoutes ListenBrainz"},
				Categories:  []string{sdk.CategoryWatchState},
				Input:       "export-file",
				Conflict:    "additive",
			},
			{
				ID:          NavidromeImporterID,
				DisplayName: sdk.LocaleString{"en": "Navidrome playlists & plays", "fr": "Playlists et écoutes Navidrome"},
				Categories:  []string{sdk.CategoryCollections, sdk.CategoryWatchState},
				Input:       "export-file",
				Conflict:    "additive",
			},
			{
				ID:          LocalM3UImporterID,
				DisplayName: sdk.LocaleString{"en": "Local M3U playlist", "fr": "Liste de lecture M3U locale"},
				Categories:  []string{sdk.CategoryCollections},
				Input:       "export-file",
				Conflict:    "additive",
			},
		},
		Scrapers: []sdk.ScraperSpec{
			{
				ID:          "musicbrainz",
				DisplayName: sdk.L("MusicBrainz"),
				Kind:        "http-json",
				Endpoint:    "https://musicbrainz.org/ws/2",
				// No API key — AuthRef stays empty. Etiquette
				// instead of auth: MusicBrainz requires a meaningful
				// User-Agent identifying the app + contact (the engine
				// sends one on every request), and throttles anonymous
				// clients to ~1 request/second — declared below with a
				// little headroom.
				AuthRef:              "",
				MinRequestIntervalMS: 1100,
				// Untagged-release identification: GET /release?query=…
				// returns {"releases": [{id, title, date, disambiguation},
				// …]} — the same keys as the lookup document, so one
				// FieldMap serves both; the chosen candidate's MBID feeds
				// the lookup below.
				SearchTemplate:  "/release?query={query}&fmt=json",
				CandidatesPath:  "releases",
				CandidateIDPath: "id",
				// Release (album) lookup by MBID, JSON format, with artist
				// credits and genres inlined. Enrichment-only: the library
				// is fully browsable from embedded tags alone.
				LookupTemplate: "/release/{id}?fmt=json&inc=artist-credits+release-groups+genres",
				FieldMap: map[string]string{
					// Real MusicBrainz release-lookup JSON paths.
					"title": "title",
					// MusicBrainz returns a full (sometimes partial)
					// YYYY-MM-DD date; the transform derives the year.
					"year":    "date",
					"summary": "disambiguation",
				},
				Transforms: map[string]string{"year": "year-from-date"},
				// SearchFieldMap makes the search-candidate mapping explicit.
				// MusicBrainz /release?query=… returns the same top-level
				// shape as the lookup document (title, date, disambiguation),
				// so the paths are identical — declared here so each template's
				// effective map is auditable without cross-referencing FieldMap.
				SearchFieldMap: map[string]string{
					"title":   "title",
					"year":    "date",
					"summary": "disambiguation",
				},
				// EntityMaps extract artist-credit entries from the release
				// lookup document and hydrate them as Artist entities in the
				// Media's Library, linked to the identified Track Media via the
				// artist kind's appears-on relation.
				//
				// MusicBrainz release lookup with ?inc=artist-credits returns:
				//   "artist-credit": [
				//     {"name": "<credited-as>",
				//      "artist": {"id": "<mbid>", "name": "<canonical>",
				//                 "sort-name": "<sort>"}, "joinphrase": ""},
				//     …
				//   ]
				// NamePath resolves the canonical name (artist.name); sort-name
				// and musicbrainz-id are Entity fields this Kind declares.
				// The credited-as string is in the element-root "name" field,
				// but the EntityMap contract carries no relation-attribute writer
				// — that field is preserved in FieldMap as a named field only
				// when the Kind declares a field for it. The artist Kind does not
				// declare "credited-as" as a stored field (it is a relation
				// attribute, not an entity property), so only the canonical
				// identity fields are captured here.
				EntityMaps: []sdk.EntityMap{
					{
						Kind:     "artist",
						Relation: "appears-on",
						Path:     "artist-credit",
						NamePath: "artist.name",
						FieldMap: map[string]string{
							"musicbrainz-id": "artist.id",
							"sort-name":      "artist.sort-name",
						},
					},
				},
				// MusicBrainz serves no images itself — cover art comes
				// from the Cover Art Archive scraper below.
			},
			{
				ID:          "cover-art-archive",
				DisplayName: sdk.L("Cover Art Archive"),
				Kind:        "http-json",
				Endpoint:    "https://coverartarchive.org",
				// Same MBID keyspace as MusicBrainz; no auth, same
				// please-send-a-User-Agent etiquette.
				AuthRef:        "",
				LookupTemplate: "/release/{id}",
				ArtworkMap: map[string]string{
					// Front cover of the release — the first entry of the
					// `images` array carries `"front": true` plus the
					// full-resolution `image` URL.
					"poster": "images.0.image",
					// 500px thumbnail for shelf/grid cards.
					"thumb": "images.0.thumbnails.500",
				},
			},
		},
	}
}
