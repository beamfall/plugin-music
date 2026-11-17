## ADDED Requirements

### Requirement: Music home browses by Album and Artist

The Music home layout SHALL offer an `Albums` shelf and an `Artists` shelf, each sourced from
`entity-items` scoped to its Entity Kind. It SHALL declare `album-detail` and `artist-detail`
layouts that list the entity's Tracks through an `entity-media` Source (CORE-2327, CORE-1147). The
layout SHALL declare no video-only Source such as `continue-watching`.

#### Scenario: Home layout rows

- **WHEN** a client renders the Music home layout
- **THEN** it includes shelves sourced from `entity-items` for kinds `album` and `artist`
- **AND** no section uses the `continue-watching` Source

#### Scenario: Album page

- **WHEN** a client opens an Album entity
- **THEN** the `album-detail` layout lists that Album's Tracks
