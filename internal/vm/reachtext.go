package vm

import (
	"fmt"
	"strings"
)

// ReachText says how the reach walk reached an outside call, in words for
// the program's author: from the root chain starts at, each step with the
// kind of edge that took it, and, when the code at the call uses callee as
// a value (edge), that this counts whether or not it is called. vmhost's
// refusals and vmrunner's say it the same way (internal/capability).
func ReachText(chain []ChainStep, edge EdgeKind, callee string) string {
	var b strings.Builder
	for i, s := range chain {
		if i == 0 {
			b.WriteString("reached from " + chainRoot(s))
			continue
		}
		b.WriteString("; " + chainEdge(chain[i-1].Func, s))
	}
	if len(chain) > 0 && edge == EdgeValue {
		b.WriteString(fmt.Sprintf("; `%s` uses `%s` as a value here, which counts whether or not it is called", chain[len(chain)-1].Func, callee))
	}
	return b.String()
}

// chainRoot names the function a chain starts from.
func chainRoot(s ChainStep) string {
	switch s.Edge {
	case EdgeTest:
		return fmt.Sprintf("test %q", s.Func)
	case EdgeBoot:
		return fmt.Sprintf("the boot `%s`", s.Func)
	}
	return "`" + s.Func + "`"
}

// chainEdge says how the walk got from the function from to s: why s
// counts as reached.
func chainEdge(from string, s ChainStep) string {
	typ, method := s.Func, s.Func
	if i := strings.LastIndex(s.Func, "."); i > 0 {
		typ, method = s.Func[:i], s.Func[i+1:]
	}
	switch s.Edge {
	case EdgeCall:
		return fmt.Sprintf("`%s` calls `%s`", from, s.Func)
	case EdgeValue:
		if s.Func == "lambda" {
			return fmt.Sprintf("`%s` makes a lambda, which counts whether or not it is called", from)
		}
		return fmt.Sprintf("`%s` uses `%s` as a value, which counts whether or not it is called", from, s.Func)
	case EdgeDispatch:
		return fmt.Sprintf("`%s` calls `%s` through an interface, and any implementation of it counts, including `%s`", from, method, s.Func)
	case EdgeDisplay:
		return fmt.Sprintf("`%s` displays a value, and any value may be displayed, so `impl Display for %s` counts", from, typ)
	case EdgeDebug:
		return fmt.Sprintf("`%s` inspects a value, and any value may be inspected, so `impl Debug for %s` counts", from, typ)
	case EdgeKeys:
		return fmt.Sprintf("`%s` compares or hashes values, and any type's may be, so `%s` counts", from, s.Func)
	case EdgeOnce:
		return fmt.Sprintf("`%s` reads `%s`, so its initializer counts", from, s.Func)
	case EdgeBoot:
		return fmt.Sprintf("the boot `%s` runs first", s.Func)
	}
	return fmt.Sprintf("`%s` reaches `%s`", from, s.Func)
}
