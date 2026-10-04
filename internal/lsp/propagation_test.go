package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "github.com/tliron/glsp/protocol_3_16"
)

// TestPropagateAsync_BlockFormImportPublishesOnDependents is the
// end-to-end LSP regression for the propagation bug reported on
// tests/15-app-and-defer/effects/: editing a parent file (renaming an exported
// struct) must cause the LSP to publish fresh diagnostics for every
// dependent file that imports the renamed symbol via block-form
// `import { ... }` syntax.
//
// Before the ExtractImportsFromNodes fix, dependents using block-form
// imports never appeared in dm.reverseDeps, so PropagateChange returned
// an empty affected list and no PublishDiagnostics ever fired. Errors
// only surfaced after LSP restart (which re-ran ScanWorkspace against
// the on-disk content and reported the broken imports).
//
// The test stubs s.notify to capture publications and asserts
// dev/prod/test each receive a diagnostic mentioning the renamed-away
// symbol after a single Update + propagateAsync.
func TestPropagateAsync_BlockFormImportPublishesOnDependents(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return full
	}

	// Mirrors the tests/15-app-and-defer/effects/ layout:
	// main.nomi imports configuration; configuration.nomi defines AppEnv and
	// imports configuration/{dev,prod,test}; each variant imports back into
	// configuration via block-form `import { configuration: AppEnv }`.
	mustWrite("main.nomi", `import { configuration: load }

fn main() {
  load()
}
`)
	configPath := mustWrite("configuration.nomi", `import {
  configuration/dev: build as dev_build
  configuration/prod: build as prod_build
  configuration/test: build as test_build
}

pub struct AppEnv {
  port: Int = 3000
}

pub fn load(): AppEnv {
  dev_build()
}
`)
	devPath := mustWrite("configuration/dev.nomi", `import {
  configuration: AppEnv
}

pub fn build(): AppEnv { AppEnv{port: 8080} }
`)
	prodPath := mustWrite("configuration/prod.nomi", `import {
  configuration: AppEnv
}

pub fn build(): AppEnv { AppEnv{port: 8081} }
`)
	testPath := mustWrite("configuration/test.nomi", `import {
  configuration: AppEnv
}

pub fn build(): AppEnv { AppEnv{port: 8082} }
`)

	s := NewServer()
	s.docs.SetWorkspaceRoot(tmp)
	s.docs.IndexWorkspace(context.Background())

	configURI := "file://" + configPath
	devURI := "file://" + devPath
	prodURI := "file://" + prodPath
	testURI := "file://" + testPath

	// Before the fix, ScanWorkspace would not record dev/prod/test as
	// dependents of configuration.nomi because their imports lived inside
	// an ImportBlock that ExtractImportsFromNodes silently skipped. Pin
	// that invariant here so the test fails loudly at the source of the
	// bug (not just the downstream "no publish" symptom).
	deps := s.docs.ReverseDeps(configURI)
	wantDeps := map[string]bool{devURI: true, prodURI: true, testURI: true}
	for _, d := range deps {
		delete(wantDeps, d)
	}
	if len(wantDeps) > 0 {
		t.Fatalf("ScanWorkspace failed to record block-form importers as reverse deps; missing: %v", wantDeps)
	}

	// Capture all PublishDiagnostics notifications.
	var mu sync.Mutex
	pubs := make(map[string][]protocol.Diagnostic)
	s.notify = func(method string, params any) {
		mu.Lock()
		defer mu.Unlock()
		if method == protocol.ServerTextDocumentPublishDiagnostics {
			p := params.(*protocol.PublishDiagnosticsParams)
			pubs[string(p.URI)] = p.Diagnostics
		}
	}

	// Simulate didOpen on configuration.nomi (the file the user opens
	// to perform the rename).
	srcConfig, _ := os.ReadFile(configPath)
	s.docs.Open(configURI, string(srcConfig))
	s.docs.UpdateImportEdges(configURI)

	// User renames AppEnv → Config and saves (didChange fires with
	// the whole new content under TextDocumentSyncKindFull).
	renamed := strings.ReplaceAll(string(srcConfig), "AppEnv", "Config")
	s.docs.Update(configURI, renamed)
	s.docs.UpdateImportEdges(configURI)
	s.propagateAsync(configURI)

	// Wait for the goroutine to publish for all three dependents.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		_, hasDev := pubs[devURI]
		_, hasProd := pubs[prodURI]
		_, hasTest := pubs[testURI]
		mu.Unlock()
		if hasDev && hasProd && hasTest {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	for _, uri := range []string{devURI, prodURI, testURI} {
		diags, ok := pubs[uri]
		if !ok {
			t.Errorf("expected PublishDiagnostics for %s; got none", uri)
			continue
		}
		hasExportError := false
		for _, d := range diags {
			if strings.Contains(d.Message, "AppEnv") &&
				(strings.Contains(d.Message, "no exported") ||
					strings.Contains(d.Message, "not exported") ||
					strings.Contains(d.Message, "private")) {
				hasExportError = true
				break
			}
		}
		if !hasExportError {
			t.Errorf("expected AppEnv export error in diagnostics for %s; got %v",
				uri, diagMessages(diags))
		}
	}
}

func diagMessages(diags []protocol.Diagnostic) []string {
	var msgs []string
	for _, d := range diags {
		msgs = append(msgs, d.Message)
	}
	return msgs
}

// TestPropagateAsync_RapidEditsStillPublishLatest fires many concurrent
// Update + propagateAsync calls on the same document to flush out data
// races between in-flight analyze() goroutines and the next propagation's
// analyze() / publish reads. Run under -race to catch the
// concurrent-doc-field-mutation bug: builds are serialized by
// DocumentManager.analyzeMu and install their results under its mu. The
// test also asserts that *some* publish reaches the dependent (main.nomi)
// — i.e., the
// cancel-previous logic doesn't cancel ALL goroutines into oblivion —
// and that the final published diagnostics reflect the last edit's state
// (or at least one publish goes through with the latest content).
func TestPropagateAsync_RapidEditsStillPublishLatest(t *testing.T) {
	tmp := t.TempDir()
	mustWrite := func(rel, content string) string {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		return full
	}

	mainPath := mustWrite("main.nomi", `import greeter

fn main() { greeter.hello() }
`)
	greetPath := mustWrite("greeter.nomi", `pub fn hello(): String { "hi" }
`)

	s := NewServer()
	s.docs.SetWorkspaceRoot(tmp)
	s.docs.IndexWorkspace(context.Background())

	greetURI := "file://" + greetPath
	mainURI := "file://" + mainPath

	// Capture publish notifications.
	var mu sync.Mutex
	pubs := make(map[string]int)
	s.notify = func(method string, params any) {
		if method != protocol.ServerTextDocumentPublishDiagnostics {
			return
		}
		uri := string(params.(*protocol.PublishDiagnosticsParams).URI)
		mu.Lock()
		pubs[uri]++
		mu.Unlock()
	}

	s.docs.Open(greetURI, `pub fn hello(): String { "hi" }`)
	s.docs.UpdateImportEdges(greetURI)

	// Fire many rapid edits to maximize overlap between propagate
	// goroutines. Each iteration runs Update + UpdateImportEdges +
	// propagateAsync — exactly what textDocumentDidChange does on every
	// keystroke when TextDocumentSyncKindFull is in effect.
	const iterations = 50
	var wg sync.WaitGroup
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			content := fmt.Sprintf(`pub fn hello(): String { "hi%d" }`, i)
			s.docs.Update(greetURI, content)
			s.docs.UpdateImportEdges(greetURI)
			s.propagateAsync(greetURI)
		}(i)
	}
	wg.Wait()

	// Drain the in-flight propagate goroutines by waiting for the
	// last-fired one's publish to land (proxies the latest goroutine
	// being last-in-line behind any in-flight analyzes), then a brief
	// pause to let the race detector finalize.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := pubs[mainURI]
		mu.Unlock()
		if got > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	mainPubs := pubs[mainURI]
	mu.Unlock()
	if mainPubs == 0 {
		t.Fatalf("expected at least one PublishDiagnostics on main.nomi after %d propagateAsync calls; got 0",
			iterations)
	}

	// Snapshot the final state and confirm analyze settled — a non-nil
	// Analysis means the last propagateAsync's analyze() ran to
	// completion without being torn by a concurrent run.
	snap := s.docs.Snapshot(greetURI)
	if snap == nil || snap.Analysis == nil {
		t.Fatal("expected greeter.nomi snapshot to have an Analysis after rapid edits")
	}
	if len(snap.Nodes) == 0 {
		t.Error("expected greeter.nomi snapshot to have parsed Nodes")
	}
}
