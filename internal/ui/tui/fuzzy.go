package tui

import (
	"unicode"
	"unicode/utf8"
)

// fuzzyScore matches pattern against s as a case-insensitive subsequence,
// like a file finder: "tlin" matches "torvalds/linux". Higher is better.
// Consecutive letters and letters at the start of a word (after / - _ .
// or a space) score extra, and gaps cost a little, so "linux" ranks
// "torvalds/linux" above "lib/inux-tools".
func fuzzyScore(pattern, s string) (int, bool) {
	if pattern == "" {
		return 0, true
	}
	// Matching is greedy, so try every place the first letter occurs and
	// keep the best: "linux" in "torvalds/linux" should not settle for the
	// l in "torvalds".
	pr := []rune(pattern)
	first := unicode.ToLower(pr[0])
	best, found := 0, false
	prev := '/'
	for i, r := range s {
		if unicode.ToLower(r) == first {
			if sc, ok := fuzzyFrom(pr, s, i, prev); ok && (!found || sc > best) {
				best, found = sc, true
			}
		}
		prev = r
	}
	if !found {
		return 0, false
	}
	// Shorter haystacks win ties: an exact name beats a long path.
	return best*4 - utf8.RuneCountInString(s)/8, true
}

// fuzzyFrom matches pr greedily in s starting at byte offset start; prev
// is the rune before start.
func fuzzyFrom(pr []rune, s string, start int, prev rune) (int, bool) {
	score, pi, prevMatch, gap := 0, 0, false, 0
	for i, r := range s[start:] {
		i += start
		if pi == len(pr) {
			break
		}
		if unicode.ToLower(r) == unicode.ToLower(pr[pi]) {
			score += 10
			switch {
			case prevMatch:
				score += 15
			case i == 0 || isBoundary(prev):
				score += 12
			}
			score -= min(gap, 5)
			pi++
			prevMatch, gap = true, 0
		} else {
			prevMatch = false
			if pi > 0 {
				gap++
			}
		}
		prev = r
	}
	return score, pi == len(pr)
}

func isBoundary(r rune) bool {
	return r == '/' || r == '-' || r == '_' || r == '.' || r == ' '
}
