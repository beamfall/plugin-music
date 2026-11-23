# music-browse-layout Specification

## Purpose
What the Music plugin's home Layout (the Library's Recommended view) shows: artists and albums,
never a flat list of Tracks.

## Requirements

### Requirement: Music home ends with an Albums grid and lists no Tracks

The Music home layout SHALL declare no `all-items` section (CORE-3347). It SHALL end with an
`Albums` grid sourced from `entity-items` for kind `album`, after the `Artists` shelf, and no two
home sections SHALL read the same Source.

#### Scenario: Recommended view

- **WHEN** a client renders the Music home layout
- **THEN** its last section is a grid of `album` entities
- **AND** no section lists every Track through `all-items`
- **AND** no album is listed by two sections
