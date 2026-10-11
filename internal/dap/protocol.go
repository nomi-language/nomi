package dap

// The parts of the Debug Adapter Protocol that github.com/google/go-dap
// v0.12.0 (its latest release) does not have, defined here over its types:
// a variable's `declarationLocationReference` and the `locations` request
// that answers it (DAP 1.66). The server decodes requests with codec, which
// is go-dap's with `locations` registered.

import (
	godap "github.com/google/go-dap"

	"github.com/nomi-language/nomi/internal/ir"
)

// codec decodes the client's messages: go-dap's, and `locations`.
var codec = func() *godap.Codec {
	c := godap.NewCodec()
	if err := c.RegisterRequest("locations",
		func() godap.Message { return &locationsRequest{} },
		func() godap.Message { return &locationsResponse{} }); err != nil {
		panic(err)
	}
	return c
}()

// variable is a DAP Variable with its declaration's location: a reference
// the `locations` request resolves to where the variable is bound.
type variable struct {
	godap.Variable
	DeclarationLocationReference int `json:"declarationLocationReference,omitempty"`
}

type variablesResponse struct {
	godap.Response
	Body variablesResponseBody `json:"body"`
}

type variablesResponseBody struct {
	Variables []variable `json:"variables"`
}

type locationsRequest struct {
	godap.Request
	Arguments locationsArguments `json:"arguments"`
}

type locationsArguments struct {
	LocationReference int `json:"locationReference"`
}

type locationsResponse struct {
	godap.Response
	Body locationsResponseBody `json:"body"`
}

type locationsResponseBody struct {
	Source    *godap.Source `json:"source"`
	Line      int           `json:"line"`
	Column    int           `json:"column,omitempty"`
	EndLine   int           `json:"endLine,omitempty"`
	EndColumn int           `json:"endColumn,omitempty"`
}

// locationRef is the location reference of pos, handed out once per
// position for the session: a variable's declaration does not move while
// the program runs, so a reference stays good across resumes. Zero for a
// position the program's source does not have. sess.mu is held.
func (sess *session) locationRef(pos ir.Pos) int {
	if !pos.IsValid() || pos.Synthesized() || pos.File() == "" {
		return 0
	}
	if ref, has := sess.locIndex[pos]; has {
		return ref
	}
	sess.locs = append(sess.locs, pos)
	sess.locIndex[pos] = len(sess.locs)
	return len(sess.locs)
}

// locations answers a `locations` request: the source position a location
// reference names.
func (sess *session) locations(r *locationsRequest) {
	sess.mu.Lock()
	ref := r.Arguments.LocationReference
	var pos ir.Pos
	if ref >= 1 && ref <= len(sess.locs) {
		pos = sess.locs[ref-1]
	}
	sess.mu.Unlock()
	if !pos.IsValid() {
		sess.s.fail(&r.Request, "no location %d", ref)
		return
	}
	src, _ := source(pos.File())
	body := locationsResponseBody{Source: src, Line: pos.Line(), Column: pos.Col()}
	if pos.EndLine() > pos.Line() || pos.EndCol() > pos.Col() {
		body.EndLine, body.EndColumn = pos.EndLine(), pos.EndCol()
	}
	sess.s.send(&locationsResponse{Response: response(&r.Request), Body: body})
}

// sendVariables answers a variables request.
func (sess *session) sendVariables(r *godap.VariablesRequest, vars []variable) {
	if vars == nil {
		vars = []variable{}
	}
	sess.s.send(&variablesResponse{Response: response(&r.Request), Body: variablesResponseBody{Variables: vars}})
}
