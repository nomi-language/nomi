// Package suggest finds the name a misspelled one most likely meant: the
// "did you mean" a diagnostic ends with. The checker's unknown-name errors,
// nomi.toml's keys and effects lists, and a host's effects list in
// a runner that links no compiler all use it, so a name is suggested the
// same way wherever it is misspelled.
//
// The rule is the one rustc and the TypeScript checker use. A candidate is
// close enough when its edit distance to the name, counting an adjacent
// transposition as one edit (optimal string alignment), is at most a third
// of the name's length and at least 1. A candidate that differs only in
// case is the closest of all. Of the close candidates the nearest wins;
// when two are equally near and case does not separate them, nothing is
// suggested, since a guess between two is not a suggestion.
package suggest

import (
	"strings"
	"unicode"
)

// Closest is the one candidate close to name by the rule above, or "".
func Closest(name string, candidates []string) string {
	if name == "" {
		return ""
	}
	limit := max(1, len([]rune(name))/3)
	lower := strings.ToLower(name)
	best, bestDist, bestFold, tied := "", limit+1, 0, false
	seen := map[string]bool{}
	for _, cand := range candidates {
		if cand == name || cand == "" || seen[cand] || !Suggestable(cand) {
			continue
		}
		seen[cand] = true
		fold := EditDistance(lower, strings.ToLower(cand))
		dist := EditDistance(name, cand)
		if fold == 0 {
			// Differs from name only in case.
			dist = 0
		}
		if dist > limit {
			continue
		}
		switch {
		case dist < bestDist || (dist == bestDist && fold < bestFold):
			best, bestDist, bestFold, tied = cand, dist, fold, false
		case dist == bestDist && fold == bestFold:
			tied = true
		}
	}
	if tied {
		return ""
	}
	return best
}

// Suggestable reports whether a name is one a program could have meant,
// a path such as `storage::testing` included: a name the toolchain
// synthesized (`__show`, `$get`) is not.
func Suggestable(name string) bool {
	if strings.HasPrefix(name, "_") {
		return false
	}
	for _, r := range name {
		if r != '_' && r != '.' && r != ':' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// EditDistance is the optimal-string-alignment distance between a and b,
// by rune: insertions, deletions, substitutions and adjacent
// transpositions each cost one.
func EditDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev2 := make([]int, len(rb)+1)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(rb)]
}
