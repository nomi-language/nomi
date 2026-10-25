package vm

import (
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
)

// The skeletons a program formats dates with.
//
// A `DateFields`, `TimeFields` or `DateTimeFields` value is made by its
// backtick literal, whose handler (`from_fragments`) std/calendar calls
// with the literal's text when the program loads, or by `parse` while it
// runs. Both reach the crossing `calendar.fields_check`. So the skeletons
// a program has are the texts of the literals whose handler is one of
// those, and they are all known when no other code calls `parse`: then a
// built image carries each listed locale's pattern for each skeleton,
// resolved, and no available formats (cldrdata.Selection.Skeletons).

// CalendarModule is the name a built image gives std/calendar's module.
const CalendarModule = "std/calendar.nomi"

// stdCalendarModule reports whether a module, by its name, is
// std/calendar, as StdMessageModule reports std/message.
func stdCalendarModule(name string) bool {
	return name == CalendarModule || strings.HasSuffix(name, "/"+CalendarModule)
}

// fieldsCheck is the crossing a skeleton's parse reaches.
const fieldsCheck = "calendar.fields_check"

// SkeletonLiterals are the texts of the skeleton literals funcs build, each
// once, sorted, and dynamic when the program may make a skeleton some
// other way: a call of a skeleton type's `parse` outside its literal
// handler, or a literal with an interpolated part.
func SkeletonLiterals(mods []*ir.Module, funcs []*ir.Func) (texts []string, dynamic bool) {
	parses, handlers := map[*ir.Symbol]bool{}, map[*ir.Symbol]bool{}
	var calendar []*ir.Func
	for _, m := range mods {
		if m != nil && stdCalendarModule(m.Name()) {
			calendar = append(calendar, m.Funcs()...)
		}
	}
	calls := func(f *ir.Func, want func(*ir.Symbol) bool) bool {
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				if c, ok := in.(*ir.Call); ok && c.Callee() != nil && want(c.Callee()) {
					return true
				}
			}
		}
		return false
	}
	for _, f := range calendar {
		if f.Name() == "parse" && f.Sym() != nil && calls(f, func(s *ir.Symbol) bool { return s.Name() == fieldsCheck }) {
			parses[f.Sym()] = true
		}
	}
	for _, f := range calendar {
		if f.Name() == "from_fragments" && f.Sym() != nil && calls(f, func(s *ir.Symbol) bool { return parses[s] }) {
			handlers[f.Sym()] = true
		}
	}
	if len(parses) == 0 {
		return nil, false
	}
	for _, f := range funcs {
		handler := f.Sym() != nil && handlers[f.Sym()]
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				c, ok := in.(*ir.Call)
				if !ok || c.Callee() == nil {
					continue
				}
				switch {
				case parses[c.Callee()] && !handler:
					dynamic = true
				case handlers[c.Callee()] && c.NumArgs() == 1:
					text, static := literalText(f, c.Arg(0))
					if !static {
						dynamic = true
						continue
					}
					texts = append(texts, text)
				}
			}
		}
	}
	slices.Sort(texts)
	return slices.Compact(texts), dynamic
}

// literalText is the text of a typed literal's fragment list, the list
// temp t of f holds, when every fragment is a `Static` string constant.
func literalText(f *ir.Func, t ir.Temp) (string, bool) {
	list, ok := f.Def(t).(*ir.Make)
	if !ok || list.Kind() != ir.MakeList {
		return "", false
	}
	var b strings.Builder
	for i := range list.Arity() {
		v, ok := f.Def(list.Operand(i)).(*ir.Make)
		if !ok || v.Kind() != ir.MakeVariant || v.Variant() != "Static" || v.Arity() != 1 {
			return "", false
		}
		k, ok := f.Def(v.Operand(0)).(*ir.Const)
		if !ok || k.Kind() != ir.ConstString {
			return "", false
		}
		b.WriteString(k.Text())
	}
	return b.String(), true
}
