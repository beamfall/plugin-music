package music

import "strings"

// Search hints (CORE-2333). The musicbrainz recording search narrows a Track's
// title with its tagged artist, album and recording MBID. Each hint is a
// complete Lucene clause, leading " AND " included, so a missing hint (core
// substitutes "") leaves today's title-only query rather than an empty phrase
// such as artist:"" that MusicBrainz would reject. Only tag values are used:
// a path-derived artist or album is a folder name, and a wrong filter hides
// the right recording.
const (
	hintRecordingClause = "recording_clause"
	hintArtistClause    = "artist_clause"
	hintAlbumClause     = "album_clause"

	// maxHintBytes is the SDK's per-hint value limit; a longer clause is
	// dropped rather than truncated into a different query.
	maxHintBytes = 256
)

// recordingSearchHints builds the musicbrainz search clauses from a file's
// tag hydration, or nil when the tags carry none.
func recordingSearchHints(tags Hydration) map[string]string {
	hints := map[string]string{}
	add := func(key, clause string) {
		if len(clause) <= maxHintBytes {
			hints[key] = clause
		}
	}
	if id := strings.TrimSpace(tags.RecordingMBID); mbidPattern.MatchString(id) {
		add(hintRecordingClause, " AND rid:"+strings.ToLower(id))
	}
	for _, credit := range tags.Credits {
		if tags.Sources["artist"] == HydrationSourcePath {
			break
		}
		if artist := strings.TrimSpace(credit.Artist); artist != "" {
			add(hintArtistClause, ` AND artist:"`+luceneQuoted(artist)+`"`)
			break
		}
	}
	if album := strings.TrimSpace(tags.Album); album != "" && tags.Sources["album"] != HydrationSourcePath {
		add(hintAlbumClause, ` AND release:"`+luceneQuoted(album)+`"`)
	}
	if len(hints) == 0 {
		return nil
	}
	return hints
}
