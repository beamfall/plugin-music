## 1. Identification (CORE-2312)

- [ ] 1.1 Declare the `class` Media field and `ImpliedMediaFields {"media.class": "audio"}` on `musicbrainz`
- [ ] 1.2 Switch `musicbrainz` search and lookup to recordings
- [ ] 1.3 Never auto-apply a bare-number title without a year
- [ ] 1.4 Add the offline tagged-track identify-path test

## 2. Titles (CORE-2313)

- [ ] 2.1 Fall back to the residue, then the trimmed stem, before failing the parse
- [ ] 2.2 Add parser and match-corpus cases

## 3. Layout (CORE-2327, CORE-1147)

- [ ] 3.1 Add the Albums and Artists shelves and the album and artist detail layouts
- [ ] 3.2 Add the layout test

## 4. Close

- [ ] 4.1 Bump the bundle version to `0.1.10`
- [ ] 4.2 Run `script/gate.sh`
- [ ] 4.3 Archive this change with `openspec archive music-identify-and-browse`
