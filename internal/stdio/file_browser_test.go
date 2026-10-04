//go:build js && wasm

package stdio

import (
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// Run under js/wasm by TestBrowserBuild_FileAccessRefuses.
func TestBrowser_FileAccessRefuses(t *testing.T) {
	if r := ReadFile("/etc/hosts"); r.Tag != rt.TagErr || r.Err.Reason != BrowserReason {
		t.Fatalf("ReadFile = %+v, want the browser refusal", r)
	}
	if r := WriteFile("/tmp/x", "y"); r.Tag != rt.TagErr || r.Err.Reason != BrowserReason {
		t.Fatalf("WriteFile = %+v, want the browser refusal", r)
	}
}
