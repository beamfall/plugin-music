## 1. Identification (CORE-2312)

- [x] 1.1 Declare the `class` Media field and `ImpliedMediaFields {"media.class": "audio"}` on `musicbrainz`
- [x] 1.2 Switch `musicbrainz` search and lookup to recordings
- [x] 1.3 Never auto-apply a bare-number title, whatever the year
- [x] 1.4 Add the offline tagged-track identify-path test
- [x] 1.5 Prefer ORIGINALDATE for the year and keep a later-year cover below the threshold
- [x] 1.6 Map Track poster and thumb from the first release to the Cover Art Archive

## 2. Titles (CORE-2313)

- [x] 2.1 Fall back to the residue, then the trimmed stem, before failing the parse
- [x] 2.2 Add parser and match-corpus cases
- [x] 2.3 Fill the track index from a bare ordinal stem

## 3. Layout (CORE-2327, CORE-1147)

- [x] 3.1 Add the Albums and Artists shelves and the album and artist detail layouts
- [x] 3.2 Add the layout test

## 4. Close

- [ ] 4.1 Run `script/gate.sh`
- [ ] 4.2 Archive this change with `openspec archive music-identify-and-browse`
