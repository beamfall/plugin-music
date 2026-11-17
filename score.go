package music

import (
	"strings"
	"unicode"

	sdk "github.com/beamfall/plugin-sdk"
)

// AutoApplyThreshold is the Music plugin's match-confidence cut for unattended
// identification (mirrored from the Film & TV plugin): a candidate
// scoring at or above it auto-applies, below it queues for manual review. 0.75
// demands a strong title match plus a corroborating year (or an exact title) —
// conservative, because a wrong auto-apply silently mislabels a track.
const AutoApplyThreshold = 0.75

// noCorroborationPenalty is subtracted from a non-exact title score when the
// filename carried no year to corroborate it. It is sized so the strongest
// non-exact title signal — a containment match (0.85) — lands at 0.60: still
// above the corpus resolution floor (sdktest.DefaultMatchThreshold, 0.5) so
// the candidate can still rank and resolve, but below AutoApplyThreshold
// (0.75) so it will not auto-apply without a human. An exact title (1.0) is
// exempt: a fully-specified unique title is safe to auto-apply alone.
const noCorroborationPenalty = 0.25

// containmentScore is the title similarity for one normalized title inside
// the other.
const containmentScore = 0.85

// letterlessScale multiplies the score of a parsed title with no letters (a
// bare number such as "03", "00" or "1999", whether a stem fallback, an
// ordinal mistaken for a title or a numeric title). Such a title is not
// distinctive (CORE-2313): many recordings share it, so it never auto-applies
// whatever the year — the best case, an exact title with an exact year, lands
// at 0.70, below AutoApplyThreshold, while ranking is preserved.
const letterlessScale = 0.7

// reissueYearScore is the year credit when the parsed year is later than the
// candidate's by more than one: the tag may carry a reissue's DATE while the
// candidate is the original recording's first release. It ranks the original
// above a conflicting later candidate (0.745 with an exact title) but stays
// below AutoApplyThreshold, since a reissue date cannot confirm the match.
const reissueYearScore = 0.15

// Scorer implements sdk.MatchScorer for audio Media:
// how confidently one MusicBrainz release/recording Candidate matches a file's
// parsed guess. It is the match-confidence half of the plugin's two compiled
// hooks, sibling to Parser. Title similarity dominates; the release year
// corroborates. Non-Latin titles score like any other script — normalization
// is Unicode-letter-aware.
type Scorer struct{}

// NewScorer returns a ready Scorer.
func NewScorer() *Scorer { return &Scorer{} }

// Score weighs one candidate (its title and year) against the parsed guess and
// returns a confidence in [0,1]:
//
//   - Title similarity (weight 0.7) dominates — an exact normalized match scores
//     1.0, a containment (one title inside the other) 0.85, otherwise a
//     token-overlap (Jaccard) ratio. A blank parsed or candidate title is a
//     hard zero — nothing to match on.
//   - Year agreement (weight 0.3) corroborates: exact year adds the full weight,
//     ±1 (release-vs-original-date drift) adds half, a conflicting year adds
//     nothing.
//
// When the parse carried no year there is nothing to corroborate the title,
// so the fail-closed asymmetry ("wrong auto-apply is worse than no
// match") applies: an *exact* title still scores 1.0 (safe to auto-apply
// alone), but a *non-exact* title is docked noCorroborationPenalty so it
// cannot clear AutoApplyThreshold on title similarity alone — a mediocre
// single candidate with no year disambiguation routes to manual review rather
// than auto-applying. A title with no letters is scaled by letterlessScale so
// it never auto-applies.
func (Scorer) Score(candidate sdk.Candidate, parsed sdk.ParsedMedia) float64 {
	title := titleScore(parsed.Title, candidate.Title)
	if title == 0 {
		return 0
	}
	score := noYearScore(title)
	if parsed.Year != 0 {
		const titleWeight, yearWeight = 0.7, 0.3
		score = titleWeight*title + yearWeight*yearScore(parsed.Year, candidate.Year)
	}
	if !hasLetter(parsed.Title) {
		return letterlessScale * score
	}
	return score
}

// noYearScore is the fail-closed verdict when the filename carried no year: an
// exact title (1.0) passes through — a unique title needs no corroboration —
// while any weaker title is docked noCorroborationPenalty (floored at 0) so it
// falls below AutoApplyThreshold and defers to manual review.
func noYearScore(title float64) float64 {
	if title == 1 {
		return 1
	}
	if docked := title - noCorroborationPenalty; docked > 0 {
		return docked
	}
	return 0
}

// titleScore is the normalized title similarity in [0,1]: exact match 1.0,
// containment 0.85, else Jaccard token overlap. Either title blank yields 0.
func titleScore(parsedTitle, candTitle string) float64 {
	a := normalizeTitle(parsedTitle)
	b := normalizeTitle(candTitle)
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return containmentScore
	}
	return jaccard(strings.Fields(a), strings.Fields(b))
}

// yearScore corroborates the title: exact 1.0, ±1 year (release-vs-original
// date drift) 0.5, a parsed year later than the candidate's (a possible
// reissue date) reissueYearScore, and an earlier parsed year (the candidate
// is a later cover or re-recording) 0. A zero candidate year (none parsed) is a
// non-signal, not a conflict — 0.5 so a strong title still clears.
func yearScore(parsedYear, candYear int) float64 {
	if candYear == 0 {
		return 0.5
	}
	switch diff := parsedYear - candYear; {
	case diff == 0:
		return 1
	case diff == 1 || diff == -1:
		return 0.5
	case diff > 1:
		return reissueYearScore
	default:
		return 0
	}
}

// normalizeTitle lower-cases and strips punctuation to spaces, then collapses
// whitespace — so "Symphony No. 5!" and "symphony no 5" compare equal. Unicode
// letters and digits (incl. CJK / Hangul) are preserved.
func normalizeTitle(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// jaccard is the token-set overlap ratio |A∩B| / |A∪B| in [0,1].
func jaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]bool, len(a))
	for _, t := range a {
		set[t] = true
	}
	union := make(map[string]bool, len(a)+len(b))
	for _, t := range a {
		union[t] = true
	}
	intersect := 0
	for _, t := range b {
		if set[t] {
			intersect++
			delete(set, t) // count each shared token once
		}
		union[t] = true
	}
	return float64(intersect) / float64(len(union))
}
