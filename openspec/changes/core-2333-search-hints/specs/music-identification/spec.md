## ADDED Requirements

### Requirement: Recording search narrows by tagged artist, album and recording MBID

The MusicBrainz recording search SHALL add a Lucene clause for each of the Track's tagged
recording MBID, first artist and album to the title query. A Track without such tags SHALL be
searched by title alone. Artist and album values derived from folder names SHALL NOT narrow the
search.

#### Scenario: Tagged Track

- **WHEN** a Track tagged title `Airbag`, artist `Radiohead` and album `OK Computer` is searched
- **THEN** the search query is `(Airbag) AND artist:"Radiohead" AND release:"OK Computer"`

#### Scenario: Untagged Track

- **WHEN** a Track has no tags and its folders name an artist and album
- **THEN** the search query is the title alone
