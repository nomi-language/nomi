package stdstrings

import "github.com/nomi-language/nomi/internal/stdregex"

// FFICaptures is `String.captures`: the capture groups of the leftmost match of
// re in s, group 1 onward, or nil when nothing matches. A group that took
// part in the match is its text, empty or not; one that did not is nil.
func FFICaptures(s string, re *stdregex.Regex) *[]*string {
	rx := re.Regexp()
	if rx == nil {
		return nil
	}
	loc := rx.FindStringSubmatchIndex(s)
	if loc == nil {
		return nil
	}
	groups := groupsAt(s, loc)
	return &groups
}

// FFICapturesAll is `String.captures_all`: FFICaptures for every non-overlapping
// match, left to right, the matches `String.find_all` returns.
func FFICapturesAll(s string, re *stdregex.Regex) [][]*string {
	rx := re.Regexp()
	if rx == nil {
		return nil
	}
	locs := rx.FindAllStringSubmatchIndex(s, -1)
	out := make([][]*string, len(locs))
	for i, loc := range locs {
		out[i] = groupsAt(s, loc)
	}
	return out
}

// groupsAt reads groups 1 onward from a submatch index slice, in which a group
// that did not take part in the match has the bounds -1, -1.
func groupsAt(s string, loc []int) []*string {
	groups := make([]*string, 0, len(loc)/2-1)
	for g := 2; g+1 < len(loc); g += 2 {
		if loc[g] < 0 {
			groups = append(groups, nil)
			continue
		}
		text := s[loc[g]:loc[g+1]]
		groups = append(groups, &text)
	}
	return groups
}
