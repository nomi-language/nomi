// Package regex adapts Go's RE2-compatible regexp engine to std/regex.
package stdregex

import "regexp"

// Regex is the opaque Go handle held by std/regex.RawRegex.
type Regex struct {
	re *regexp.Regexp
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
