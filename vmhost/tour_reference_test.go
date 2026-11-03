package vmhost_test

import (
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// referenceDir is where scripts/build-tour-wasm.sh has cmd/nomi-docgen
// write the stdlib reference pages, beside the staged bundle.
const referenceDir = "../tour/src/content/docs/reference"

// referencePrompt matches one `//!` prompt as a reference page carries it:
// a live editor (`language-nomi-test`), or code the page does not run
// (`language-nomi` with data-nomi-browser-lacks).
var referencePrompt = regexp.MustCompile(`<pre data-nomi-stdlib-module="([^"]+)"(?: data-nomi-stdlib-context="([^"]*)")?(?: data-nomi-browser-lacks="([^"]*)")?><code class="language-nomi(?:-test)?">([\s\S]*?)</code></pre>`)

// TestTourWasmAnswersTheReferenceEditors runs every `//!` prompt on the
// generated stdlib reference pages through the staged wasm, under node, as
// the page's worker runs one (nomiRunStdlibTest). Each live editor must run
// clean: the page runs it when it scrolls into view, so one that fails
// fails for every reader. cmd/nomi-docgen shows a prompt that reaches a
// capability the browser lacks as code it does not run (its
// promptClassifier); such a prompt runs here too, and the log names each
// that ran clean anyway, where the classification, which counts what a
// prompt could reach, is wider than what it does reach.
func TestTourWasmAnswersTheReferenceEditors(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; loads a 25MB wasm under node; -short")
	}
	if stagedFileMissing(t, "nomi.wasm.gz") || stagedFileMissing(t, "wasm_exec.js") {
		return
	}
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv(requireEnv) == "1" {
			t.Fatalf("node is not on PATH, so the staged wasm cannot be run the way the browser runs it (%s=1 makes this a failure)", requireEnv)
		}
		t.Skip("node is not on PATH; the staged wasm cannot be executed the way the browser executes it")
	}
	pages, err := filepath.Glob(filepath.Join(referenceDir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) == 0 {
		t.Fatalf("no reference pages under %s, though the bundle is staged. Fix: %s", referenceDir, rebuildInstruction)
	}
	sort.Strings(pages)

	type probe struct {
		Name    string `json:"name"`
		Module  string `json:"module"`
		Context string `json:"context"`
		Source  string `json:"source"`
		lacks   string
	}
	var probes []probe
	for _, page := range pages {
		data, err := os.ReadFile(page)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, m := range referencePrompt.FindAllStringSubmatch(string(data), -1) {
			n++
			probes = append(probes, probe{
				Name:    m[1] + " #" + strconv.Itoa(n),
				Module:  m[1],
				Context: html.UnescapeString(m[2]),
				Source:  html.UnescapeString(m[4]),
				lacks:   m[3],
			})
		}
	}
	if len(probes) == 0 {
		t.Fatalf("no `//!` prompts found on the reference pages under %s", referenceDir)
	}
	answers := runUnderNode(t, node, probes)
	if len(answers) != len(probes) {
		t.Fatalf("staged wasm answered %d of %d reference prompts", len(answers), len(probes))
	}
	live, static := 0, 0
	var wider []string
	for i, p := range probes {
		got := answers[i]
		if got.Name != p.Name {
			t.Fatalf("answer %d is for %q, expected %q", i, got.Name, p.Name)
		}
		if p.lacks != "" {
			static++
			if got.Error == "" {
				wider = append(wider, p.Name+" ("+p.lacks+")")
			}
			continue
		}
		live++
		if got.Error != "" {
			t.Errorf(`A STDLIB REFERENCE EDITOR FAILS IN THE BROWSER.

  prompt  %s
  body
%s

  error   %s

The page runs this editor when it scrolls into view. If the prompt needs
something the browser cannot do, its crossing needs a capability class
the js/wasm target lacks (internal/stdlibbindings, vmhost.TargetLacks), and
cmd/nomi-docgen then shows it as code it does not run. Otherwise: %s`,
				p.Name, indentPrompt(p.Source), got.Error, rebuildInstruction)
		}
	}
	if len(wider) > 0 {
		t.Logf("not run by the page, yet clean in the staged wasm (the walk counts what they could reach): %s", strings.Join(wider, ", "))
	}
	t.Logf("staged wasm ran %d reference prompts: %d live editors, %d the page does not run", len(probes), live, static)
}

func indentPrompt(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}
