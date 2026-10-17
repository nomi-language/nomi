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
	// Manifest is Root's nomi.toml as the build read it, "" for none.
	Manifest string
}

// CheckFunc is the lowering Check runs.
type CheckFunc func(ctx context.Context, path, src string, a *Analyzed) (problems error, ok bool, err error)

// OfferedFunc is the test Offered runs.
type OfferedFunc func(path, src string, a *Analyzed) bool

var (
	check   CheckFunc
	offered OfferedFunc
)

// SetCheck installs the lowering Check runs and the test Offered runs.
// nomi/vmhost calls it from its package initializer.
func SetCheck(f CheckFunc, o OfferedFunc) { check, offered = f, o }

// Check is vmhost.CheckLowering for a file whose text src the caller has
// already analyzed as a: the program is lowered from a, with no second
// front end, and problems and err are what vmhost.CheckLowering answers.
// ok is false, and nothing ran, when a is not the program
// vmhost.CheckLowering would build: it has errors, which stop the front end
// before lowering; its root, test mode or host declarations are not the
// front end's; or a file it read (another project file, nomi.toml, or the
// entry itself when the program imports it back) does not hold on disk the
// text the build read, which vmhost.CheckLowering would read now. The
// caller then runs vmhost.CheckLowering. When ctx is done the lowering
// stops at its next step and answers ctx's error as err, as
// vmhost.CheckLowering does.
//
// Lowering only reads a, so a may be shared with concurrent readers. The
// caller must import nomi/vmhost, which installs the lowering.
func Check(ctx context.Context, path, src string, a *Analyzed) (problems error, ok bool, err error) {
	return check(ctx, path, src, a)
}

// Offered reports whether Check would lower a, the ok Check answers,
// without lowering it.
func Offered(path, src string, a *Analyzed) bool {
	return offered(path, src, a)
}
