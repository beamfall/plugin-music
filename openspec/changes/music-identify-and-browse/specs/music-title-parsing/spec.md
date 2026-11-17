## ADDED Requirements

### Requirement: Title falls back instead of leaving a raw stem

The Music parser SHALL take a Track title from the first source that yields one (CORE-2313):

1. A non-empty TITLE tag.
2. The letter-bearing filename title.
3. The ordinal-stripped filename residue.
4. The filename stem with surrounding separator punctuation trimmed.

It SHALL fail only when none of these yields any text.

#### Scenario: Empty residue after the track number

- **WHEN** the file is `00 -.flac` and carries no TITLE tag
- **THEN** the parse succeeds with title `00`, never `00 -`

#### Scenario: Numeric title mistaken for a year

- **WHEN** the file is `05 - 1979.flac` and carries no TITLE tag
- **THEN** the parse succeeds with title `1979`, track 5 and no year taken from that token

#### Scenario: Tag wins over a junk filename

- **WHEN** the file is `00 -.flac` and carries the TITLE tag `Intro`
- **THEN** the title is `Intro`

#### Scenario: Parenthesised year as the only title

- **WHEN** the file is `01 - (1999).flac` and carries no TITLE tag
- **THEN** the parse succeeds with title `1999` and track 1, and that title never auto-applies

### Requirement: A bare ordinal stem is the track index

The parser SHALL take the track index from a filename stem of one to three digits when no tag
supplies a track number.

#### Scenario: Bare ordinal stem

- **WHEN** the file is `07.flac` and carries no tags
- **THEN** the parse succeeds with title `07` and track 7
