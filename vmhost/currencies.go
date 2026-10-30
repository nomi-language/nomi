package vmhost

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/nomi-language/nomi/internal/analysis"
	"github.com/nomi-language/nomi/internal/ast"
	"github.com/nomi-language/nomi/internal/cldrfull"
	"github.com/nomi-language/nomi/internal/frontend"
	"github.com/nomi-language/nomi/internal/irbuild"
	"github.com/nomi-language/nomi/internal/stdformat"
	"github.com/nomi-language/nomi/rt"
)

// A MODULE LISTS THE CUSTOM CURRENCIES ITS CODE WRITES.
//
// A custom currency needs no setup, so a typo makes a new one: `PST:0`
// beside `PTS:0` is two currencies. A module's nomi.toml therefore lists the
// custom currencies its source may name (`[money].custom_currencies`), and
// a “Currency`…` “ or “Money`…` “ literal, or a `Currency.custom` call
// whose code and digits are both literals, that names an unlisted one is a
// compile error at it. The literal's currency is the Ok its handler
// answered (checkLiteral); the call's is its two arguments.
//
// The list is per module: a file's is the nomi.toml nearest above it, so a
// dependency's literals answer to the dependency's list. A file with no
// module (a script, a single-file tour block, the REPL) and the standard
// library's own files may name any. A currency made from values while the
// program runs (`Currency.custom(code, digits)`, `Currency.parse`,
// `Money.parse_in`) is never checked.

// UnlistedCurrencyCode is the Code of the diagnostic at a `Currency.custom`
// call whose literal currency its module does not list. An unlisted
// literal's diagnostic is InvalidLiteralCode's.
const UnlistedCurrencyCode = "unlisted-currency"

// CustomCurrencyCallCode is the Code of the hint at a `Currency.custom` call
// whose code and digits are both literals, which names the literal that
// writes the same currency. CheckLowering answers it after the problems; the
// language server shows it as a warning, and `nomi check` does not report it.
const CustomCurrencyCallCode = "custom-currency-call"

// currencyModule is what a file's module allows: whether the file is in a
// module, the custom currencies its nomi.toml lists, and how a diagnostic
// names that nomi.toml.
type currencyModule struct {
	inModule bool
	listed   []string
	toml     string
}

// currencyModuleOf is the module of the file at path: a site's path, absolute
// for a file on disk, a module's name (`main.nomi`) for an in-memory one.
func (p *Program) currencyModuleOf(path string) currencyModule {
	if !filepath.IsAbs(path) {
		if m := p.cfg.manifest; m != nil {
			return currencyModule{inModule: true, listed: m.CustomCurrencies, toml: "nomi.toml"}
		}
		return currencyModule{}
	}
	if p.currencyModules == nil {
		p.currencyModules = map[string]currencyModule{}
	}
	dir := filepath.Dir(path)
	if cm, ok := p.currencyModules[dir]; ok {
		return cm
	}
	var cm currencyModule
	if root, ok := manifestDirAbove(dir); ok {
		m, err := analysis.LoadManifest(root)
		// The front end reports a manifest it cannot read; the standard
		// library's own files are in no project module.
		if err == nil && m.Name != "std" {
			cm = currencyModule{inModule: true, listed: m.CustomCurrencies, toml: p.tomlName(root)}
		}
	}
	p.currencyModules[dir] = cm
	return cm
}

// manifestDirAbove is the nearest directory from dir upward that holds a
// nomi.toml.
func manifestDirAbove(dir string) (string, bool) {
	for {
		if info, err := os.Stat(filepath.Join(dir, "nomi.toml")); err == nil && !info.IsDir() {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// tomlName is how a diagnostic names the nomi.toml in root: its path from the
// program's root when it is under it, else its absolute path.
func (p *Program) tomlName(root string) string {
	path := filepath.Join(root, "nomi.toml")
	if p.prog.Root != "" {
		if rel, err := filepath.Rel(p.prog.Root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return path
}

// unlisted says why currency, a custom currency as its literal writes it
// (`PTS:0`), written at a site in the file at path, is a compile error: the
// file's module does not list it. The hints suggest a listed one that is
// close.
func (p *Program) unlisted(path, currency string) (msg string, hints []string, ok bool) {
	cm := p.currencyModuleOf(path)
	if !cm.inModule || slices.Contains(cm.listed, currency) {
		return "", nil, false
	}
	msg = fmt.Sprintf("`%s` is a custom currency this module does not list: add it to [money].custom_currencies in %s", currency, cm.toml)
	if near := nearCurrency(currency, cm.listed); near != "" {
		hints = append(hints, fmt.Sprintf("did you mean `%s`? %s lists it", near, cm.toml))
	}
	return msg, hints, true
}

// nearCurrency is the first of listed close to currency: the same code with
// other digits, or one edit (an insertion, a deletion, a substitution or two
// neighbours swapped) away; "" for none.
func nearCurrency(currency string, listed []string) string {
	code, _, _ := strings.Cut(currency, ":")
	for _, l := range listed {
		if c, _, _ := strings.Cut(l, ":"); c == code {
			return l
		}
	}
	for _, l := range listed {
		if editDistance(currency, l) == 1 {
			return l
		}
	}
	return ""
}

// editDistance is the optimal string alignment distance between a and b.
func editDistance(a, b string) int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(a)][len(b)]
}

// literalCurrency is the custom currency a Currency or Money literal's Ok
// value v holds, as its literal writes it; false for an ISO one.
func literalCurrency(v any) (string, bool) {
	if r, ok := v.(*rt.Record); ok && r.Desc.Kind == rt.KindStruct {
		c, found := r.FieldNamed("currency")
		if !found {
			return "", false
		}
		v = c
	}
	text, ok := localeTag(v)
	if !ok || !strings.Contains(text, ":") {
		return "", false
	}
	return text, true
}

// currencyCall is a `Currency.custom` call whose code and digits are both
// literals that make a custom currency.
type currencyCall struct {
	path            string
	line, col       int
	endLine, endCol int
	call, currency  string
}

// currencyCalls are the program's `Currency.custom` calls whose code and
// digits are literals that make a custom currency, in module and source
// order. Std's own modules are left out.
func (p *Program) currencyCalls() []currencyCall {
	var out []currencyCall
	for i := range p.prog.Modules {
		mod := &p.prog.Modules[i]
		if mod.FA == nil || strings.HasPrefix(mod.Name, "std/") {
			continue
		}
		path := mod.Path
		if path == "" {
			path = mod.Name + ".nomi"
		}
		for _, top := range mod.Nodes {
			analysis.WalkNodes(top, func(n ast.Node) {
				if c, ok := customCurrencyCall(mod.FA, n); ok {
					c.path = path
					out = append(out, c)
				}
			})
		}
	}
	return out
}

// customCurrencyCall reads n as `Currency.custom("PTS", 0)`, std/currency's
// function called with a string literal and an integer literal that make a
// custom currency.
func customCurrencyCall(fa *analysis.FileAnalysis, n ast.Node) (currencyCall, bool) {
	call, ok := n.(*ast.Call)
	if !ok || len(call.Args) != 2 {
		return currencyCall{}, false
	}
	callee, ok := call.Func.(*ast.FieldAccess)
	if !ok || callee.Field == nil || callee.Field.Name != "custom" || !resultOfCurrency(fa.ExprTypes[call]) {
		return currencyCall{}, false
	}
	owner, ok := callee.Object.(*ast.TypeIdent)
	if !ok {
		return currencyCall{}, false
	}
	var code *ast.StringLit
	var digits *ast.IntLit
	for i, a := range call.Args {
		name := []string{"code", "minor_digits"}[i]
		if na, ok := a.(*ast.NamedArg); ok {
			name, a = na.Name, na.Value
		}
		switch name {
		case "code":
			code, _ = a.(*ast.StringLit)
		case "minor_digits":
			digits, _ = a.(*ast.IntLit)
		}
	}
	if code == nil || digits == nil || digits.Big != nil || code.Raw || code.Triple {
		return currencyCall{}, false
	}
	currency := code.Value + ":" + strconv.FormatInt(digits.Value, 10)
	if stdformat.CustomCurrencyProblem(cldrfull.Full(), currency) != "" {
		return currencyCall{}, false
	}
	c := currencyCall{line: owner.Line, col: owner.Col, currency: currency,
		call: fmt.Sprintf("%s.custom(%q, %d)", owner.Name, code.Value, digits.Value)}
	if !call.Span.IsZero() {
		c.endLine, c.endCol = call.Span.EndLine, call.Span.EndCol
	}
	return c, true
}

// resultOfCurrency reports whether ty is `Result<Currency, currency.Error>`,
// what std/currency's `Currency.custom` answers.
func resultOfCurrency(ty analysis.Type) bool {
	r, ok := ty.(*analysis.EnumType)
	if !ok || r.Name != "Result" || len(r.TypeArgs) != 2 {
		return false
	}
	c, ok := r.TypeArgs[0].(*analysis.DistinctType)
	e, ok2 := r.TypeArgs[1].(*analysis.EnumType)
	return ok && ok2 && c.Origin == "std/currency" && c.Name == "Currency" &&
		e.Origin == "std/currency" && e.Name == "Error"
}

// literalFormHint is the hint at a `Currency.custom` call with literal
// arguments: the literal that writes the same currency.
func literalFormHint(currency string) string {
	return fmt.Sprintf("write Currency`%s`, which the compiler checks", currency)
}

// checkCurrencyCalls answers the diagnostic of each literal `Currency.custom`
// call whose module does not list its currency.
func (p *Program) checkCurrencyCalls() frontend.Diagnostics {
	var ds frontend.Diagnostics
	for _, c := range p.currencyCalls() {
		msg, hints, bad := p.unlisted(c.path, c.currency)
		if !bad {
			continue
		}
		d := p.currencyDiagnostic(c, fmt.Sprintf("%s: %s", c.call, msg))
		d.Hints = append(append(d.Hints, hints...), literalFormHint(c.currency))
		d.Code = UnlistedCurrencyCode
		ds = append(ds, d)
	}
	return ds
}

// currencyCallHints are the hints at the entry's literal `Currency.custom`
// calls whose currency is no error: the literal that writes it.
func (p *Program) currencyCallHints() frontend.Diagnostics {
	var ds frontend.Diagnostics
	entry := p.prog.Entry().Path
	for _, c := range p.currencyCalls() {
		if c.path != entry {
			continue
		}
		if _, _, bad := p.unlisted(c.path, c.currency); bad {
			continue
		}
		d := p.currencyDiagnostic(c, fmt.Sprintf("%s makes a fixed currency: %s", c.call, literalFormHint(c.currency)))
		d.Code = CustomCurrencyCallCode
		ds = append(ds, d)
	}
	return ds
}

func (p *Program) currencyDiagnostic(c currencyCall, msg string) frontend.Diagnostic {
	d := frontend.NewDiagnostic(c.path, p.sources[c.path], c.line, c.col, msg)
	if c.endLine > 0 {
		d.EndLine, d.EndCol = c.endLine, c.endCol
	}
	return d
}

// unlistedLiteral is the diagnostic message of a Currency or Money literal
// at site, whose handler answered v, when its module does not list v's
// custom currency.
func (p *Program) unlistedLiteral(site irbuild.LiteralSite, v any) (string, []string, bool) {
	if !site.Currency {
		return "", nil, false
	}
	currency, ok := literalCurrency(v)
	if !ok {
		return "", nil, false
	}
	path := site.Path
	if path == "" {
		path = p.moduleWithNodeAt(site.Line, site.Col)
	}
	return p.unlisted(path, currency)
}
