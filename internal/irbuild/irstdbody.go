package irbuild

// The retention seam has two producers: `funcDecl` for a user program's
// module-scope `fn`, and `emitStdFunc` for a Nomi-bodied stdlib declaration.
// The std bodies need no instruction, grammar rule or `ir.Lint` rule of their
// own: they are the same shape lowered through the same builder by a second
// caller.

import "github.com/nomi-language/nomi/internal/ast"

// irFuncSig is everything the retention seam reads of a function's signature,
// and nothing else.
//
// It exists because the seam has two producers, and a `*fnSig` is not
// available to both. `funcDecl` has one; `emitStdFunc` has a `*stdFunc`,
// which is a different record of a different resolution — a sibling table
// rather than a module function table, a `key` rather than a Go name, and no
// dictionary seam at all. Fabricating a `*fnSig` for the std side would be
// inventing a call-site view that no call site uses, and the fabricated
// fields would be the ones a later reader trusts.
//
// Each field is here because something reads it:
//
//   - `result` is what the body must produce. `tail`, `tailIf`, `tailCase`
//     and `armInto` compare against it, and `irFuncShellFor` sizes the result
//     slot from it.
//   - `decl` is the declaration a callee `*ir.Symbol` is interned on.
//     `ir.Table.Symbol` panics on a nil token rather than comparing printed
//     names, so identity has to come from the node.
//   - `dicts` is how many dictionary parameters the declaration takes. A
//     bounded generic has a type parameter, which has no representation in
//     this IR at all, so a non-zero count declines. It is a count and
//     not the seam itself because that is the only question asked of it.
//   - `name` is what `irFuncObserved` reports this body as, and it is NOT
//     `fd.Name` for the std side. `add` is the declared name of eleven
//     distinct stdlib functions; `stdFunc.key` is the module-qualified
//     spelling that already serves as the identity a refusal names. A hook
//     counting bodies by `fd.Name` would collapse them.
//   - `origin` is which population the body belongs to. It is a field rather
//     than something a reader infers from the name because
//     `stdlibLowering()` is a process-wide `sync.Once`: whether a hook sees
//     the std bodies depends on whether an earlier caller in the process
//     already ran it, so a count that does not separate the two populations
//     depends on test order.
//   - `inferResult` and `testArms` are the lambda and test-body variants of
//     the shape; see their field comments.
//
// Nothing else on either record is read.
type irFuncSig struct {
	// inferResult lets lambda arms establish their shared result kind.
	inferResult bool
	// testArms lowers each arm of a test-body `if` or `case` STATEMENT as
	// test statements, assertions included, whose values are discarded.
	testArms bool
	result   kind
	decl     *ast.FuncDef
	dicts    int
	name     string
	origin   irFuncOrigin
}

// irFuncOrigin is which producer built a retained function, and therefore
// which denominator it counts against.
type irFuncOrigin uint8

const (
	// irFromModule is `funcDecl`: a module-scope `fn` in a user program.
	irFromModule irFuncOrigin = iota
	// irFromStd is `emitStdFunc`: a Nomi-bodied declaration in `std/`.
	irFromStd
	// irFromImpl is a declared or inherited source body of a user impl block.
	irFromImpl
)

// irFuncSigOf is `funcDecl`'s producer.
func irFuncSigOf(fd *ast.FuncDef, sig *fnSig) irFuncSig {
	dicts := 0
	if sig.dict != nil {
		dicts = len(sig.dict.params)
	}
	return irFuncSig{result: sig.result, decl: sig.decl,
		dicts: dicts, name: fd.Name, origin: irFromModule}
}

// irStdFuncSig is `emitStdFunc`'s producer.
//
// `dicts` is zero and that is a fact about the population rather than an
// omission: `collectStdCandidates` refuses a generic stdlib declaration by
// name — `stdlib generic function` — before a body is ever tried, so a
// candidate reaching `emitStdFunc` has no type parameters and no dictionary.
// The one exception is a monomorphic INSTANCE (stdinstance.go), whose type
// arguments are already substituted to concrete kinds by the time the body is
// lowered, so it has none either.
func irStdFuncSig(f *stdFunc, fd *ast.FuncDef) irFuncSig {
	return irFuncSig{result: f.result, decl: fd, name: f.key, origin: irFromStd}
}
