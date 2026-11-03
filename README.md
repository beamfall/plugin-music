<p align="center">
  <img src=".github/brand/beamfall-logo.png" alt="Beamfall" width="96" height="96">
</p>

# Beamfall Music

This is the first-party Music plugin for [Beamfall Core](https://github.com/beamfall/core), the
self-hosted media server for the library you already own. Point a Beamfall Library at your music
folders, and this plugin reads each track's embedded tags to build artists, albums, and genres,
plays albums in disc and track order, and gives the Library a music home screen. It can import
your play history from Last.fm, ListenBrainz, or Navidrome, and can keep scrobbling to Last.fm and ListenBrainz as you listen.

It is free and open source under [AGPL-3.0](LICENSE).

## Features

- When a file carries a title tag, the plugin trusts it,
  along with the track, disc, and date tags beside it. Album, composer, sort names, lyrics, BPM,
  rating, comment, compilation flag, and MusicBrainz recording, release, and release-group IDs
  are all kept as track fields.
- Artists are credited on each track, albums
  are ordered containers that play in (disc, track) order, and genres are a flat list that a
  track can belong to several of. Multi-valued artist and genre tags become separate links,
  taken literally; "feat." strings are never split.
- A bare `01 - Title.flac` or `1-05 Title.flac` still
  yields a title and track number. An `Artist/Album (Year)/CD1/` folder layout fills in the
  artist, album, year, and disc when the tags are missing, and each inferred value is marked as
  as coming from the path.
- A `Various Artists` album artist or a compilation tag groups the album as one compilation.
- A `.lrc` file next to a track supplies its lyrics when the tags
  have none.
- Untagged releases can be identified against MusicBrainz, scored on title
  similarity with the year as corroboration. Only a strong match is applied automatically; a
  doubtful one waits for manual review, because a wrong auto-apply silently mislabels a track.
- Albums are enriched with title, date, and artist credits from
  MusicBrainz, with no API key and a request rate kept under MusicBrainz's one-per-second limit.
  The library is fully browsable from embedded tags alone.
- The home screen has Recently Played, Most Played, Recently Added, and your full Library. There is no Continue Watching shelf, because songs do not resume.
- You can import play history from a Last.fm scrobble CSV export or a
  ListenBrainz JSON or JSONL export, playlists and plays from a Navidrome export, and playlists
  from local M3U files. Imports are additive and safe to re-run.
- The Last.fm and ListenBrainz scrobble adapters send each play with the
  track's artist, album, and MusicBrainz IDs when they are known, and honor the service's
  `Retry-After` when it asks you to slow down.
- Import files have fixed size and item limits, M3U entries must
  stay inside the playlist's folder and may not follow links, and the filename, Last.fm, and
  ListenBrainz parsers are fuzz tested.
- Labels are available in English and French.

## How it fits into Beamfall

The Music plugin ships with [Beamfall Core](https://github.com/beamfall/core) and is compiled into
the server; you do not install it separately. It is built on the
[Beamfall plugin SDK](https://github.com/beamfall/plugin-sdk): Core depends on the SDK, plugins
depend on the SDK, and Core never depends on a plugin. All music vocabulary (artists, albums,
genres, scrobble services) lives in this plugin; Core has none of it.

| File | What it does |
| --- | --- |
| [`bundle.go`](bundle.go) | The declarative bundle: entity kinds, track fields, home layout, importers, and the MusicBrainz scraper. |
| [`parse.go`](parse.go) | The filename parser: tag-first track parsing with a filename fallback for tag-poor rips. |
| [`score.go`](score.go) | The match scorer and its fail-closed auto-apply threshold. |
| [`hydrate.go`](hydrate.go) | Maps embedded tags, folder layout, and `.lrc` sidecars to track fields and credits. |
| [`importsources.go`](importsources.go), [`import_lastfm.go`](import_lastfm.go), [`import_listenbrainz.go`](import_listenbrainz.go) | Migration sources for Last.fm and ListenBrainz exports. |
| [`import_navidrome.go`](import_navidrome.go) | Migration source for Navidrome playlist and play exports. |
| [`import_local_m3u.go`](import_local_m3u.go) | Migration source for local M3U playlists, with confined, link-free file access. |
| [`scrobble.go`](scrobble.go) | The Last.fm and ListenBrainz scrobble adapters. |
| [`pack.go`](pack.go) | Offline-pack resolvers for albums, collections, and track lists. |

## Develop

You need the Go toolchain named in [`go.mod`](go.mod), Python 3, and
[golangci-lint](https://golangci-lint.run/) on your `PATH`.

```sh
go test ./...        # unit, corpus, and conformance tests
script/gate.sh       # full local gate: gofmt, vet, tests with coverage floors, golangci-lint
script/gate.sh race  # tests under the race detector
script/gate.sh fuzz  # a bounded fuzz run of the filename and import parsers
```

The tests run the SDK's `sdktest` conformance harness against `Bundle()`, the parser, and the
scorer ([`conformance_test.go`](conformance_test.go)), plus a wrong-match regression corpus
([`matchcorpus_test.go`](matchcorpus_test.go)). CI runs the same gate on every pull request and
every push to `main`, and runs bounded fuzzing of the parsers on a schedule
([`conformance.yml`](.github/workflows/conformance.yml)).

## Contributing

Contributions require a signed Contributor License Agreement. Read
[CONTRIBUTING.md](CONTRIBUTING.md) and [CLA.md](CLA.md) before you open a pull request.

## Security

Please report vulnerabilities privately as described in [SECURITY.md](SECURITY.md). Do not open a
public issue.

## License

Beamfall Music is licensed under the GNU Affero General Public License v3.0. See
[LICENSE](LICENSE).
