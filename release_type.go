package music

import (
	"strings"

	sdk "github.com/beamfall/plugin-sdk"
)

// Release types (CORE-2769). An Album records the kind of release it is —
// album, EP, single, live, compilation — so an artist's page can group its
// releases the way listeners scan a discography. The value comes from the
// MusicBrainz release-group type Picard embeds (RELEASETYPE / "MusicBrainz
// Album Type": a primary type plus optional secondary types such as
// "album; live"), else from the compilation flag.

// ReleaseTypeField is the Album field holding the release type.
const ReleaseTypeField = "release-type"

// Release type values: the group keys stored in ReleaseTypeField.
const (
	ReleaseTypeAlbum       = "album"
	ReleaseTypeEP          = "ep"
	ReleaseTypeSingle      = "single"
	ReleaseTypeLive        = "live"
	ReleaseTypeCompilation = "compilation"
	ReleaseTypeSoundtrack  = "soundtrack"
	ReleaseTypeRemix       = "remix"
	ReleaseTypeOther       = "other"
)

// ReleaseTypeGroup is one release type and the heading over its group.
type ReleaseTypeGroup struct {
	Value string
	Label sdk.LocaleString
}

// ReleaseTypeGroups returns the release types in the order an artist's page
// lists them, each with its plural heading.
func ReleaseTypeGroups() []ReleaseTypeGroup {
	return []ReleaseTypeGroup{
		{ReleaseTypeAlbum, sdk.LocaleString{"en": "Albums", "fr": "Albums"}},
		{ReleaseTypeEP, sdk.LocaleString{"en": "EPs", "fr": "EP"}},
		{ReleaseTypeSingle, sdk.LocaleString{"en": "Singles", "fr": "Singles"}},
		{ReleaseTypeLive, sdk.LocaleString{"en": "Live Albums", "fr": "Albums live"}},
		{ReleaseTypeCompilation, sdk.LocaleString{"en": "Compilations", "fr": "Compilations"}},
		{ReleaseTypeSoundtrack, sdk.LocaleString{"en": "Soundtracks", "fr": "Bandes originales"}},
		{ReleaseTypeRemix, sdk.LocaleString{"en": "Remixes", "fr": "Remix"}},
		{ReleaseTypeOther, sdk.LocaleString{"en": "Other Releases", "fr": "Autres parutions"}},
	}
}

// secondaryReleaseTypes are the MusicBrainz secondary types that name their
// own group, in precedence order: a live compilation reads as live.
var secondaryReleaseTypes = []string{ReleaseTypeLive, ReleaseTypeCompilation, ReleaseTypeSoundtrack, ReleaseTypeRemix}

// ParseReleaseType maps a MusicBrainz release-group type tag to a release
// type. The tag lists a primary type (album, single, ep, broadcast, other)
// and secondary types (live, compilation, soundtrack, remix, …), separated by
// ";", "/", "," or spaces in any case. A secondary type with its own group
// wins over the primary type; an unknown or empty tag returns "".
func ParseReleaseType(tag string) string {
	words := strings.FieldsFunc(strings.ToLower(tag), func(r rune) bool {
		return r == ';' || r == '/' || r == ',' || r == '\x00' || r == ' ' || r == '\t'
	})
	if len(words) == 0 {
		return ""
	}
	seen := map[string]bool{}
	for _, w := range words {
		seen[w] = true
	}
	for _, t := range secondaryReleaseTypes {
		if seen[t] {
			return t
		}
	}
	switch {
	case seen[ReleaseTypeAlbum]:
		return ReleaseTypeAlbum
	case seen[ReleaseTypeEP]:
		return ReleaseTypeEP
	case seen[ReleaseTypeSingle]:
		return ReleaseTypeSingle
	case seen["broadcast"], seen[ReleaseTypeOther], seen["dj-mix"], seen["mixtape"],
		seen["spokenword"], seen["interview"], seen["audiobook"], seen["demo"]:
		return ReleaseTypeOther
	}
	return ""
}
