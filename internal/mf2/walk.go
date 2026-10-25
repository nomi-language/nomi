package mf2

// Functions are every function annotation m's declarations, selectors and
// placeholders write, in text order: what a build reads to know which of a
// program's locale data its messages reach.
func Functions(m *Message) []*Function {
	if m == nil {
		return nil
	}
	var out []*Function
	add := func(e *Expression) {
		if e != nil && e.Function != nil {
			out = append(out, e.Function)
		}
	}
	pattern := func(p Pattern) {
		for _, part := range p.Parts {
			add(part.Expr)
		}
	}
	for i := range m.Declarations {
		add(&m.Declarations[i].Value)
	}
	pattern(m.Pattern)
	for _, v := range m.Variants {
		pattern(v.Pattern)
	}
	return out
}
