# kind-plurals Specification

## Purpose
The plural display names this plugin declares for its entity kinds, which clients use to head a group
of several entities of one kind.

## Requirements

### Requirement: Entity kinds declare plural display names

Every entity kind this plugin declares SHALL carry a `PluralDisplayName` with English and French entries, so clients can head a group of several entities of the kind in the plural.

#### Scenario: Head a group of related entities

- **WHEN** a client renders a group of related entities of one of this plugin's kinds
- **THEN** the kind offers a plural label (artist "Artists", album "Albums", genre "Genres")
