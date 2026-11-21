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
- **THEN** the search request targets `/recording?query=(Airbag)&fmt=json` (CORE-2333 narrows it
  further when the Track carries artist, album or recording MBID tags)
- **AND** the recording titled `Airbag` first released in 1997 scores at or above the auto-apply
  threshold and is the single best candidate
- **AND** the lookup maps title `Airbag` and year `1997`

### Requirement: A bare-number title never auto-applies

The scorer SHALL score a parsed title that has no letters below the auto-apply threshold, whether
or not the parse carried a year, even when the candidate title and year are equal.

#### Scenario: Fallback title "00"

- **WHEN** the parsed title is `00` with no year and a candidate is titled `00`
- **THEN** the score is below the auto-apply threshold

#### Scenario: Ordinal title with a date tag

- **WHEN** the parsed title is `03` with year 2004 and a candidate is titled `03` from 2004
- **THEN** the score is below the auto-apply threshold

### Requirement: A later-year cover never auto-applies over the original

The parser SHALL take the year from ORIGINALDATE or ORIGINALYEAR before DATE. The scorer SHALL
score a candidate first released earlier than the parsed year (a reissue) below the auto-apply
threshold.

#### Scenario: Original date tag picks the original

- **WHEN** a Track tagged DATE 2017 and ORIGINALDATE 1997 is searched and the candidates are the
  1997 recording and a 2017 cover of the same title
- **THEN** the 1997 recording is the single best candidate at or above the threshold

#### Scenario: Reissue date only

- **WHEN** the parsed year is 1997 and a same-titled candidate was first released in 1959
- **THEN** the score is below the auto-apply threshold

### Requirement: Tracks take release cover art

The lookup SHALL map the Track `poster` and `thumb` artwork slots from the recording's first
release, rendered as Cover Art Archive URLs.

#### Scenario: Recording with a release

- **WHEN** the looked-up recording's first release has id `rel-ok-computer`
- **THEN** `poster` is `https://coverartarchive.org/release/rel-ok-computer/front`
- **AND** `thumb` is `https://coverartarchive.org/release/rel-ok-computer/front-500`
