package vm

import (
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
)

// The currencies a program formats money in, and whether it can write a
// currency's name.
//
// A `Currency` is made by std/currency's `iso` or `custom`, and nothing
// else can make one: the type is opaque. Its backtick literal's handler
// (`from_fragments`) calls `parse`, which calls `iso` for an ISO code, with
// the literal's text when the program loads. So the ISO currencies a
// program has are the texts of its currency literals, and they are all
// known when no other code calls `parse` or `iso`: then a built image
// carries the symbols of those currencies alone
// (cldrdata.Selection.Currencies). std/money's `Money` literal handler
// calls `parse` with the text after the amount, so a ``Money`5 USD` ``
// literal names its currency as ``Currency`USD` `` does. A custom currency
// has no CLDR symbol or name, and every display writes its code.
//
// `Money.format` writes a currency's name only for `CurrencyDisplay.Name`,
// which, like any enum value, exists only where an instruction builds it.
// A program that builds no `Name` and calls std/money_format's `format`
// only from std/money, or with a constant display that is not a name,
// carries no currency names.

// The modules, by the names a built image gives them.
const (
	CurrencyModule    = "std/currency.nomi"
	MoneyModule       = "std/money.nomi"
	MoneyFormatModule = "std/money_format.nomi"
)

func stdModule(name, module string) bool {
	return name == module || strings.HasSuffix(name, "/"+module)
}

// displayName is std/money_format's `format` display that writes the name.
const displayName = 3

// CurrencyLiterals are the ISO codes of the currency literals funcs build,
// each once, sorted, and dynamic when the program may make an ISO currency
// some other way: a call of `Currency.parse` outside its literal handler
// and `Money`'s,
// of the private `Currency.iso` outside `parse` (std/currency's own), or a
// literal with an interpolated part.
func CurrencyLiterals(mods []*ir.Module, funcs []*ir.Func) (codes []string, dynamic bool) {
	var currency, money []*ir.Func
	for _, m := range mods {
		switch {
		case m == nil:
		case stdModule(m.Name(), CurrencyModule):
			currency = append(currency, m.Funcs()...)
		case stdModule(m.Name(), MoneyModule):
			money = append(money, m.Funcs()...)
		}
	}
	isos, parses, handlers := map[*ir.Symbol]bool{}, map[*ir.Symbol]bool{}, map[*ir.Symbol]bool{}
	for _, f := range currency {
		if f.Name() == "iso" && f.Sym() != nil {
			isos[f.Sym()] = true
		}
	}
	for _, f := range currency {
		if f.Name() == "parse" && f.Sym() != nil && calls(f, isos) {
			parses[f.Sym()] = true
		}
	}
	for _, f := range currency {
		if f.Name() == "from_fragments" && f.Sym() != nil && calls(f, parses) {
			handlers[f.Sym()] = true
		}
	}
	// Money's handler, lowered with the program and so named by its
	// type: its text is the amount, a space and the currency.
	moneyHandlers := map[*ir.Symbol]bool{}
	for _, f := range money {
		if f.Name() == "Money.from_fragments" && f.Sym() != nil && calls(f, parses) {
			moneyHandlers[f.Sym()] = true
		}
	}
	if len(isos) == 0 {
		return nil, false
	}
	walkFuncs(funcs, func(f *ir.Func) {
		in := func(set map[*ir.Symbol]bool) bool { return f.Sym() != nil && set[f.Sym()] }
		eachInstr(f, func(i ir.Instr) {
			if v, ok := i.(*ir.FuncValue); ok && v.Body() != nil && v.Body().Sym() != nil {
				// A function value of iso or parse calls it with any text.
				if s := v.Body().Sym(); isos[s] || parses[s] {
					dynamic = true
				}
			}
			c, ok := i.(*ir.Call)
			if !ok || c.Callee() == nil {
				return
			}
			switch {
			case isos[c.Callee()] && !in(parses):
				dynamic = true
			case parses[c.Callee()] && !in(handlers) && !in(moneyHandlers):
				dynamic = true
			case handlers[c.Callee()] && c.NumArgs() == 1:
				text, static := literalText(f, c.Arg(0))
				if !static {
					dynamic = true
					return
				}
				if !strings.Contains(text, ":") {
					codes = append(codes, text)
				}
			case moneyHandlers[c.Callee()] && c.NumArgs() == 1:
				text, static := literalText(f, c.Arg(0))
				if !static {
					dynamic = true
					return
				}
				if _, code, ok := strings.Cut(text, " "); ok && !strings.Contains(code, ":") {
					codes = append(codes, code)
				}
			}
		})
	})
	slices.Sort(codes)
	return slices.Compact(codes), dynamic
}

// CurrencyNames reports whether funcs may write a currency's name with
// `Money.format`: they build a `CurrencyDisplay.Name`, or call std/
// money_format's `format` from outside std/money with a display that is not
// a constant other than the name's.
func CurrencyNames(mods []*ir.Module, funcs []*ir.Func) bool {
	formats := map[*ir.Symbol]bool{}
	money := map[*ir.Func]bool{}
	for _, m := range mods {
		switch {
		case m == nil:
		case stdModule(m.Name(), MoneyFormatModule):
			for _, f := range m.Funcs() {
				if f.Name() == "format" && f.Sym() != nil {
					formats[f.Sym()] = true
				}
			}
		case stdModule(m.Name(), MoneyModule):
			for _, f := range m.Funcs() {
				money[f] = true
			}
		}
	}
	names := false
	walkFuncs(funcs, func(f *ir.Func) {
		eachInstr(f, func(i ir.Instr) {
			switch n := i.(type) {
			case *ir.Make:
				// Matched by the printed name: a program's own enum of that
				// name and variant only makes the image carry names it does
				// not need.
				if n.Kind() == ir.MakeVariant && n.Variant() == "Name" && displayEnum(n.Typ().Name()) {
					names = true
				}
			case *ir.FuncValue:
				if n.Body() != nil && n.Body().Sym() != nil && formats[n.Body().Sym()] {
					names = true
				}
			case *ir.Call:
				if n.Callee() == nil || !formats[n.Callee()] || money[f] {
					return
				}
				if n.NumArgs() != 4 {
					names = true
					return
				}
				k, ok := f.Def(n.Arg(3)).(*ir.Const)
				if !ok || k.Kind() != ir.ConstInt || k.Int() == displayName {
					names = true
				}
			}
		})
	})
	return names
}

func displayEnum(name string) bool {
	return name == "CurrencyDisplay" || strings.HasSuffix(name, ".CurrencyDisplay")
}

// calls reports whether f calls a function of set directly.
func calls(f *ir.Func, set map[*ir.Symbol]bool) bool {
	found := false
	eachInstr(f, func(i ir.Instr) {
		if c, ok := i.(*ir.Call); ok && c.Callee() != nil && set[c.Callee()] {
			found = true
		}
	})
	return found
}

func eachInstr(f *ir.Func, visit func(ir.Instr)) {
	for _, b := range f.Blocks() {
		for _, in := range b.Instrs() {
			visit(in)
			if d, ok := in.(*ir.Defer); ok && d.Call() != nil {
				visit(d.Call())
			}
		}
	}
}

// walkFuncs visits funcs and every function value body they build, each
// once.
func walkFuncs(funcs []*ir.Func, visit func(*ir.Func)) {
	seen := map[*ir.Func]bool{}
	var walk func(f *ir.Func)
	walk = func(f *ir.Func) {
		if f == nil || seen[f] {
			return
		}
		seen[f] = true
		visit(f)
		eachInstr(f, func(i ir.Instr) {
			if v, ok := i.(*ir.FuncValue); ok {
				walk(v.Body())
			}
		})
	}
	for _, f := range funcs {
		walk(f)
	}
}
