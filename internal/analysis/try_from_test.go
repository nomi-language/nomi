package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
)

// tryFromErrors declares a chain of error types, each with a `From` impl
// from the one below it: IoError -> DiskError -> ApiError.
const tryFromErrors = `enum IoError
    Closed
end

enum DiskError
    Io IoError
    Full
end

enum ApiError
    Disk DiskError
end

interface Problem
    fn describe(p: self) -> String
end

impl Problem for DiskError
    fn describe(_p: DiskError) -> String
        "disk"
    end
end

impl From<IoError> for DiskError
    fn from(e: IoError) -> DiskError
        DiskError.Io(e)
    end
end

impl From<DiskError> for ApiError
    fn from(e: DiskError) -> ApiError
        ApiError.Disk(e)
    end
end

fn io() -> Result<Int, IoError>
    Err(.Closed)
end

fn disk(full: Bool) -> Result<Int, DiskError>
    if full then
        Err(.Full)
    else
        Ok(1)
    end
end

`

// tryOpsMarked is the `?` symbol on each line of src that ends with the
// comment `// <mark>`, in line order.
func tryOpsMarked(t *testing.T, fa *analysis.FileAnalysis, src, mark string) []*analysis.Symbol {
	t.Helper()
	var out []*analysis.Symbol
	for i, line := range strings.Split(src, "\n") {
		if !strings.HasSuffix(line, "// "+mark) {
			continue
		}
		col := strings.LastIndex(strings.TrimSuffix(line, "// "+mark), "?") + 1
		sym := fa.References[analysis.Pos{Line: i + 1, Col: col}]
		if sym == nil || sym.Kind != analysis.SymbolTryOp || sym.TryOp == nil {
			t.Fatalf("no `?` symbol at %d:%d", i+1, col)
		}
		out = append(out, sym)
	}
	if len(out) == 0 {
		t.Fatalf("no line marked %q", mark)
	}
	return out
}

// A `?` whose error the boundary's `From` impl takes converts it: the
// program is accepted, and the `?` records the impl it applies, in the `E?`
// spelling and as a pipe stage, with the impl's `from` as its dispatch.
func TestTryFrom_TheImplConverts(t *testing.T) {
	src := tryFromErrors + "fn count() -> Result<Int, ApiError>\n    n = disk(False)? // converts\n    m = True |> disk()? // converts\n    Ok(n + m)\nend\n"
	fa, errs := checkSourceWithStdlib(src)
	if len(errs) > 0 {
		t.Fatalf("the front end rejects a `?` the From impl converts: %v", errs)
	}
	for _, sym := range tryOpsMarked(t, fa, src, "converts") {
		conv := sym.TryOp.Convert
		if conv == nil {
			t.Fatalf("the `?` at %d:%d records no conversion", sym.Pos.Line, sym.Pos.Col)
		}
		if got := conv.Label(); got != "From<DiskError>" {
			t.Errorf("the `?` at %d:%d applies %q, want From<DiskError>", sym.Pos.Line, sym.Pos.Col, got)
		}
		if conv.To.String() != "ApiError" {
			t.Errorf("the `?` at %d:%d converts to %s, want ApiError", sym.Pos.Line, sym.Pos.Col, conv.To)
		}
		fd, ok := sym.DispatchImpl.(*ast.FuncDef)
		if !ok || fd.Name != "from" || fd.Params[0].TypeAnnotation.TypeString() != "DiskError" {
			t.Errorf("the `?` at %d:%d dispatches to %v, want ApiError's from", sym.Pos.Line, sym.Pos.Col, sym.DispatchImpl)
		}
	}
}

// A `?` whose types agree, or whose interface-typed boundary takes the
// error as an implementor, converts nothing: assignability is asked first.
func TestTryFrom_AssignabilityComesFirst(t *testing.T) {
	src := tryFromErrors + "fn same() -> Result<Int, DiskError>\n    n = disk(True)? // plain\n    Ok(n)\nend\n\n" +
		"fn problem() -> Result<Int, Problem>\n    n = disk(True)? // plain\n    Ok(n)\nend\n"
	fa, errs := checkSourceWithStdlib(src)
	if len(errs) > 0 {
		t.Fatalf("the front end rejects a `?` whose boundary takes its error: %v", errs)
	}
	for _, sym := range tryOpsMarked(t, fa, src, "plain") {
		if sym.TryOp.Convert != nil || sym.DispatchImpl != nil {
			t.Errorf("the `?` at %d:%d converts with %s, though its boundary takes the error", sym.Pos.Line, sym.Pos.Col, sym.TryOp.Convert.Label())
		}
	}
}

// A `?` with no impl from its error to the boundary's is the mismatch, and
// its help names both fixes. One conversion at most: IoError reaches
// ApiError only through DiskError, and the `?` does not follow the chain. A
// lambda's boundary is inferred, so it has no declared error type to
// convert to.
func TestTryFrom_NoImplIsTheMismatch(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"no impl",
			"enum Other\n    Failed\nend\n\nfn f() -> Result<Int, Other>\n    n = disk(True)?\n    Ok(n)\nend\n",
			"`?` error type mismatch: expected Other, got DiskError (the enclosing function returns Result<Int, Other>); write `impl From<DiskError> for Other` and the `?` converts the error, or convert it before the `?` with Result.map_err"},
		{"no chain",
			"fn f() -> Result<Int, ApiError>\n    n = io()?\n    Ok(n)\nend\n",
			"`?` error type mismatch: expected ApiError, got IoError (the enclosing function returns Result<Int, ApiError>); write `impl From<IoError> for ApiError` and the `?` converts the error, or convert it before the `?` with Result.map_err"},
		{"a variant holding the error keeps its map_err help",
			"enum Wrapped\n    Disk DiskError\n    Other String\nend\n\nfn f() -> Result<Int, Wrapped>\n    n = disk(True)?\n    Ok(n)\nend\n",
			"`?` error type mismatch: expected Wrapped, got DiskError (the enclosing function returns Result<Int, Wrapped>); write `impl From<DiskError> for Wrapped` and the `?` converts the error, or convert it before the `?` with Result.map_err\nhelp: wrap the error in `Wrapped.Disk`: `|> Result.map_err(Wrapped.Disk)?`"},
		{"two std types name no impl, which would be an orphan",
			"fn code() -> Result<Int, Int>\n    Err(1)\nend\n\nfn f() -> Result<Int, String>\n    n = code()?\n    Ok(n)\nend\n",
			"`?` error type mismatch: expected String, got Int (the enclosing function returns Result<Int, String>); convert the error before the `?`, for example with Result.map_err\nhelp: write the error as a String that says what failed: `|> Result.map_err(&\"...: ${&1}\")?`"},
		{"an inferred boundary does not convert",
			"fn f() -> Result<Int, ApiError>\n    g = fn ->\n        n = disk(True)?\n        Ok(n)\n    end\n    r: Result<Int, ApiError> = g()\n    r\nend\n",
			"type mismatch: expected Result<Int, ApiError>, got Result<Int, DiskError>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tryFromErrors + tc.src)
			if len(errs) == 0 {
				t.Fatalf("the front end accepts a `?` that has no From impl to apply:\n%s", tc.src)
			}
			expectExactDiag(t, errs, tc.want)
		})
	}
}

// `impl From<LocalErr> for String` is not an orphan: `From`'s `E` is an
// input, so a module's own type there admits the impl for a foreign target.
func TestTryFrom_ALocalSourceAdmitsAForeignTarget(t *testing.T) {
	src := "enum LocalErr\n    Bad\nend\n\nimpl From<LocalErr> for String\n    fn from(_e: LocalErr) -> String\n        \"bad\"\n    end\nend\n\n" +
		"fn local() -> Result<Int, LocalErr>\n    Err(.Bad)\nend\n\nfn f() -> Result<Int, String>\n    n = local()? // converts\n    Ok(n)\nend\n"
	fa, errs := checkSourceWithStdlib(src)
	if len(errs) > 0 {
		t.Fatalf("the front end rejects `impl From<LocalErr> for String`: %v", errs)
	}
	for _, sym := range tryOpsMarked(t, fa, src, "converts") {
		if got := sym.TryOp.Convert.Label(); got != "From<LocalErr>" {
			t.Errorf("the `?` applies %q, want From<LocalErr>", got)
		}
	}
}
