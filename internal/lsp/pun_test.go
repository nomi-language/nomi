package lsp

import (
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// Go-to-definition on a punned label that reads a binding offers both
// declarations: the field's or parameter's, and the binding's. A pattern's
// punned label is the binding's own declaration, so it offers the field.
func TestPun_DefinitionOffersBothDeclarations(t *testing.T) {
	src := punSrc
	uri := "file:///puns.nomi"
	server := NewServer()
	server.docs.Open(uri, src)
	for _, c := range []struct {
		what  string
		at    protocol.Position
		name  string
		decls [][2]uint32
	}{
		{"a struct literal's `on:`", at(t, src, 7, "on:", 1), "on", [][2]uint32{{2, 2}, {6, 2}}},
		{"a struct literal's `name:`", at(t, src, 7, "name:", 1), "name", [][2]uint32{{1, 2}, {5, 8}}},
		{"an update's `on:`", at(t, src, 12, "on:", 1), "on", [][2]uint32{{2, 2}, {11, 2}}},
		{"a named argument's `status:`", at(t, src, 26, "status:", 1), "status", [][2]uint32{{20, 8}, {25, 2}}},
		{"a pattern's `on:`", at(t, src, 16, "on:", 1), "on", [][2]uint32{{2, 2}}},
	} {
		locs := appFieldDefinitions(t, server, uri, c.at)
		wantDefinitions(t, c.what, locs, uri, c.name, c.decls...)
	}
}

// Renaming a punned label's binding writes `name: newname`, and renaming
// its field or parameter writes `newname: name`.
func TestPun_RenameUnpunsEitherWay(t *testing.T) {
	src := punSrc
	for _, c := range []struct {
		what    string
		at      protocol.Position
		newName string
		want    []string
	}{
		{"the literal's binding", at(t, src, 6, "on", 1), "day",
			[]string{"6:2-4=>day", "7:15-18=>on: day"}},
		{"the update's binding", at(t, src, 11, "on", 1), "day",
			[]string{"11:2-4=>day", "12:8-11=>on: day"}},
		{"the field", at(t, src, 2, "on", 1), "date",
			[]string{"12:8-11=>date: on", "16:8-11=>date: on", "2:2-4=>date", "7:15-18=>date: on"}},
		{"the pattern's binding", at(t, src, 17, "on", 1), "d",
			[]string{"16:8-11=>on: d", "17:2-4=>d"}},
		{"the argument's binding", at(t, src, 25, "status", 1), "code",
			[]string{"25:2-8=>code", "26:11-18=>status: code"}},
	} {
		if got := renameAt(t, src, c.at, c.newName); strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("rename of %s = %v, want %v", c.what, got, c.want)
		}
	}
}
