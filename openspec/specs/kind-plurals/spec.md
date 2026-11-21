# kind-plurals Specification

## Purpose
TBD - created by archiving change core-2820-kind-plurals. Update Purpose after archive.

## Requirements

### Requirement: Entity kinds declare plural display names

Every entity kind this plugin declares SHALL carry a `PluralDisplayName` with English and French entries, so clients can head a group of several entities of the kind in the plural.

#### Scenario: Head a group of related entities

- **WHEN** a client renders a group of related entities of one of this plugin's kinds
- **THEN** the kind offers a plural label (artist "Artists", album "Albums", genre "Genres")
