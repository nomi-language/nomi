package hostpair

import (
	"strings"
	"testing"
)

// TestDeriveSourceReportsAParseFailure pins the one place this derivation
// deliberately does NOT match ffirun.
//
// ffirun discards the parse error and derives from recovery output
// (discovery.go:281), which is defensible for a scan that only decides whether
// the wrapper build path is needed. A derivation a consumer would emit a direct
// call from cannot make that elision: "no pairings" and "the file did not
// parse" must be distinguishable, or a typo in a facade silently becomes an
// empty binding set.
func TestDeriveSourceReportsAParseFailure(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		wantErr     bool
		wantPairing int
	}{
		{
			name: "empty source is not an error",
			src:  "",
		},
		{
			name: "declarations with no bindings are not an error",
			src:  "pub fn double(n: Int): Int {\n  n * 2\n}\n",
		},
		{
			name:    "unparseable source is an error",
			src:     "this is not nomi at all @@@ (((\n",
			wantErr: true,
		},
		{
			name:        "a rejected declaration is an error even though recovery still sees the binding",
			src:         "gopkg \"e/f\" as ffi\n\npub opaque type Box<T> Int\n\nimpl Box<T> {\n  fn peek(b: Box<T>): Int go ffi.Peek\n}\n",
			wantErr:     true,
			wantPairing: 1,
		},
		{
			name:        "valid bindings derive with no error",
			src:         "gopkg \"e/f\" as ffi\n\nfn echo(s: String): String go ffi.Echo\n",
			wantPairing: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pairings, err := DeriveSource("probe.nomi", "probe", []byte(tc.src))
			if tc.wantErr && err == nil {
				t.Fatal("expected a parse error, got nil — a caller cannot tell this from a file that declares nothing")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if err != nil {
				if !strings.Contains(err.Error(), "probe.nomi") {
					t.Errorf("error does not name the file: %v", err)
				}
				if strings.Contains(err.Error(), "%!w") {
					t.Errorf("error wraps a nil cause and renders as a Go format artifact: %v", err)
				}
			}
			if len(pairings) != tc.wantPairing {
				t.Fatalf("derived %d pairings, want %d", len(pairings), tc.wantPairing)
			}
		})
	}
}

// TestDeriveNodesIsSourceOrdered pins the ordering contract. A consumer that
// renders these into generated Go needs a deterministic sequence, and
// cmd/nomi-stdlibbindings' golden output depends on one; source order gives it
// without a sort, which keeps a caller free to sort by whichever key it uses.
func TestDeriveNodesIsSourceOrdered(t *testing.T) {
	const src = `gopkg "e/f" as ffi

opaque type Handle go ffi.Handle

fn first(): Int go ffi.First

impl Handle {
  fn middle(h: Handle): Int go ffi.Middle
}

fn last(): Int go ffi.Last
`
	pairings, err := DeriveSource("order.nomi", "order", []byte(src))
	if err != nil {
		t.Fatalf("DeriveSource: %v", err)
	}
	var names []string
	for _, p := range pairings {
		names = append(names, p.Name)
	}
	want := []string{"Handle", "first", "middle", "last"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", names, want)
	}
}

// TestSelectorWithNoHandleIsNotAPairing pins the drop rule.
//
// A `go alias.Symbol` naming an alias no handle declared has no import path, so
// there is no Go symbol. ffirun drops it (discovery.go:510); inventing one
// would give a consumer a call it cannot link.
func TestSelectorWithNoHandleIsNotAPairing(t *testing.T) {
	const src = `fn echo(s: String): String go nobody.Echo
`
	pairings, err := DeriveSource("orphan.nomi", "orphan", []byte(src))
	if err != nil {
		t.Fatalf("DeriveSource: %v", err)
	}
	if len(pairings) != 0 {
		t.Fatalf("derived %d pairings from a selector with no gopkg handle: %v", len(pairings), pairings)
	}
}
