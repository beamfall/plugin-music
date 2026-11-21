## ADDED Requirements

### Requirement: ID3v1 genre codes decode to names

Tag hydration SHALL decode ID3v1 genre codes to their names and SHALL drop placeholder genres, so
no category is named after a numeric code.

#### Scenario: Parenthesised and bare codes

- **WHEN** a Track's GENRE tag is `(12)`, `17` or `(17)(4)`
- **THEN** its genres are none (`Other` is a placeholder), `Rock`, and `Rock` plus `Disco`

#### Scenario: Refinement text wins

- **WHEN** a Track's GENRE tag is `(4)Eurodisco`
- **THEN** its genre is `Eurodisco`

### Requirement: Album genres come from the release group

The bundle SHALL export an album-genre lookup against MusicBrainz release groups that shares the
`musicbrainz` scraper's ID, endpoint and request interval, accepts a candidate only when its
normalised title and artist match the album, and selects at most three genres.

#### Scenario: Matching release group

- **WHEN** an album `OK Computer` by `Radiohead` is searched
- **THEN** the query names both the release group and the artist
- **AND** only a candidate titled `OK Computer` credited to `Radiohead` is picked

#### Scenario: Genre selection

- **WHEN** a release group votes `alternative rock` 12, `art rock` 9 and `electronic` 2
- **THEN** the album's genres are `Alternative Rock` and `Art Rock`

#### Scenario: Artist fallback

- **WHEN** a release group carries no genres and its artist votes `shoegaze`
- **THEN** the album's genre is `Shoegaze`

### Requirement: Enriched genres use their own relation

Genres sourced from the release group SHALL be written under the `genre` kind's
`classifies-release` relation, never under `classifies`, so tag hydration and enrichment each
replace only their own rows.

#### Scenario: Rescan keeps enriched genres

- **WHEN** a Track with an enriched genre is rescanned
- **THEN** its `classifies-release` rows are unchanged
