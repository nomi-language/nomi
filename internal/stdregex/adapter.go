// Package regex adapts Go's RE2-compatible regexp engine to std/regex.
package stdregex

import (
	"regexp"
	"sync"
)

// Regex is the opaque Go handle held by std/regex.RawRegex.
type Regex struct {
	re *regexp.Regexp
	// suffix is re anchored to the end of the text, built by FFISuffixOf on
	// its first call.
	suffixOnce sync.Once
	suffix     *regexp.Regexp
}

func FFICompile(pattern string) (*Regex, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return &Regex{re: re}, nil
}

func FFIPattern(re *Regex) string {
	if re == nil || re.re == nil {
		return ""
	}
	return re.re.String()
}

func FFIMatch(re *Regex, input string) bool {
	return re != nil && re.re != nil && re.re.MatchString(input)
}

func FFIFind(re *Regex, input string) *string {
	if re == nil || re.re == nil {
		return nil
	}
	match := re.re.FindString(input)
	if match == "" && re.re.FindStringIndex(input) == nil {
		return nil
	}
	return &match
}

func FFIFindAll(re *Regex, input string) []string {
	if re == nil || re.re == nil {
		return nil
	}
	return re.re.FindAllString(input, -1)
}

func FFIReplaceAll(re *Regex, input, replacement string) string {
	if re == nil || re.re == nil {
		return input
	}
	return re.re.ReplaceAllString(input, replacement)
}

func FFISplit(re *Regex, input string) []string {
	if re == nil || re.re == nil {
		return nil
	}
	return re.re.Split(input, -1)
}

// FFIPrefixOf reports whether a match starts at the beginning of input. The
// leftmost match is the one with the smallest start, so a match at 0 exists
// exactly when the leftmost match starts there.
func FFIPrefixOf(re *Regex, input string) bool {
	if re == nil || re.re == nil {
		return false
	}
	loc := re.re.FindStringIndex(input)
	return loc != nil && loc[0] == 0
}

// FFISuffixOf reports whether some match ends at the end of input. The
// leftmost match need not be that one (`a|ab` on "ab" picks "a"), so it asks
// a second regex, the pattern anchored to the end of the text, which is
// compiled on first use and kept on the handle.
func FFISuffixOf(re *Regex, input string) bool {
	if re == nil || re.re == nil {
		return false
	}
	re.suffixOnce.Do(func() {
		re.suffix = regexp.MustCompile(`(?:` + re.re.String() + `)\z`)
	})
	return re.suffix.MatchString(input)
}

// FFIReplaceLiteral replaces every non-overlapping match with replacement,
// taken literally: `$1` is not expanded.
func FFIReplaceLiteral(re *Regex, input, replacement string) string {
	if re == nil || re.re == nil {
		return input
	}
	return re.re.ReplaceAllLiteralString(input, replacement)
}
