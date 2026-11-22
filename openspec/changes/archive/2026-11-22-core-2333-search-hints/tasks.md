## 1. Plugin

- [x] 1.1 Repin plugin-sdk to v0.4.8
- [x] 1.2 Parser derives recording, artist and album clauses from tags only
- [x] 1.3 The musicbrainz search template carries the three optional hint placeholders

## 2. Tests

- [x] 2.1 Parse test: clauses from tags, Lucene quoting, none for an untagged file, an over-long clause dropped
- [x] 2.2 Identify trace test: the tagged Track's search URL carries the artist and album clauses
