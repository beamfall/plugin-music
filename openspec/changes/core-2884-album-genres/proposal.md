## Why

CORE-2884. Tower's Music › Categories lists three genres for about 760 albums. Most files carry no
GENRE tag, and many that do carry an ID3v1 code such as `(12)` or `17`, which the pivot shows as a
literal category named `(12)`.

## What Changes

- Tag hydration decodes ID3v1 genre codes: a bare number (`17`), a parenthesised code (`(12)`), a
  chain (`(17)(4)`), the `(RX)` and `(CR)` markers, and refinement text after a code
  (`(4)Eurodisco`). Placeholders (`Other`, `Unknown`, `Genre`) are dropped.
- The bundle exports an album-genre lookup that core's generic entity-enrichment job runs against
  MusicBrainz release groups:
  - `AlbumGenreScraper` derives a release-group search and lookup from the bundle's `musicbrainz`
    scraper, so both share one ID and one request throttle.
  - `AlbumGenreQuery` builds the search query: the release MBID when the album has one, otherwise
    the album title and artist.
  - `PickAlbumCandidate` accepts only a candidate whose normalised title and artist match.
  - `SelectAlbumGenres` keeps up to three release-group genres voted at least half as often as the
    top one, falls back to the artist's genres, and title-cases the names.
- The `genre` kind declares a second Media relation, `classifies-release`, for genres sourced from
  the album's release group. Tag genres keep `classifies`, so a rescan and an enrichment pass never
  overwrite each other.

## Impact

- Core consumes the exported helpers from `cmd/beamfall` glue; no core schema names a domain kind.
