## ADDED Requirements

### Requirement: Albums record their release type

Tag hydration SHALL map the MusicBrainz release-group type tag to one release type group, and the
bundle SHALL declare the `release-type` field on the `album` kind and export the groups in display
order with localized plural headings.

#### Scenario: Secondary type wins

- **WHEN** a Track's RELEASETYPE tag is `album; live`
- **THEN** its release type is `live`

#### Scenario: Primary types

- **WHEN** the tag is `Single`, `EP` or `album`
- **THEN** the release type is `single`, `ep` or `album`

#### Scenario: Compilation fallback

- **WHEN** a Track has no release type tag and is a compilation
- **THEN** its release type is `compilation`

#### Scenario: Unknown type

- **WHEN** the tag is empty or names no known type
- **THEN** the release type is empty
