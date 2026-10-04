package irbuild

import (
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// The corpus is analysed once for the whole test binary, and every test over
// it reads that one result.
//
// Many tests in this package ask the same question of the same corpus: run
// the front end, run the builder, and read the result. A full walk per test
// would put the package past Go's default 600s per-package timeout, so the
// walk is evaluated once.
//
// Once is sound because there is exactly one corpus, the `tests` tree
// two directories up, and it is immutable for the lifetime of the binary. No
// test in this package writes under it, so `Analyze` and `GenerateIR` over it
// are pure functions of a tree nothing touches, and sync.OnceValues is the
// whole cache.
//
// The slice each caller gets is its own copy, so appending to it or
// reordering it cannot reach another test. The *Program and *Result inside it
// are shared, and callers must treat them as read-only: a test that annotated
// a shared *Program would produce an order-dependent failure elsewhere.
type corpusFile struct {
	// Path is the .nomi file, absolute.
	Path string
	// Rel is Path relative to the corpus root, which is what every report in
	// this package identifies a program by.
	Rel string
	// Prog is the checked program, nil when the front end refused the file.
	Prog *Program
	// AnalyzeErr is the front end's refusal. Not a backend signal: a corpus
	// module that is not a valid entry lands here.
	AnalyzeErr error
	// Res is the program lowered to IR.
	Res *Result
	// GenErr is a lowering error, which is a bug rather than a construct the
	// builder declined.
	GenErr error
}

// corpusRun is one whole-corpus analysis.
type corpusRun struct {
	// Root is the corpus directory, absolute.
	Root string
	// Files is one entry per .nomi file under Root, sorted by path so every
	// report over it is deterministic run to run.
	Files []corpusFile
}

// corpusWalks counts how many times the whole-corpus analysis has run.
// Counting rather than timing: the regression guarded against is a second
// walk, and a wall-clock assertion would catch it only on an idle machine
// while failing on a busy one. See corpusSharingViolation for why it is read
// from TestMain.
var corpusWalks atomic.Int64

// sharedCorpus is the one evaluation. sync.OnceValues rather than a mutex and
// a nil check because the error has to be shared too: a corpus that failed to
// enumerate must fail every test identically instead of being retried once
// per test.
var sharedCorpus = sync.OnceValues(analyzeCorpus)

// corpusRoot is the one place in this package's tests that names the corpus.
//
// It is the sharing guard's subject: a test cannot walk the corpus without
// first asking where it is, so counting the callers of this counts corpus
// walks exactly. Counting callers of the directory walker would also count
// walks of other roots.
func corpusRoot() (string, error) {
	return filepath.Abs(filepath.Join("..", "..", "tests"))
}

func analyzeCorpus() (*corpusRun, error) {
	corpusWalks.Add(1)
	root, err := corpusRoot()
	if err != nil {
		return nil, fmt.Errorf("resolving the corpus root: %w", err)
	}
	paths, err := nomiProgramsUnder(root)
	if err != nil {
		return nil, fmt.Errorf("discovering the corpus: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no corpus programs found under %s", root)
	}
	run := &corpusRun{Root: root, Files: make([]corpusFile, 0, len(paths))}
	for _, path := range paths {
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		f := corpusFile{Path: path, Rel: rel}
		f.Prog, f.AnalyzeErr = Analyze(path)
		if f.AnalyzeErr == nil {
			f.Res, _, f.GenErr = GenerateIR(f.Prog)
		}
		run.Files = append(run.Files, f)
	}
	return run, nil
}

// corpusSharingViolation reports the package having paid for the corpus more
// than once, as a message, or "" when it has not.
//
// Read from TestMain rather than asserted inside a test, because a test cannot
// see walks that happen after it: under `-shuffle=on` an in-test guard may run
// first, observe one walk, and pass while a later test walks again. After
// m.Run() returns there is no "later", so the count is final however the run
// was ordered or filtered.
func corpusSharingViolation() string {
	got := corpusWalks.Load()
	if got <= 1 {
		return ""
	}
	return fmt.Sprintf("the corpus was analysed %d times, want at most 1: the sharing in "+
		"corpusshare_test.go has come undone, and this package pays a full front-end and "+
		"builder pass over the corpus for every extra walk, against Go's "+
		"600s default per-package timeout", got)
}

// corpusAnalysis is how every test in this package reaches the corpus.
//
// The returned Files slice is the caller's own, so appending to it is safe;
// see the type comment for what remains shared.
func corpusAnalysis(t *testing.T) (root string, files []corpusFile) {
	t.Helper()
	run, err := sharedCorpus()
	if err != nil {
		t.Fatalf("the shared corpus analysis failed, so every test over it is vacuous: %v", err)
	}
	return run.Root, slices.Clone(run.Files)
}

// TestCorpusOnlyHasOneEnumerationSite is the other half of the sharing guard:
// it catches a new test that enumerates the corpus and walks it itself instead
// of calling corpusAnalysis.
//
// It counts callers of corpusRoot, the one place that names the corpus. A
// count of calls rather than an interception inside Analyze, because Analyze
// is production code and should not police its own tests.
//
// It does no corpus work, so it runs under -short too, and it is
// order-independent: source text does not depend on what has run.
func TestCorpusOnlyHasOneEnumerationSite(t *testing.T) {
	sites, err := corpusEnumeratorCalls()
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 {
		t.Errorf("%d call(s) to corpusRoot, want exactly 1 — analyzeCorpus in "+
			"corpusshare_test.go:\n  %s\n"+
			"a test over the corpus must take it from corpusAnalysis(t). Walking it "+
			"again costs this package a full front-end and builder pass over the whole "+
			"corpus, and a few such walks push it past the default per-package timeout.\n"+
			"A test over a different root does not belong here: call "+
			"nomiProgramsUnder(thatRoot) directly.",
			len(sites), strings.Join(sites, "\n  "))
	}
}

// corpusEnumeratorCalls lists every CALL to corpusRoot in the package's own
// test sources.
//
// Parsed rather than grepped: a text scan would count this guard's own error
// message and the matcher below among the hits. A call expression is the
// thing being counted, so the parser counts it.
func corpusEnumeratorCalls() ([]string, error) {
	names, err := filepath.Glob("*_test.go")
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no test sources found; the call-site census would be vacuous")
	}
	fset := token.NewFileSet()
	var out []string
	for _, name := range names {
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, err)
		}
		goast.Inspect(file, func(n goast.Node) bool {
			call, ok := n.(*goast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*goast.Ident); ok && id.Name == "corpusRoot" {
				out = append(out, fset.Position(call.Lparen).String())
			}
			return true
		})
	}
	return out, nil
}
