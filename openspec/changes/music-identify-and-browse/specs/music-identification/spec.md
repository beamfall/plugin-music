## ADDED Requirements

### Requirement: Track identification scraper implies the parsed class

The searchable `musicbrainz` scraper SHALL declare an implied Media class equal to the class the
Music parser assigns, so core's identify pipeline selects it for parsed Tracks (CORE-2312).

#### Scenario: Parsed Track reaches the search

- **WHEN** the parser returns a Track with class `audio`
- **THEN** exactly one searchable scraper in the bundle implies `media.class` = `audio`
- **AND** the bundle declares the text Media field `class` that the implied key writes

### Requirement: Identification searches recordings

Track identification SHALL search and look up MusicBrainz recordings, never releases. A candidate's
title, year and summary SHALL come from the recording's `title`, `first-release-date` and
`disambiguation`, so applying a match never writes an album title onto a Track.

#### Scenario: Tagged Track identifies to its recording

- **WHEN** a Track tagged with title `Airbag` and date `1997` is searched
- **THEN** the search request targets `/recording?query=Airbag&fmt=json`
- **AND** the recording titled `Airbag` first released in 1997 scores at or above the auto-apply
  threshold and is the single best candidate
- **AND** the lookup maps title `Airbag` and year `1997`

### Requirement: A bare-number title never auto-applies uncorroborated

The scorer SHALL score a parsed title that has no letters below the auto-apply threshold when the
parse carried no year, even when the candidate title is equal.

#### Scenario: Fallback title "00"

- **WHEN** the parsed title is `00` with no year and a candidate is titled `00`
- **THEN** the score is below the auto-apply threshold
