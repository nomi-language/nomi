package vm

import (
	"io"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ir"
)

// The compiled texts of a program's messages and nouns.
//
// A `message` declaration expands into a function that passes its compiled
// texts, as one string constant, to std/message's `compiled`, and a `noun`
// into a `once` that passes its forms to `compiled_noun`
// (internal/analysis's expansions). The tables a program carries are
// therefore the constants those calls take in the functions it carries: a
// built image holds only the functions the program reaches (PruneImage), so
// its tables are the messages and nouns it reaches, and the running
// program checks a catalog loaded at run time against exactly those
// (std/message's `Catalog.load`).

// MessageModule is the name a built image gives std/message's module
// (vmhost's imageName), which a runner reads its tables by.
const MessageModule = "std/message.nomi"

// StdMessageModule reports whether a module, by its name, is std/message:
// the image's name for it, or a path ending in it, as a program lowered in
// process names it. No other module can have such a path, since a file at
// std/ under a project would be shadowed by the stdlib.
func StdMessageModule(name string) bool {
	return name == MessageModule || strings.HasSuffix(name, "/"+MessageModule)
}

// The std/message functions whose second argument is a table.
const (
	compiledMessage = "compiled"
	compiledNoun    = "compiled_noun"
)

// MessageTables are the message and noun tables funcs pass to std/message,
// each once, sorted. stdMessage reports whether a module of mods, by its
// name, is std/message.
func MessageTables(mods []*ir.Module, funcs []*ir.Func, stdMessage func(module string) bool) (messages, nouns []string) {
	ms, ns := MessageTableConsts(mods, funcs, stdMessage)
	return constTexts(ms), constTexts(ns)
}

func constTexts(cs []*ir.Const) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Text())
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// MessageTableConsts are the constants MessageTables reads its tables
// from, for a writer that carries a table in another form (a browser
// build's image, which leaves out the locales it loads from files).
func MessageTableConsts(mods []*ir.Module, funcs []*ir.Func, stdMessage func(module string) bool) (messages, nouns []*ir.Const) {
	// The callees, by the declaration identity a call names.
	kinds := map[*ir.Symbol]string{}
	for _, m := range mods {
		if m == nil || !stdMessage(m.Name()) {
			continue
		}
		for _, f := range m.Funcs() {
			if f.Sym() != nil && (f.Name() == compiledMessage || f.Name() == compiledNoun) {
				kinds[f.Sym()] = f.Name()
			}
		}
	}
	if len(kinds) == 0 {
		return nil, nil
	}
	for _, f := range funcs {
		for _, b := range f.Blocks() {
			for _, in := range b.Instrs() {
				c, ok := in.(*ir.Call)
				if !ok || c.Form() != ir.CalleeDirect || c.NumArgs() < 2 {
					continue
				}
				kind, ok := kinds[c.Callee()]
				if !ok {
					continue
				}
				k, ok := f.Def(c.Arg(1)).(*ir.Const)
				if !ok || k.Kind() != ir.ConstString {
					continue
				}
				if kind == compiledMessage {
					messages = append(messages, k)
				} else {
					nouns = append(nouns, k)
				}
			}
		}
	}
	return messages, nouns
}

// ModuleFuncs is every function mods declare, once initializers included.
func ModuleFuncs(mods []*ir.Module) []*ir.Func {
	var out []*ir.Func
	for _, m := range mods {
		if m == nil {
			continue
		}
		out = append(out, m.Funcs()...)
		for _, c := range m.Cells() {
			if f := c.Initializer(); f != nil {
				out = append(out, f)
			}
		}
	}
	return out
}

// ReachedFuncs is every function this program's own walk (Reached) reaches
// from roots and boots, the set a built image carries.
func ReachedFuncs(entry *ir.Module, mods []*ir.Module, roots []*ir.Func, boots []*ir.Symbol) []*ir.Func {
	reached, _ := NewProgram(entry, mods, io.Discard).Reached(roots, boots)
	out := make([]*ir.Func, 0, len(reached))
	for f := range reached {
		out = append(out, f)
	}
	return out
}
