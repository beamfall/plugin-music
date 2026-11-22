## Why

CORE-2769. Plex groups an artist's discography into Albums, Singles & EPs, Live and Compilations.
Beamfall shows one "Album" rail because nothing records what kind of release an Album is.

## What Changes

- Tag hydration reads the MusicBrainz release-group type Picard embeds (`RELEASETYPE`,
  `MusicBrainz Album Type`): a primary type plus secondary types such as `album; live`.
  `ParseReleaseType` maps it to one group key: `album`, `ep`, `single`, `live`, `compilation`,
  `soundtrack`, `remix` or `other`. A secondary type with its own group wins over the primary type.
  A compilation with no type tag is `compilation`.
- The `album` kind declares a filterable `release-type` text field.
- The bundle exports `ReleaseTypeGroups`: the group keys in display order with their plural
  headings, so core's generic entity grouping can order and label an artist's releases without
  naming a domain type.

## Impact

- Core writes the field from hydration and groups related entities by it through `cmd/beamfall`
  glue; no core schema names a release type. MusicBrainz release-group lookup for untagged albums
  is a follow-up.
