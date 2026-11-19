## ADDED Requirements

### Requirement: Artists and albums opt into title search

The Music plugin SHALL declare the artist and album entity kinds FullTextSearchable so Core can return their entity pages in name searches, subject to the existing visibility rules.

#### Scenario: Search an artist or album name

- **WHEN** Core registers the Music plugin's entity kind policies
- **THEN** artist and album names are eligible for entity search and indexing of existing entities
