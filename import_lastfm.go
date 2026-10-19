package music

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	sdk "github.com/beamfall/plugin-sdk"
)

// maxImportBytes bounds the size of a single importer source read (CSV or
// JSON/JSONL) so a malicious or corrupted export cannot exhaust the
// in-process core's memory. 64 MB comfortably covers real-world scrobble
// history exports (which run low tens of MB even at hundreds of thousands of
// plays) while still bounding worst-case growth.
const maxImportBytes = 64 << 20

// errImportTooLarge is returned when an importer source exceeds maxImportBytes.
var errImportTooLarge = errors.New("import source exceeds 64 MB size limit")

// ParseLastfmCSV reads a Last.fm scrobble CSV export and returns one
// sdk.ScrobbleRecord per row. It tolerates the two common export shapes:
//
//   - Headered: a first row naming the columns — artist / track (or "name") /
//     album, plus a timestamp column ("uts" unix-seconds, or a date string in
//     "timestamp" / "date" / "utc_time"). Column order is taken from the header.
//   - Positional (no recognized header): the classic `artist, album, track,
//     timestamp` order, where timestamp is unix-seconds or a date string.
//
// A row missing both artist and track is skipped (a blank or trailing line). An
// unparseable timestamp is tolerated — the scrobble keeps a zero PlayedAt (the
// play still counts; last-played just stays unset for that row).
//
// The input is capped at maxImportBytes to protect the in-process core from
// unbounded memory growth on a malicious or corrupted export.
func ParseLastfmCSV(r io.Reader) ([]sdk.ScrobbleRecord, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxImportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxImportBytes {
		return nil, errImportTooLarge
	}

	reader := csv.NewReader(bytes.NewReader(raw))
	reader.FieldsPerRecord = -1 // ragged exports: some rows omit a trailing field
	reader.TrimLeadingSpace = true

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []sdk.ScrobbleRecord{}, nil
	}

	cols, headerConsumed := lastfmColumns(rows[0])
	body := rows
	if headerConsumed {
		body = rows[1:]
	}

	scrobbles := make([]sdk.ScrobbleRecord, 0, len(body))
	for _, row := range body {
		artist := strings.TrimSpace(field(row, cols.artist))
		track := strings.TrimSpace(field(row, cols.track))
		if artist == "" && track == "" {
			continue // blank/trailing line
		}
		scrobbles = append(scrobbles, sdk.ScrobbleRecord{
			Present:  true,
			Artist:   artist,
			Track:    track,
			Album:    strings.TrimSpace(field(row, cols.album)),
			PlayedAt: parseScrobbleTime(strings.TrimSpace(field(row, cols.timestamp))),
		})
	}
	return scrobbles, nil
}

// columnMap holds the 0-based column index of each scrobble field; -1 means the
// export carries no such column.
type columnMap struct {
	artist, track, album, timestamp int
}

// lastfmColumns resolves the column layout from a candidate header row. If the
// first row names at least two recognized columns it is consumed as a header
// and its order honored; otherwise the classic positional order `artist,
// album, track, timestamp` is assumed and the row is treated as data. Two
// recognized names (not one) are required because a positional DATA row can
// coincidentally spell a single column name — a track titled "Time" or an
// album titled "Release" — and swallowing that row as a header would misalign
// every column and silently import zero scrobbles. Real headered exports name
// artist/track/album/timestamp columns together, always clearing two.
func lastfmColumns(first []string) (cols columnMap, headerConsumed bool) {
	cols = columnMap{artist: -1, track: -1, album: -1, timestamp: -1}
	recognized := 0
	for i, raw := range first {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case "artist", "artist name":
			cols.artist = i
			recognized++
		case "track", "track name", "name", "title", "song":
			cols.track = i
			recognized++
		case "album", "album name", "release":
			cols.album = i
			recognized++
		case "uts", "timestamp", "date", "utc_time", "time", "datetime":
			// Prefer the unix-seconds column when several timestamp-ish columns
			// exist (uts is unambiguous); otherwise take the first seen.
			if cols.timestamp == -1 || strings.EqualFold(strings.TrimSpace(raw), "uts") {
				cols.timestamp = i
			}
			recognized++
		}
	}
	if recognized >= 2 {
		return cols, true
	}
	// Positional fallback: artist, album, track, timestamp (the lastfm-to-csv /
	// Universal Scrobbler default order).
	return columnMap{artist: 0, album: 1, track: 2, timestamp: 3}, false
}

func field(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return row[idx]
}

// scrobbleTimeLayouts are the date string formats Last.fm exports use when a row
// is not bare unix-seconds. Last.fm's own UI/export renders e.g.
// "06 Jun 2024 21:30" and the `utc_time` column "2024-06-06 21:30".
var scrobbleTimeLayouts = []string{
	"02 Jan 2006 15:04",
	"2006-01-02 15:04",
	"2006-01-02 15:04:05",
	time.RFC3339,
}

// parseScrobbleTime parses a scrobble timestamp: unix-seconds when the cell is a
// bare integer (the "uts" column), else one of the known date layouts (UTC).
// Returns the zero time when the cell is empty or unparseable — the caller
// treats that as "no last-played" without dropping the play.
func parseScrobbleTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	if secs, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return time.Unix(secs, 0).UTC()
	}
	for _, layout := range scrobbleTimeLayouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
