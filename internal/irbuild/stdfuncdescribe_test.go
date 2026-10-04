package irbuild

import (
	"strings"
)

// describeStdFunc renders a stdlib candidate for a failure message: its module,
// whether it has a Nomi body, its signature, and what the builder lowers it to
// or why it refused.
func describeStdFunc(f *stdFunc) string {
	body := "host fn"
	if f.decl != nil && f.decl.Body != nil {
		body = "nomi body"
	}
	state := "refused: " + f.why
	switch {
	case f.rtCall != "":
		state = "lowers to " + f.rtCall
	case f.body:
		state = "lowers to its body"
	}
	params := make([]string, 0, len(f.params))
	for _, p := range f.params {
		params = append(params, p.nomi())
	}
	return "std/" + f.module + " " + body + "(" + strings.Join(params, ", ") + ") -> " +
		f.result.nomi() + "; " + state
}
