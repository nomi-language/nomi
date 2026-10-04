package lsp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

const callUsersSrc = `pub struct User {
    name: String
}

pub fn make(): User { User{name: normalize("a")} }

fn normalize(s: String): String { String.trim(s) }

impl User {
    pub fn rename(u: User, name: String): User { User{name: normalize(name)} }
}
`

const callMainSrc = `import models/users
import models/users.User
import std/io

fn main() {
    u = users.make()
    v = User.rename(u, "b")
    w = users.make() |> User.rename("c")
    f = users.make
    io.inspect((v, w, f(), seed))
}

once seed = users.make()

test "makes" {
    assert users.make().name == "a"
}
`

func callHierarchyServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, src string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("nomi.toml", "[module]\nname = \"app\"\nentry_points = [\"main\"]\n")
	write("models/users.nomi", callUsersSrc)
	write("main.nomi", callMainSrc)
	s := NewServer()
	s.docs.SetWorkspaceRoot(dir)
	uri := pathToURI(filepath.Join(dir, "main.nomi"))
	if snap := s.docs.Open(uri, callMainSrc); snap.Analysis == nil || len(snap.Analysis.TypeErrors) > 0 {
		t.Fatalf("main.nomi does not check: %v", snap.Analysis.TypeErrors)
	}
	return s, uri, dir
}

func prepareCall(t *testing.T, s *Server, uri string, pos protocol.Position) []protocol.CallHierarchyItem {
	t.Helper()
	items, err := s.textDocumentPrepareCallHierarchy(nil, &protocol.CallHierarchyPrepareParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: protocol.DocumentUri(uri)}, Position: pos}})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func itemString(dir string, it protocol.CallHierarchyItem) string {
	rel, _ := filepath.Rel(dir, uriToPath(string(it.URI)))
	if strings.HasPrefix(rel, "..") {
		rel = "std:" + filepath.Base(string(it.URI))
	}
	return fmt.Sprintf("%s %s %s sel %s", it.Name, rel, fmtRange(it.Range), fmtRange(it.SelectionRange))
}

func rangesString(rs []protocol.Range) string {
	var out []string
	for _, r := range rs {
		out = append(out, fmtRange(r))
	}
	return strings.Join(out, " ")
}

// The item is sent back to the server as JSON, as a client does.
func roundTrip(t *testing.T, it protocol.CallHierarchyItem) protocol.CallHierarchyItem {
	t.Helper()
	data, err := jsonRoundTrip(it)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCallHierarchy(t *testing.T) {
	s, uri, dir := callHierarchyServer(t)

	items := prepareCall(t, s, uri, occ(callMainSrc, "make()", 0, 1))
	if len(items) != 1 {
		t.Fatalf("prepare on a call: %d items", len(items))
	}
	if got, want := itemString(dir, items[0]), "make models/users.nomi 4:0-4:50 sel 4:7-4:11"; got != want {
		t.Errorf("prepare: %s, want %s", got, want)
	}

	// Incoming calls of make: main's two calls (`f = users.make` is not a
	// call), the once initializer and the test.
	in, err := s.callHierarchyIncomingCalls(nil, &protocol.CallHierarchyIncomingCallsParams{Item: roundTrip(t, items[0])})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range in {
		got = append(got, itemString(dir, c.From)+" from "+rangesString(c.FromRanges))
	}
	want := []string{
		"main main.nomi 4:0-10:1 sel 4:3-4:7 from 5:14-5:18 7:14-7:18",
		"seed main.nomi 12:0-12:24 sel 12:5-12:9 from 12:18-12:22",
		`test "makes" main.nomi 14:0-16:1 sel 14:5-14:12 from 15:17-15:21`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("incoming of make:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// Outgoing calls of main: make, the impl function rename (called and
	// piped into) and a stdlib function.
	mains := prepareCall(t, s, uri, occ(callMainSrc, "main()", 0, 0))
	if len(mains) != 1 {
		t.Fatalf("prepare on main: %d items", len(mains))
	}
	out, err := s.callHierarchyOutgoingCalls(nil, &protocol.CallHierarchyOutgoingCallsParams{Item: roundTrip(t, mains[0])})
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, c := range out {
		got = append(got, c.To.Name+" "+itemString(dir, c.To)[len(c.To.Name)+1:]+" from "+rangesString(c.FromRanges))
	}
	if len(got) != 3 || !strings.HasPrefix(got[0], "make models/users.nomi 4:0-4:50 sel 4:7-4:11 from 5:14-5:18 7:14-7:18") ||
		!strings.HasPrefix(got[1], "rename models/users.nomi 9:4-9:78 sel 9:11-9:17 from 6:13-6:19 7:29-7:35") ||
		!strings.HasPrefix(got[2], "inspect std:io.nomi") {
		t.Errorf("outgoing of main:\n%s", strings.Join(got, "\n"))
	}

	// Across a closed file: normalize's callers are make and the impl
	// function rename, and rename calls normalize.
	in, err = s.callHierarchyIncomingCalls(nil, &protocol.CallHierarchyIncomingCallsParams{Item: roundTrip(t, out[1].To)})
	if err != nil {
		t.Fatal(err)
	}
	if len(in) != 1 || in[0].From.Name != "main" {
		t.Errorf("incoming of rename: %v", in)
	}
	out, err = s.callHierarchyOutgoingCalls(nil, &protocol.CallHierarchyOutgoingCallsParams{Item: roundTrip(t, out[1].To)})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || itemString(dir, out[0].To) != "normalize models/users.nomi 6:0-6:50 sel 6:3-6:12" || rangesString(out[0].FromRanges) != "9:60-9:69" {
		for _, c := range out {
			t.Logf("%s from %s", itemString(dir, c.To), rangesString(c.FromRanges))
		}
		t.Errorf("outgoing of rename: %d calls", len(out))
	}
	in, err = s.callHierarchyIncomingCalls(nil, &protocol.CallHierarchyIncomingCallsParams{Item: roundTrip(t, out[0].To)})
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, c := range in {
		got = append(got, c.From.Name+" from "+rangesString(c.FromRanges))
	}
	if strings.Join(got, ", ") != "make from 4:33-4:42, rename from 9:60-9:69" {
		t.Errorf("incoming of normalize: %v", got)
	}

	// Not a function: a binding, a type, a keyword.
	for _, pos := range []protocol.Position{occ(callMainSrc, "u = ", 0, 0), occ(callMainSrc, "User.rename", 0, 0), occ(callMainSrc, "fn main", 0, 0)} {
		if items := prepareCall(t, s, uri, pos); len(items) != 0 {
			t.Errorf("prepare at %v: %v, want nothing", pos, items)
		}
	}
}

func jsonRoundTrip(it protocol.CallHierarchyItem) (protocol.CallHierarchyItem, error) {
	var out protocol.CallHierarchyItem
	raw, err := json.Marshal(it)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}
