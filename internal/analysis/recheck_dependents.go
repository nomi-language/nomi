package analysis

import (
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/nomi-language/nomi/internal/ast"
)

// An edit that changes a file's interface changes what the files that
// import it, directly or through others, may say. Recheck then checks those
// files afresh with the edited ones, parsed, declared, walked and checked
// as a full check would, and keeps every other file's analysis as it is:
// such a file imports none of them, so neither its declarations nor its
// bodies can name anything they declare (recheckDependents). The program is
// a new one, folded over the fresh files and the kept ones
// (checkProgramFrom), and its reductions over every file's bodies run again.
//
// What another file may read of a file without importing it is the impls
// it brings into the program: they are global. So an edit that changes the
// impls an edited file brings (implsFingerprint) is checked in full, and so
// is one where a kept file's analysis already holds something of a file
// checked afresh (a call dispatched to one of its impls).

// implsFingerprint renders what of a file's syntax decides the impls it
// brings into the program: every interface impl block's header and its
// items' signatures, every inherent block's header, every derive and
// conformance, and the name, kind and type
// parameters of every type it declares, which gets a Debug impl and is
// what the program's impl tables key on. Bodies and positions are left out,
// as interfaceFingerprint leaves them out. nodes are the file's nodes as
// parsed.
func implsFingerprint(nodes []ast.Node) string {
	var b strings.Builder
	for _, n := range nodes {
		switch n := n.(type) {
		case *ast.ImplBlock:
			if n.Interface == nil {
				// An inherent block's functions are its own type's, which
				// only a file that imports this one can name.
				b.WriteString("impl ")
				fingerprintValue(&b, reflect.ValueOf(n.Receiver))
				fingerprintValue(&b, reflect.ValueOf(n.Generics))
				fingerprintValue(&b, reflect.ValueOf(n.WhereClauses))
				break
			}
			fingerprintValue(&b, reflect.ValueOf(n))
		case *ast.ImplConformance:
			fingerprintValue(&b, reflect.ValueOf(n))
		case *ast.StructDef, *ast.EnumDef, *ast.TypeDef, *ast.ExternType, *ast.InterfaceDef, *ast.TypeAlias:
			v := reflect.ValueOf(n).Elem()
			b.WriteString(n.NodeType())
			b.WriteByte(' ')
			b.WriteString(v.FieldByName("Name").String())
			if tp := v.FieldByName("TypeParams"); tp.IsValid() {
				fingerprintValue(&b, tp)
			}
		default:
			continue
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// recheckDependents is Recheck's answer for edits that change interfaces:
// it checks the edited files and every file that imports one of them,
// directly or through others, afresh, and keeps the analyses of the rest
// (checkProgramFrom). It answers false with the reason when the edits need
// a full check. It writes nothing until it has decided; once it has, prev
// is consumed, since the kept analyses join the answer. One thing it can
// only decide after checking: when the application types a kept file may
// name changed (keptAppTypes), it answers false with prev consumed. parsed
// holds the edited texts parsed, by module key; nil, or a missing key,
// parses again.
//
// prev may be a program whose edited files' bodies rebuildEditedBodies
// rebuilt in place before it found that a `once` changed type: that writes
// only the edited files, their declarations' positions, and what other
// files hold of those, and every one of those files is checked afresh here.
func (dm *DocumentManager) recheckDependents(prev *CheckedProgram, edits []ProgramEdit, parsed map[string]*parsedFile) (*CheckedProgram, string) {
	c := prev.checks
	switch {
	case c == nil:
		return nil, "the program was not checked"
	case prev.externs:
		return nil, "the project declares Go bindings"
	case prev.unread:
		return nil, "the program reads files of other modules"
	case len(c.checked) != len(prev.Files)-1:
		return nil, "the program checks files it does not report"
	}
	edited := make(map[string]bool, len(edits))
	for _, e := range edits {
		edited[e.Key] = true
		if prev.Files[e.Key] == nil {
			return nil, "the program does not reach the file"
		}
		if len(prev.Syntax[e.Key]) > 0 {
			return nil, "the old text has a syntax error"
		}
		pf := parsed[e.Key]
		if pf == nil {
			pf = parseFile(e.Text)
			if parsed == nil {
				parsed = map[string]*parsedFile{}
			}
			parsed[e.Key] = pf
		}
		if len(pf.errs) > 0 {
			return nil, "the new text has a syntax error"
		}
		was, ok := prev.implFingerprints[e.Key]
		if !ok || pf.implFingerprint != was {
			return nil, "the impls the file brings changed"
		}
		if exportsFunctions(pf.nodes) {
			return nil, "the program exports functions"
		}
	}
	for _, nodes := range prev.Nodes {
		if exportsFunctions(nodes) {
			// Which file a repeated name's error goes to depends on every
			// file (CheckExportNames).
			return nil, "the program exports functions"
		}
	}

	// The files that import an edited one, directly or through others.
	importers := map[string][]string{}
	for key, fa := range prev.Files {
		for _, imported := range fa.ImportedModules {
			importers[imported] = append(importers[imported], key)
		}
	}
	fresh := maps.Clone(edited)
	queue := slices.Collect(maps.Keys(edited))
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		for _, importer := range importers[key] {
			if !fresh[importer] {
				fresh[importer] = true
				queue = append(queue, importer)
			}
		}
	}
	if !fresh[prev.Entry] {
		// The entry reaches every file, so it imports each edited one.
		return nil, "the entry does not import the edited files"
	}
	if len(fresh) == len(prev.Files) {
		return nil, "every file of the program depends on the edited files"
	}

	// Nothing a kept file holds may come from a file checked afresh: such a
	// file's symbols, the impl methods its calls were dispatched to, or the
	// modules its type-qualified calls reached.
	paths := map[string]bool{}
	impls := map[ast.Node]bool{}
	for key := range fresh {
		if path := prev.Files[key].FilePath; path != "" {
			paths[path] = true
		}
		for _, n := range prev.Nodes[key] {
			if blk, ok := n.(*ast.ImplBlock); ok {
				for _, fn := range implBlockFuncDefs(blk) {
					impls[fn] = true
				}
				for _, ext := range implBlockExternFuncs(blk) {
					impls[ext] = true
				}
			}
		}
	}
	keep := map[string]bool{}
	for key, fa := range prev.Files {
		if fresh[key] {
			continue
		}
		if fa.kept == nil {
			return nil, "a file's declarations were not kept"
		}
		if holdsFrom(fa, fresh, paths, impls) {
			return nil, "a file that does not import the edited files holds something of them"
		}
		keep[key] = true
	}

	// Decided: the kept analyses join the answer.
	freshOrigins := map[string]bool{}
	for key := range fresh {
		freshOrigins[prev.Files[key].Origin] = true
	}
	wasApps := keptAppTypes(prev.Files[prev.Entry].Program(), freshOrigins)
	prev.consumed = true
	texts := maps.Clone(prev.Texts)
	for _, e := range edits {
		texts[e.Key] = e.Text
	}
	next := dm.checkProgramFrom(prev.Root, prev.Entry, texts, prev, keep, parsed)
	if !maps.Equal(keptAppTypes(next.Files[next.Entry].Program(), freshOrigins), wasApps) {
		// A kept file's bodies were checked knowing which of its types are
		// application types, which the entry boots decide: `Settings.tag`
		// is a read of one, or an error. The answer is a full check's,
		// which the caller makes from the texts alone.
		return nil, "the application types a kept file may name changed"
	}
	return next, ""
}

// keptAppTypes answers the application types of program declared outside
// the files whose origins fresh names: the ones a file a dependents recheck
// keeps may name.
func keptAppTypes(program *Program, fresh map[string]bool) map[*StructType]bool {
	apps := map[*StructType]bool{}
	for _, st := range program.AppTypes {
		if !fresh[st.Origin] {
			apps[st] = true
		}
	}
	return apps
}

// exportsFunctions reports whether nodes declare an `export fn`.
func exportsFunctions(nodes []ast.Node) bool {
	for _, n := range nodes {
		if fn, ok := n.(*ast.FuncDef); ok && fn.Export {
			return true
		}
	}
	return false
}

// holdsFrom reports whether fa's analysis holds something of the files keys
// name, whose paths are paths and whose impl methods are impls: a symbol one
// of them defines, a call or an operator dispatched to one of their impl
// methods, or a type-qualified call that reached one of them.
func holdsFrom(fa *FileAnalysis, keys, paths map[string]bool, impls map[ast.Node]bool) bool {
	for key := range fa.TypeMethodModules {
		if keys[key] {
			return true
		}
	}
	from := func(sym *Symbol) bool {
		for s, hops := sym, 0; s != nil && hops < 64; s, hops = s.Resolved, hops+1 {
			if paths[s.SourceFile] || s.DispatchImpl != nil && impls[s.DispatchImpl] {
				return true
			}
		}
		return false
	}
	for _, m := range []map[Pos]*Symbol{fa.References, fa.Definitions, fa.PunnedFieldLabels} {
		for _, sym := range m {
			if from(sym) {
				return true
			}
		}
	}
	for _, op := range fa.OperatorImpls {
		if impls[op.Method] {
			return true
		}
	}
	return false
}
