# MusicBrainz recording search narrows by tagged artist, album and MBID

Ticket: CORE-2333 (`queue:core:main`).

## Why

Track identification searched MusicBrainz recordings by title alone. A short or common title
returns many unrelated recordings, so the right one rarely scores as the unique winner.
plugin-sdk v0.4.8 lets a parser pass named search hints that core substitutes into the scraper's
SearchTemplate query (`{hint.<key>}`).

## What Changes

- Repin plugin-sdk to v0.4.8.
- The parser sets three optional hints from the file's tags, each a complete Lucene clause:
  `recording_clause` (` AND rid:<mbid>` from the recording MBID tag), `artist_clause`
  (` AND artist:"…"` from the first tagged artist) and `album_clause` (` AND release:"…"` from the
  tagged album). Phrases are Lucene-quoted. Folder-derived artist and album values are not used,
  and a clause over the SDK's 256-byte limit is dropped.
- The `musicbrainz` search template becomes
  `/recording?query=({query}){hint.recording_clause}{hint.artist_clause}{hint.album_clause}&fmt=json`.
  An untagged Track gets empty hints and the title-only search. The album-genre release-group
  scraper is unchanged.

## Impact

- Code: `search_hints.go`, `parse.go`, `bundle.go`, `go.mod`.
- Needs core with CORE-2333's hint substitution; older core sends the placeholders unsubstituted.
