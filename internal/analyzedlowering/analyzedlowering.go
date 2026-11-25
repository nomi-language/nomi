// Package analyzedlowering lets the language server lower a document from
// its own analysis, through nomi/vmhost, without that entry point being
// part of vmhost's public API: its input holds front-end types no code
// outside this module can build. vmhost installs the lowering from its
// package initializer (SetCheck), so any importer of vmhost may call Check.
package analyzedlowering

import (
	"context"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// Analyzed is a file the caller has already analyzed, with the other
// project files of the program it is the entry of: the language server's
// analysis of an open document (analysis.EntryProgram), as its own entry
// or through an entry that reaches it. Nodes and FA are the entry's
// prepared nodes and its analysis, Root the project root it was resolved
// against.
type Analyzed struct {
	Nodes []ast.Node
	FA    *analysis.FileAnalysis
	Root  string
	// Files are the program's other project files, checked as part of FA's
	// program (a recheck may have kept one's analysis from an earlier
	// check), each with the text it was parsed from.
	Files []analysis.EntryProgramFile
	// ReachesEntry reports that a file of the program imports the entry,
	// and the build answered that import with the entry's analyzed text.
	ReachesEntry bool
	// Manifest is what decided Root's build beside its sources as the build
	// read it (analysis.ManifestTexts): its nomi.toml, "" for none, and the
	// lock and workspace that select its versions.
	Manifest string
	// Stored, when set, is the entry of a program the language server opened
	// from its record, which is lowered from the record's kept lowering and
	// the checks of the files that changed since (analysis.StoredLowering);
	// the other fields are unset. Memo keeps no lowering of it.
	Stored *analysis.StoredLowering
}

// Found is what a lowering of a file finds for an editor: Problems, what
// `nomi check` reports beyond the front end (vmhost.CheckLowering), and
// Reached, each effect call the program reaches from the roots `nomi
// check` holds to nomi.toml's [effects] lists, each function that can
// reach one, and each call and function that can act, which a hover on
// the call or the function shows and a mark draws.
type Found struct {
	Problems error
	Reached  []Reached
}

// Reached is one place a lowering found that can do something besides
// compute its result: an effect call, a call that can act, or a function
// of the program and each call of it. Path
// and the span are where a hover shows it: an effect call's callee as the
// author wrote it, where a diagnostic about it points, a function's name,
// or a call's callee (1-based lines and byte columns, the end one past the
// last byte). A site that is no call in source, such as a `host fn`'s
// declaration, is a point: its End is its start. Text is the Markdown a
// hover on it shows: what it reaches and whether nomi.toml's lists allow
// it (effects.ReachedLine, effects.FunctionReach), or that it acts only
// on what it is passed (effects.HeldLine), or may act through an
// interface (effects.MayLine). Marked says an editor draws the token
// there bold: a call the compiler cannot show is pure, and the name of a
// function whose body holds one (vmhost's reachedCalls). A read of a
// `once`, a `once`'s name and a function named as a value have a line and
// no mark. Uses marks a
// function's, or its call's, line of the app fields it uses
// (effects.UsesLine) instead: a hover shows it, and it is never Marked,
// since an app field is an input and reaches nothing. Fills marks
// a line of code the program's main, exports and tests do not reach,
// found by the walk from every function they leave (vmhost's
// editorWalks): a hover shows it only where no other line is, so a file
// two programs hold reads as the one that reaches it says.
type Reached struct {
	Path            string
	Line, Col       int
	EndLine, EndCol int
	Text            string
	Marked          bool
	Uses            bool
	Fills           bool
}

// CheckFunc is the lowering Check runs.
type CheckFunc func(ctx context.Context, path, src string, a *Analyzed) (found Found, ok bool, err error)

// OfferedFunc is the test Offered runs.
type OfferedFunc func(path, src string, a *Analyzed) bool

var (
	check   CheckFunc
	offered OfferedFunc
	kept    analysis.KeptLoweringFunc
)

// SetCheck installs the lowering Check runs and the test Offered runs.
// nomi/vmhost calls it from its package initializer.
func SetCheck(f CheckFunc, o OfferedFunc) { check, offered = f, o }

// Check is vmhost.CheckLowering for a file whose text src the caller has
// already analyzed as a: the program is lowered from a, with no second
// front end, and found and err are what vmhost.CheckLowering answers.
// ok is false, and nothing ran, when a is not the program
// vmhost.CheckLowering would build: it has errors, which stop the front end
// before lowering; its root, test mode or host declarations are not the
// front end's; or a file it read (another project file, nomi.toml as
// analysis.ReadManifest reads it, or the entry itself when the program
// imports it back) does not hold the text the build read, which
// vmhost.CheckLowering would read now. The
// caller then runs vmhost.CheckLowering. When ctx is done the lowering
// stops at its next step and answers ctx's error as err, as
// vmhost.CheckLowering does.
//
// Lowering only reads a, so a may be shared with concurrent readers. The
// caller must import nomi/vmhost, which installs the lowering.
func Check(ctx context.Context, path, src string, a *Analyzed) (found Found, ok bool, err error) {
	return check(ctx, path, src, a)
}

// Offered reports whether Check would lower a, the ok Check answers,
// without lowering it.
func Offered(path, src string, a *Analyzed) bool {
	return offered(path, src, a)
}

// SetKept installs the function Kept runs. nomi/vmhost calls it from its
// package initializer.
func SetKept(f analysis.KeptLoweringFunc) { kept = f }

// Kept answers the kept lowering of the last lowering of the document at
// path that found nothing to report (analysis.KeptLoweringFunc), which the
// language server writes into a program's record.
func Kept(path string) (data []byte, tests bool, sums map[string][32]byte, ok bool) {
	if kept == nil {
		return nil, false, nil, false
	}
	return kept(path)
}
