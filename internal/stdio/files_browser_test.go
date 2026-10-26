//go:build js && wasm

package stdio

import (
	"context"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// Run under js/wasm by TestBrowserBuild_FilesStayInMemory: a js/wasm build's
// file system is the process's in-memory one, so a program reaches no disk,
// and what it writes it reads back.
func TestBrowser_FilesStayInMemory(t *testing.T) {
	fr := rt.NewFrame(context.Background())
	if r := ReadText(fr, "/etc/hosts"); r.Tag != rt.TagErr || r.Err != (rt.IOError{Tag: rt.TagIONotFound, Path: "/etc/hosts"}) {
		t.Fatalf("ReadText(/etc/hosts) = %+v, want NotFound: the browser build has no disk", r)
	}
	if r := Write(fr, "notes.txt", rt.Bytes("hi"), int64(Replace)); r.Tag != rt.TagOk {
		t.Fatalf("Write = %+v", r)
	}
	if r := ReadText(fr, "/work/notes.txt"); r.Tag != rt.TagOk || r.Ok != "hi" {
		t.Fatalf("ReadText = %+v, want the text written", r)
	}
	if got := ProcessMemory().Files()["/work/notes.txt"]; string(got) != "hi" {
		t.Fatalf("the process's memory holds %q", got)
	}
}
