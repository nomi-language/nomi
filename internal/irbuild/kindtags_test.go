package irbuild

import (
	goast "go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// importKind's switch over a kind's tag has an arm for every tag there is,
// so a tag added to native.go fails here until importKind says how a kind
// of it is mirrored, and what falls out of the switch is a tag that does
// not exist.
func TestImportKindHasAnArmForEveryTag(t *testing.T) {
	tags := kindTags(t)
	if len(tags) < 15 {
		t.Fatalf("native.go declares only %d tags", len(tags))
	}
	arms := tagSwitchArms(t, "foreign.go", "importKind")
	var missing []string
	for _, tag := range tags {
		if !arms[tag] {
			missing = append(missing, tag)
		}
	}
	if len(missing) > 0 {
		t.Errorf("importKind has no arm for %s: say how a kind of each is mirrored into another file's gen, or that it is not", strings.Join(missing, ", "))
	}
	var stale []string
	for arm := range arms {
		found := false
		for _, tag := range tags {
			found = found || tag == arm
		}
		if !found {
			stale = append(stale, arm)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("importKind has arms for tags native.go does not declare: %s", strings.Join(stale, ", "))
	}
}

// kindTags is the names of the tag constants, in declaration order: the
// const block of native.go that starts at tagInvalid.
func kindTags(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "native.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range f.Decls {
		gd, ok := d.(*goast.GenDecl)
		if !ok || gd.Tok != token.CONST || len(gd.Specs) == 0 {
			continue
		}
		first := gd.Specs[0].(*goast.ValueSpec)
		if len(first.Names) != 1 || first.Names[0].Name != "tagInvalid" {
			continue
		}
		var tags []string
		for _, s := range gd.Specs {
			for _, n := range s.(*goast.ValueSpec).Names {
				tags = append(tags, n.Name)
			}
		}
		return tags
	}
	t.Fatal("native.go has no const block starting at tagInvalid")
	return nil
}

// tagSwitchArms is the identifiers the first `switch k.tag` of the function
// fn in file has a case for.
func tagSwitchArms(t *testing.T, file, fn string) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	arms := map[string]bool{}
	for _, d := range f.Decls {
		fd, ok := d.(*goast.FuncDecl)
		if !ok || fd.Name.Name != fn || fd.Body == nil {
			continue
		}
		goast.Inspect(fd.Body, func(n goast.Node) bool {
			sw, ok := n.(*goast.SwitchStmt)
			if !ok || len(arms) > 0 {
				return len(arms) == 0
			}
			sel, ok := sw.Tag.(*goast.SelectorExpr)
			if !ok || sel.Sel.Name != "tag" {
				return true
			}
			for _, c := range sw.Body.List {
				for _, e := range c.(*goast.CaseClause).List {
					if id, ok := e.(*goast.Ident); ok {
						arms[id.Name] = true
					}
				}
			}
			return false
		})
	}
	if len(arms) == 0 {
		t.Fatalf("%s: no switch over a kind's tag in %s", file, fn)
	}
	return arms
}
