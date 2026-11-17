## Why

Tower QA (2026-11-17) found three Music plugin defects:

- **CORE-2312 (T08).** No Track ever identifies. The parser labels every Track `audio`, but the
  `musicbrainz` scraper declares no implied class. Core's identify pipeline picks only a searchable
  scraper whose `ImpliedMediaFields` class equals the parsed class, so it skips every Track. The
  scraper also searched MusicBrainz *releases*, so even a forced match would have renamed a Track
  to its album's title.
- **CORE-2327 / CORE-1147 (T18), plugin part.** The Music home layout offers no way to browse by
  Artist or Album.
- **CORE-2313 (T09), music part.** A file whose name has no title text after the track number,
  such as `00 -.flac`, makes the parser fail. Core then stores the raw stem, so the Track is titled
  `00 -`.

## What Changes

- The `musicbrainz` scraper declares `ImpliedMediaFields {"media.class": "audio"}`. The bundle
  declares the text Media field `class` that this key requires.
- The `musicbrainz` scraper searches and looks up **recordings** (`/recording`) instead of
  releases. A candidate's year comes from `first-release-date`.
- The scorer does not auto-apply a title that has no letters (a bare number) unless a year
  corroborates it.
- The home layout gains `Albums` and `Artists` shelves (`entity-items` Sources). It also gains
  `album-detail` and `artist-detail` layouts, which list each entity's Tracks (`entity-media`).
- The parser tries more fallbacks when the filename has no letter-bearing title:
  1. The ordinal-stripped residue, such as `1979`.
  2. The stem with separator punctuation trimmed, such as `00`.

  It fails only when neither yields any text. A TITLE tag still wins over all of these.
- The bundle version moves to `0.1.10`, the version of the next release tag.

## Non-goals

- **Artist and album in the search query.** Core passes only `ParsedMedia.Title` as `{query}`, and
  `sdk.ParsedMedia` and `sdk.Candidate` carry no artist or album. Narrowing a recording search by
  artist and album needs a core or SDK seam, which belongs to another batch.
- **Library browse sorts and filters.** The video-only options (Watched, Resolution, Last watched)
  and the missing artist/album/year/recently-played options are hardcoded in core's web client
  and graph (`LIBRARY_SORTS`, `store.ValidLibraryBrowseSort`). The SDK has no seam that lets a
  plugin declare sorts.
- **Artist subtitle on cards.** Core's `graph.SourceItem` carries no subtitle or credit field.
- **Cover Art Archive keying.** That scraper stays keyed on release MBIDs. Core's identify flow does
  not chain it, so moving `musicbrainz` to recording IDs does not affect it.

## Failure modes

- Without artist or album in the query, a title shared by many recordings ties at the top score.
  The pipeline then fails closed to the unmatched queue rather than guess. This is the intended
  posture until the query seam exists.
- An unmatched-queue row from before this change holds a release MBID. Applying it after this
  change does a recording lookup that fails with an error, and the operator searches again. No
  Music row can exist in practice, because no Track ever reached the identify search.
- A bare-number fallback title, such as `00`, scores at most 0.60 without a year. It queues for
  review and never auto-applies.

## Acceptance evidence

- Bundle tests pin the scraper's implied class to the parser's class and to a declared MediaClass.
- A tagged-track fixture goes through the plugin side of the identify path offline: parse, then the
  core scraper-selection rule, then `sdktest.CompileScraperTrace` over a recorded recording-search
  document, then the scorer, then the lookup trace. It produces a recording-search request, one
  winning candidate at or above `AutoApplyThreshold`, and the Track's own title and year.
- Parser tests cover `00 -.flac`, `05 - 1979.flac` and a TITLE tag that wins over a junk filename.
  A match-corpus case shows that a bare-number title does not auto-apply.
- `gofmt -l`, `golangci-lint run`, `go test ./...` and `openspec validate --all --strict` pass.

## Rollback

Revert the commits. The bundle carries no migration. Fields written by an applied recording
identification are ordinary operator-tier metadata, and a later re-identify or manual edit
replaces them.
