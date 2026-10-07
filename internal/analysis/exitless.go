package analysis

import "fmt"

// exitlessSite is an expression that no function encloses: a `once`
// initializer, a parameter's default, or a field's default. Each is evaluated
// on its own, outside any function body, so an early exit written directly in
// it (`try`, `return`, `break`, `continue`, an assertion) has nothing to exit.
// A lambda, a nested `fn` or a `concurrent` block inside it is a boundary of
// its own, and an exit there leaves that boundary as it does anywhere.
type exitlessSite struct {
	// what names the expression in the diagnostic: "the initializer of `once
	// n`", "the default of parameter 'x'".
	what string
	// why says what is missing: "a `once` has no function to return from".
	why string
	// hint says what to write instead of a `try`.
	hint string
}

func onceExitless(name string) *exitlessSite {
	return &exitlessSite{
		what: fmt.Sprintf("the initializer of `once %s`", name),
		why:  "a `once` has no function to return from",
		hint: fmt.Sprintf("bind the Result or Maybe and handle it where `%s` is read, or compute the value inside a function", name),
	}
}

func paramDefaultExitless(param string) *exitlessSite {
	return &exitlessSite{
		what: fmt.Sprintf("the default of parameter '%s'", param),
		why:  "a default has no function to return from",
		hint: "handle the Result or Maybe inside the default with `case`, or compute the value inside the function",
	}
}

func fieldDefaultExitless(field, owner string) *exitlessSite {
	return &exitlessSite{
		what: fmt.Sprintf("the default of field '%s' of %s", field, owner),
		why:  "a default has no function to return from",
		hint: "handle the Result or Maybe inside the default with `case`, or compute the value in a function and pass the field",
	}
}

// checkExitless runs check with site as the innermost enclosing expression
// that no function encloses. Every boundary checked inside it (a `fn`, a
// lambda, a `concurrent` block) clears the site for its own body
// (enterBoundaryExits).
func (c *checker) checkExitless(site *exitlessSite, check func() Type) Type {
	prev := c.exitless
	c.exitless = site
	defer func() { c.exitless = prev }()
	return check()
}

// enterBoundaryExits clears the exitless site for a boundary's body and
// returns what restores it.
func (c *checker) enterBoundaryExits() func() {
	prev := c.exitless
	c.exitless = nil
	return func() { c.exitless = prev }
}

// rejectExitlessExit reports an early exit (`kw`) at line:col when no function
// encloses it, and says whether it did. A caller that gets true skips its
// boundary checks: there is no boundary to check against, and the one
// c.returnTy describes belongs to whatever function happened to be checking
// when this expression was reached (a `once` is checked on demand, from its
// first read).
func (c *checker) rejectExitlessExit(kw string, line, col int) bool {
	site := c.exitless
	if site == nil {
		return false
	}
	hint := site.hint
	switch kw {
	case "return", "break", "continue":
		hint = "give each branch of an `if` or `case` its own value instead of exiting"
	case "try":
	default:
		hint = fmt.Sprintf("move the `%s` into a function that returns a Result", kw)
	}
	c.report(TypeError{
		Line:    line,
		Col:     col,
		Message: fmt.Sprintf("`%s` cannot be used in %s: %s", kw, site.what, site.why),
		Hints:   []string{hint},
	})
	return true
}
