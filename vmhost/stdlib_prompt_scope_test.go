package vmhost_test

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// A stdlib module's `//!` prompt is checked in the module's own scope: its
// declarations and its imports, with no prelude, as the module's bodies are.
// Every path agrees on that. `nomi test std/regex.nomi` builds the embedded
// module's cases from the shared stdlib analysis (LoadStdlib), and a reference
// editor checks the module's source afresh (StdlibReference, through
// LoadStdlibSource). The shared analysis rejected a prompt's bare `False` when
// std/regex.nomi did not import it, but LoadStdlib did not report the
// shared analysis's errors and the builder lowered the prompt anyway, so the
// prompt passed `nomi test` and failed in the reference editor.
func TestStdlibPrompt_CachedAndFreshPathsShareTheModuleScope(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; lowers and runs std/regex's prompts on the VM; -short")
	}
	pinStdlibEnv(t)
	restoreEnv, err := vmhost.DefaultTestEnv()
	if err != nil {
		t.Fatal(err)
	}
	defer restoreEnv()

	const prompt = "assert String.contains?(\"room\", Regex`\\d+`) == False"
	src, ok := std.ReadFile("regex")
	if !ok {
		t.Fatal("std/regex is not embedded")
	}
	before, _, found := strings.Cut(string(src), "//! "+prompt+"\n")
	if !found {
		t.Fatalf("std/regex.nomi no longer carries the prompt %q", prompt)
	}
	promptLine := strings.Count(before, "\n") + 1

	path := strings.TrimPrefix(std.Load().FileURI("regex"), "file://")
	cachedProg, err := vmhost.LoadStdlib(path)
	if err != nil {
		t.Fatalf("cached path (LoadStdlib): %v", err)
	}
	freshProg, err := vmhost.LoadStdlibSource("regex", string(src))
	if err != nil {
		t.Fatalf("fresh path (LoadStdlibSource): %v", err)
	}
	cached := cachedProg.Cases(io.Discard, vmhost.TestOptions{})
	fresh := freshProg.Cases(io.Discard, vmhost.TestOptions{})
	if len(cached) == 0 || len(cached) != len(fresh) {
		t.Fatalf("cached path ran %d cases, fresh path %d", len(cached), len(fresh))
	}
	sawPrompt := false
	for i := range cached {
		c, f := cached[i], fresh[i]
		if c.Name != f.Name || c.Line != f.Line || c.EndLine != f.EndLine {
			t.Errorf("case %d: cached %q (lines %d-%d), fresh %q (lines %d-%d)",
				i, c.Name, c.Line, c.EndLine, f.Name, f.Line, f.EndLine)
		}
		for _, r := range []struct {
			path string
			c    vmhost.CaseResult
		}{{"cached", c}, {"fresh", f}} {
			if r.c.Err != nil || r.c.Blocked != nil {
				t.Errorf("%s path: %s does not pass: err %v, blocked %v", r.path, r.c.Name, r.c.Err, r.c.Blocked)
			}
		}
		if c.Line <= promptLine && promptLine <= c.EndLine {
			sawPrompt = true
		}
	}
	if !sawPrompt {
		t.Error("neither path ran the case holding the prompt")
	}

	cases, err := vmhost.StdlibReference("regex", prompt, "", io.Discard)
	if err != nil {
		t.Fatalf("reference editor: %v", err)
	}
	if len(cases) != 1 || cases[0].Err != nil || cases[0].Blocked != nil {
		t.Errorf("reference editor: want one passing case, got %+v", cases)
	}

	// The scope is the module's, not a user file's: std/regex imports
	// maybe.Maybe but not its variants, so a bare `Some` is undefined there.
	_, err = vmhost.StdlibReference("regex", "assert Some(1) != None", "", io.Discard)
	if err == nil {
		t.Fatal("a bare `Some` in a std/regex prompt is admitted; std/regex does not import it")
	}
	if !strings.Contains(err.Error(), "undefined type or variant 'Some'") {
		t.Errorf("a bare `Some` in a std/regex prompt: want \"undefined type or variant 'Some'\", got %v", err)
	}
}
