package music_test

import (
	"os"
	"path/filepath"
	"testing"

	sdk "github.com/beamfall/plugin-sdk"
	"github.com/beamfall/plugin-sdk/sdktest"

	music "github.com/beamfall/plugin-music"
)

// TestMatchCorpus seeds the Music plugin's matching-regression corpus
// via the SDK match-corpus harness: one MatchFamily per hostile-input
// class this plugin must not silently mis-resolve. Under the fail-closed
// matching baseline the regression a family guards is a *wrong
// match* — a WantID=="" case routing to the unmatched queue is the intended,
// passing outcome, not a failure. The parser/scorer under test are the
// filename parser/scorer path: tag-poor fixtures exercise filename
// fallback, while tag-bearing fixtures pin the tag-authoritative rules.
func TestMatchCorpus(t *testing.T) {
	sidecarDir := t.TempDir()
	sidecarPath := filepath.Join(sidecarDir, "01 - Airbag.flac")
	if err := os.WriteFile(filepath.Join(sidecarDir, "01 - Airbag.lrc"), []byte("sidecar lyrics"), 0o600); err != nil {
		t.Fatal(err)
	}

	folderFallback := folderFallbackFamily()
	extendedTags := extendedTagsFamily()
	siblingLRC := siblingLRCFamily(sidecarPath)
	compilation := compilationTagFamily()
	mbidGrouping := mbidGroupingFamily()
	parser := hydrationGuardParser{
		inner: music.NewParser(),
		guards: map[string]func(music.Hydration) bool{
			folderFallback.Cases[0].Path: func(h music.Hydration) bool {
				return h.Album == "OK Computer" && h.AlbumArtist == "Radiohead" &&
					h.DiscNumber == 1 && h.TrackNumber == 1
			},
			extendedTags.Cases[0].Path: func(h music.Hydration) bool {
				return h.Composer == "Radiohead" && h.Lyrics == "lyrics" && h.BPMSet && h.BPM == 125
			},
			siblingLRC.Cases[0].Path: func(h music.Hydration) bool {
				return h.Lyrics == "sidecar lyrics" && h.Sources["lyrics"] == music.HydrationSourceSidecar
			},
			compilation.Cases[0].Path: compilationHydrationIsGrouped,
			compilation.Cases[1].Path: compilationHydrationIsGrouped,
			mbidGrouping.Cases[0].Path: func(h music.Hydration) bool {
				return h.ReleaseMBID == "release-1" && h.ArtistMBID == "artist-1"
			},
		},
	}

	sdktest.RunMatchCorpus(t, parser, music.NewScorer(), []sdktest.MatchFamily{
		tagPoorRipFamily(),
		folderFallback,
		extendedTags,
		siblingLRC,
		compilation,
		mbidGrouping,
		classicalWorkMovementFamily(),
		variousArtistsCompilationFamily(),
		nonLatinMetadataFamily(),
		popularityTiebreakFamily(),
	})
}

// hydrationGuardParser keeps the corpus on the production parser/scorer while
// making hydration-only rules observable to the corpus. ParsedMedia cannot
// carry lyrics, compilation, grouping IDs, or folder album/artist fields, so a
// guard fails closed before delegating when the mapped rule output regresses.
type hydrationGuardParser struct {
	inner  sdk.FilenameParser
	guards map[string]func(music.Hydration) bool
}

func (p hydrationGuardParser) Parse(path string, probe sdk.ParseProbe) (sdk.ParsedMedia, bool) {
	if guard := p.guards[path]; guard != nil && !guard(music.MapHydration(probe.Tags, path)) {
		return sdk.ParsedMedia{}, false
	}
	return p.inner.Parse(path, probe)
}

func compilationHydrationIsGrouped(h music.Hydration) bool {
	return h.CompilationSet && h.Compilation && h.AlbumArtist == "Various Artists" && len(h.Credits) == 1
}

// TestHydrationRuleCorpus complements the parser/scorer corpus with the
// hydration and grouping inputs that ParsedMedia does not carry. Each family
// directly guards one tag/folder-fallback hydration rule, so deleting a
// mapper rule cannot leave an unrelated title-only match green.
func TestHydrationRuleCorpus(t *testing.T) {
	t.Run("folder-fallback", func(t *testing.T) {
		h := music.MapHydration(nil, "/music/Radiohead/OK Computer/CD1/01 - Airbag.flac")
		if h.Title != "Airbag" || h.Album != "OK Computer" || h.AlbumArtist != "Radiohead" || h.DiscNumber != 1 || h.TrackNumber != 1 {
			t.Fatalf("folder hydration = %+v", h)
		}
	})

	t.Run("extended-standard-tags", func(t *testing.T) {
		id3 := music.MapHydration(map[string]string{
			"TCOM": "Composer", "TCMP": "1", "TSOP": "Artist Sort", "TSOA": "Album Sort",
			"TSO2": "Album Artist Sort", "USLT": "Lyrics", "TBPM": "125", "POPM": "196/255",
			"COMM": "Comment", "MUSICBRAINZ_RELEASEGROUPID": "release-group-1",
		}, "/music/Artist/Album/01 - Track.mp3")
		if id3.Composer != "Composer" || !id3.Compilation || id3.ArtistSort != "Artist Sort" ||
			id3.AlbumSort != "Album Sort" || id3.AlbumArtistSort != "Album Artist Sort" ||
			id3.Lyrics != "Lyrics" || !id3.BPMSet || id3.BPM != 125 || !id3.RatingSet ||
			id3.Rating != 196 || id3.Comment != "Comment" || id3.ReleaseGroupMBID != "release-group-1" {
			t.Fatalf("ID3 hydration = %+v", id3)
		}

		vorbis := music.MapHydration(map[string]string{
			"COMPOSER": "Composer", "COMPILATION": "1", "ARTISTSORT": "Artist Sort",
			"ALBUMSORT": "Album Sort", "ALBUMARTISTSORT": "Album Artist Sort", "LYRICS": "Lyrics",
			"BPM": "126", "RATING": "4", "COMMENT": "Comment", "MUSICBRAINZ_RELEASEGROUPID": "release-group-2",
		}, "/music/Artist/Album/01 - Track.flac")
		if vorbis.Composer != "Composer" || !vorbis.Compilation || vorbis.ArtistSort != "Artist Sort" ||
			vorbis.AlbumSort != "Album Sort" || vorbis.AlbumArtistSort != "Album Artist Sort" ||
			vorbis.Lyrics != "Lyrics" || !vorbis.BPMSet || vorbis.BPM != 126 || !vorbis.RatingSet ||
			vorbis.Rating != 4 || vorbis.Comment != "Comment" || vorbis.ReleaseGroupMBID != "release-group-2" {
			t.Fatalf("Vorbis hydration = %+v", vorbis)
		}

		mp4 := music.MapHydration(map[string]string{
			"©wrt": "Composer", "cpil": "1", "soar": "Artist Sort", "soal": "Album Sort",
			"soaa": "Album Artist Sort", "©lyr": "Lyrics", "tmpo": "127", "rtng": "5", "©cmt": "Comment",
		}, "/music/Artist/Album/01 - Track.m4a")
		if mp4.Composer != "Composer" || !mp4.Compilation || mp4.ArtistSort != "Artist Sort" ||
			mp4.AlbumSort != "Album Sort" || mp4.AlbumArtistSort != "Album Artist Sort" ||
			mp4.Lyrics != "Lyrics" || !mp4.BPMSet || mp4.BPM != 127 || !mp4.RatingSet ||
			mp4.Rating != 5 || mp4.Comment != "Comment" {
			t.Fatalf("MP4 hydration = %+v", mp4)
		}
	})

	t.Run("lyrics-sidecar", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "01 - Airbag.flac")
		if err := os.WriteFile(filepath.Join(dir, "01 - Airbag.lrc"), []byte("sidecar lyrics"), 0o600); err != nil {
			t.Fatal(err)
		}
		h := music.MapHydration(nil, path)
		if h.Lyrics != "sidecar lyrics" || h.Sources["lyrics"] != music.HydrationSourceSidecar {
			t.Fatalf("sidecar hydration = %+v", h)
		}
	})

	t.Run("compilation-tag-grouping-input", func(t *testing.T) {
		albumIDs := map[string]bool{}
		trackArtists := map[string]bool{}
		for i, fixture := range []struct {
			artist      string
			compilation string
			albumArtist string
		}{
			{artist: "Artist One", compilation: "1"},
			{artist: "Artist Two", compilation: "1"},
			{artist: "Artist Three", albumArtist: "Various Artists"},
		} {
			h := music.MapHydration(map[string]string{
				"ALBUM": "Shared Compilation", "ARTIST": fixture.artist,
				"ALBUMARTIST": fixture.albumArtist, "COMPILATION": fixture.compilation,
				"MUSICBRAINZ_ALBUMID": "compilation-release",
			}, filepath.Join("/music/Compilations/Shared Compilation", string(rune('1'+i))+" - Track.flac"))
			if !h.CompilationSet || !h.Compilation || h.AlbumArtist != "Various Artists" || len(h.Credits) != 1 {
				t.Fatalf("compilation hydration %d = %+v", i, h)
			}
			albumIDs[h.ReleaseMBID] = true
			trackArtists[h.Credits[0].Artist] = true
		}
		if len(albumIDs) != 1 || !albumIDs["compilation-release"] || len(trackArtists) != 3 {
			t.Fatalf("compilation groups = %v, track artists = %v", albumIDs, trackArtists)
		}
	})

	t.Run("mbid-first-grouping-input", func(t *testing.T) {
		albumIDs := map[string]bool{}
		for _, fixture := range []struct{ album, releaseID string }{
			{album: "Same Album", releaseID: "release-a"},
			{album: "Same Album", releaseID: "release-b"},
			{album: "Alternate Spelling", releaseID: "release-a"},
		} {
			h := music.MapHydration(map[string]string{
				"ALBUM": fixture.album, "ARTIST": "The Artist", "MUSICBRAINZ_ALBUMID": fixture.releaseID,
			}, "/music/The Artist/Album/01 - Track.flac")
			albumIDs[h.ReleaseMBID] = true
		}
		if len(albumIDs) != 2 || !albumIDs["release-a"] || !albumIDs["release-b"] {
			t.Fatalf("MBID-first album inputs = %v, want release-a and release-b", albumIDs)
		}

		artistIDs := map[string]bool{}
		for _, artist := range []string{"The Artist", "Artist, The"} {
			h := music.MapHydration(map[string]string{
				"ALBUM": "Album", "ARTIST": artist, "ALBUMARTIST": artist,
				"MUSICBRAINZ_ARTISTID": "artist-1", "MUSICBRAINZ_ALBUMARTISTID": "album-artist-1",
				"MUSICBRAINZ_RELEASEGROUPID": "release-group-1",
			}, "/music/Artist/Album/01 - Track.flac")
			if h.AlbumArtistMBID != "album-artist-1" || h.ReleaseGroupMBID != "release-group-1" ||
				len(h.Credits) != 1 || h.Credits[0].MBID != "artist-1" {
				t.Fatalf("MBID hydration = %+v", h)
			}
			artistIDs[h.ArtistMBID] = true
		}
		if len(artistIDs) != 1 || !artistIDs["artist-1"] {
			t.Fatalf("MBID-first artist inputs = %v, want artist-1", artistIDs)
		}
	})
}

func folderFallbackFamily() sdktest.MatchFamily {
	return sdktest.MatchFamily{Name: "folder-fallback", Cases: []sdktest.MatchCase{{
		Name: "artist-album-disc-track-path", Path: "Radiohead/OK Computer/CD1/01 - Airbag.flac",
		Candidates: []sdk.Candidate{{ID: "airbag", Title: "Airbag"}}, WantID: "airbag",
	}}}
}

func extendedTagsFamily() sdktest.MatchFamily {
	return sdktest.MatchFamily{Name: "extended-standard-tags", Cases: []sdktest.MatchCase{{
		Name: "tagged-title-with-remaining-fields", Path: "unknown.flac", Probe: sdk.ParseProbe{Tags: map[string]string{
			"TITLE": "Airbag", "TCOM": "Radiohead", "USLT": "lyrics", "TBPM": "125",
		}}, Candidates: []sdk.Candidate{{ID: "airbag", Title: "Airbag"}}, WantID: "airbag",
	}}}
}

func siblingLRCFamily(path string) sdktest.MatchFamily {
	return sdktest.MatchFamily{Name: "sibling-lrc", Cases: []sdktest.MatchCase{{
		Name: "sidecar-lyrics-preserve-title-match", Path: path,
		Candidates: []sdk.Candidate{{ID: "airbag", Title: "Airbag"}}, WantID: "airbag",
	}}}
}

func compilationTagFamily() sdktest.MatchFamily {
	candidates := []sdk.Candidate{{ID: "track", Title: "Track"}}
	return sdktest.MatchFamily{Name: "compilation-tag-grouping", Cases: []sdktest.MatchCase{
		{
			Name: "compilation-flag-normalizes-album-artist", Path: "Compilations/Shared/01 - Track.flac",
			Probe: sdk.ParseProbe{Tags: map[string]string{
				"TITLE": "Track", "ALBUM": "Shared", "ARTIST": "Artist One", "COMPILATION": "1",
			}},
			Candidates: candidates, WantID: "track",
		},
		{
			Name: "various-artists-implies-compilation", Path: "Compilations/Shared/02 - Track.flac",
			Probe: sdk.ParseProbe{Tags: map[string]string{
				"TITLE": "Track", "ALBUM": "Shared", "ARTIST": "Artist Two", "ALBUMARTIST": "Various Artists",
			}},
			Candidates: candidates, WantID: "track",
		},
	}}
}

func mbidGroupingFamily() sdktest.MatchFamily {
	return sdktest.MatchFamily{Name: "mbid-first-grouping-input", Cases: []sdktest.MatchCase{{
		Name: "release-and-artist-mbids-preserve-title-match", Path: "01.flac", Probe: sdk.ParseProbe{Tags: map[string]string{
			"TITLE": "Airbag", "MUSICBRAINZ_ALBUMID": "release-1", "MUSICBRAINZ_ARTISTID": "artist-1",
		}}, Candidates: []sdk.Candidate{{ID: "airbag", Title: "Airbag"}}, WantID: "airbag",
	}}}
}

// tagPoorRipFamily covers the dominant tag-poor rip conventions — a bare
// leading track ordinal ("07 - Title") and a disc-track ordinal ("1-05
// Title") — resolving on the filename title alone with no embedded tags. A
// bare-ordinal file with no recoverable title text (e.g. "07.flac") must fail
// closed to unmatched rather than guess from a digit residue.
func tagPoorRipFamily() sdktest.MatchFamily {
	candidates := []sdk.Candidate{
		{ID: "midnight-drive", Title: "Midnight Drive"},
		{ID: "unrelated", Title: "Sunset Boulevard"},
	}
	return sdktest.MatchFamily{
		Name: "tag-poor-rip",
		Cases: []sdktest.MatchCase{
			{Name: "bare-track-ordinal", Path: "07 - Midnight Drive.flac", Candidates: candidates, WantID: "midnight-drive"},
			{Name: "disc-track-ordinal", Path: "1-05 Midnight Drive.flac", Candidates: candidates, WantID: "midnight-drive"},
			{Name: "digits-only-no-title", Path: "07.flac", Candidates: candidates, WantID: ""},
		},
	}
}

// classicalWorkMovementFamily covers "Composer - Work - Movement" filenames
// (the parser has no
// dedicated Work/Movement structure — it resolves on the trailing movement
// text as a plain title). A movement title shared verbatim across two
// candidate movements (e.g. two symphonies both with an "Allegro") is a
// genuine ambiguity the corpus pins as unmatched, not a coin-flip guess.
func classicalWorkMovementFamily() sdktest.MatchFamily {
	return sdktest.MatchFamily{
		Name: "classical-work-movement",
		Cases: []sdktest.MatchCase{
			{
				Name: "composer-work-movement-resolves",
				Path: "Beethoven - Symphony No. 5 - II. Andante con moto.flac",
				Candidates: []sdk.Candidate{
					{ID: "sym5-andante", Title: "II. Andante con moto"},
					{ID: "sym5-allegro", Title: "I. Allegro con brio"},
				},
				WantID: "sym5-andante",
			},
			{
				Name: "shared-movement-name-is-unmatched",
				Path: "Beethoven - Symphony No. 5 - Allegro.flac",
				Candidates: []sdk.Candidate{
					{ID: "sym5-allegro", Title: "Allegro"},
					{ID: "sym9-allegro", Title: "Allegro"},
				},
				WantID: "",
			},
		},
	}
}

// variousArtistsCompilationFamily covers "Various Artists - NN - Artist -
// Title" compilation rips: the parser's last-" - "-segment rule carries the
// track title through the extra Various-Artists/Artist fields. A track title
// that two different artists' recordings share (a compilation staple, e.g.
// covers or re-releases) is an ambiguity the corpus pins as unmatched.
func variousArtistsCompilationFamily() sdktest.MatchFamily {
	return sdktest.MatchFamily{
		Name: "various-artists-compilation",
		Cases: []sdktest.MatchCase{
			{
				Name: "compilation-track-resolves",
				Path: "Various Artists - 03 - Owl City - Fireflies.mp3",
				Candidates: []sdk.Candidate{
					{ID: "fireflies-owlcity", Title: "Fireflies"},
					{ID: "other", Title: "Wildflower"},
				},
				WantID: "fireflies-owlcity",
			},
			{
				Name: "shared-track-title-is-unmatched",
				Path: "Various Artists - 03 - Artist Name - Fireflies.mp3",
				Candidates: []sdk.Candidate{
					{ID: "fireflies-a", Title: "Fireflies"},
					{ID: "fireflies-b", Title: "Fireflies"},
				},
				WantID: "",
			},
		},
	}
}

// nonLatinMetadataFamily covers CJK and Cyrillic titles in tag-poor rip
// filenames: title normalization is Unicode-letter-aware, so exact non-Latin
// titles resolve like any other script, and an unrelated non-Latin candidate
// does not.
func nonLatinMetadataFamily() sdktest.MatchFamily {
	return sdktest.MatchFamily{
		Name: "non-latin-metadata",
		Cases: []sdktest.MatchCase{
			{
				Name: "japanese-title-track-ordinal",
				Path: "07 - 環世界.flac",
				Candidates: []sdk.Candidate{
					{ID: "kanseikai", Title: "環世界"},
					{ID: "other-jp", Title: "世界の環"},
				},
				WantID: "kanseikai",
			},
			{
				Name: "cyrillic-title-disc-track-ordinal",
				Path: "1-05 Песня О Друге.mp3",
				Candidates: []sdk.Candidate{
					{ID: "pesnya-o-druge", Title: "Песня О Друге"},
					{ID: "other-ru", Title: "Дорога Домой"},
				},
				WantID: "pesnya-o-druge",
			},
			{
				Name: "no-matching-non-latin-candidate-is-unmatched",
				Path: "07 - 環世界.flac",
				Candidates: []sdk.Candidate{
					{ID: "other-jp", Title: "世界の環"},
				},
				WantID: "",
			},
		},
	}
}

// popularityTiebreakFamily covers Candidate.Popularity: the SDK harness's pickMatch only lets popularity break an *exact*
// score tie among candidates that already clear the floor, and only when the
// popularity signal itself is unique — it never promotes a lower-scoring or
// below-floor candidate. Two same-title-and-year candidates (a duplicate
// catalog entry) are the exact tie; a containment-only candidate is the
// lower-score control; a partial-overlap candidate below DefaultMatchThreshold
// (0.5) is the below-floor control.
func popularityTiebreakFamily() sdktest.MatchFamily {
	const path = "07 - Waking Hour 2015.flac"
	return sdktest.MatchFamily{
		Name: "popularity-tiebreak",
		Cases: []sdktest.MatchCase{
			// Exact score tie (same title, same year): the unique popularity
			// signal picks the canonical duplicate.
			{
				Name: "exact-tie-unique-popularity-wins",
				Path: path,
				Candidates: []sdk.Candidate{
					{ID: "waking-hour-dup", Title: "Waking Hour", Year: 2015, Popularity: 5},
					{ID: "waking-hour", Title: "Waking Hour", Year: 2015, Popularity: 250},
				},
				WantID: "waking-hour",
			},
			// Exact score tie with equal popularity: popularity never guesses,
			// so the genuine ambiguity still routes to unmatched.
			{
				Name: "exact-tie-equal-popularity-is-unmatched",
				Path: path,
				Candidates: []sdk.Candidate{
					{ID: "waking-hour-a", Title: "Waking Hour", Year: 2015, Popularity: 40},
					{ID: "waking-hour-b", Title: "Waking Hour", Year: 2015, Popularity: 40},
				},
				WantID: "",
			},
			// A lower-scoring candidate (containment title match, same year)
			// cannot out-rank the exact match no matter how much more popular
			// it is — popularity only breaks ties, it never promotes a score.
			{
				Name: "higher-popularity-lower-score-does-not-win",
				Path: path,
				Candidates: []sdk.Candidate{
					{ID: "waking-hour-exact", Title: "Waking Hour", Year: 2015, Popularity: 1},
					{ID: "waking-hour-live", Title: "Waking Hour (Live)", Year: 2015, Popularity: 999999},
				},
				WantID: "waking-hour-exact",
			},
			// A candidate scoring below the resolution floor stays unmatched
			// even with an enormous popularity value — popularity is never
			// eligibility, only a tiebreaker among candidates that already
			// clear the floor on their own merits.
			{
				Name: "below-floor-popularity-does-not-rescue",
				Path: path,
				Candidates: []sdk.Candidate{
					{ID: "waking-nightmare", Title: "Waking Nightmare", Popularity: 1000000},
				},
				WantID: "",
			},
		},
	}
}
