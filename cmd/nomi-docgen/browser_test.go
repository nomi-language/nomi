package main

import (
	"html"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
)

// A prompt that reaches a capability the browser lacks is shown as code the
// page does not run, never as a live editor: the browser build answers a
// listen or a dial with "a WebAssembly program in a browser has no sockets",
// so such an editor fails for every reader. The classification is the
// js/wasm build's own (vmhost.TargetLacks), from what the prompt reaches,
// std's calls on its behalf included. A prompt whose only network use is an
// HTTP request stays live, since the browser build sends it through fetch:
// std::http::testing.run's prompt calls `http.get`. So do two prompts the walk
// flags but whose run never reaches the operating system: process.run's
// starts its program under std::process::testing.run's fake, and
// std::mail::testing.rules reaches the SMTP mailer only through `Mailer.send`'s
// every impl.
//
// That the live editors do run in the browser is
// TestTourWasmAnswersTheReferenceEditors (vmhost), over the staged wasm.
func TestReferencePromptsTheBrowserCannotRunAreNotEditors(t *testing.T) {
	lib := std.Load()
	for _, tc := range []struct {
		module, label, lacks string
	}{
		{"mail/testing", "testing.with_smtp_server", "connect listen"},
		{"http/testing", "testing.with_server", "listen"},
		{"mail", "mail.smtp", "connect listen"},
		{"process", "process.run", ""},
		{"mail/testing", "testing.rules", ""},
		{"net", "net.lookup_host", "connect"},
		{"http/testing", "testing.run", ""},
	} {
		page := renderModuleWithLinks(lib, tc.module, nil)
		var found *referenceTest
		tests := referenceTestBodies(page, tc.module)
		for i := range tests {
			if tests[i].Label == tc.label {
				found = &tests[i]
				break
			}
		}
		if found == nil {
			t.Errorf("std/%s: no prompt labelled %q on its page", tc.module, tc.label)
			continue
		}
		if found.BrowserLacks != tc.lacks {
			t.Errorf("std/%s %s: the browser lacks %q, want %q", tc.module, tc.label, found.BrowserLacks, tc.lacks)
		}
		editor := strings.Contains(page, "<code class=\"language-nomi-test\">"+html.EscapeString(found.Body)+"</code>")
		if editor != (tc.lacks == "") {
			t.Errorf("std/%s %s: rendered as a live editor = %v, want %v", tc.module, tc.label, editor, tc.lacks == "")
		}
		if tc.lacks != "" && !strings.Contains(page, `<p class="nomi-ref-note">Not run here: this example`) {
			t.Errorf("std/%s %s: no note says why the page does not run it", tc.module, tc.label)
		}
	}
}
