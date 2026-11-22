package virtualproject

import (
	"strings"
	"testing"
)

// A `(base64)` section is a file that is not text: its bytes arrive as
// they were, trailing newlines and bytes that are not UTF-8 included, in
// a block of one module and in a block of modules alike, and a section
// that is not base64, or a Nomi file marked base64, is an error.
func TestFromMarkedSourceDecodesABase64Section(t *testing.T) {
	data := []byte("\x00asm\x01\x00\x00\x00\xff\n\n")
	data = append(data, make([]byte, 100)...)
	body := Base64Body(data)
	if lines := strings.Split(strings.TrimSuffix(body, "\n"), "\n"); len(lines) != 2 || len(lines[0]) != 76 || len(lines[1]) != 72 {
		t.Errorf("the body of 111 bytes is %d lines: %q", len(lines), lines)
	}
	single, err := FromMarkedSource("// FILE: main.nomi\nfn main() end\n\n// FILE: engine/x.wasm (base64)\n" + body + "\n\n// FILE: notes.txt\nhi\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := single.DataFiles["engine/x.wasm"]; got != string(data) {
		t.Errorf("the block's binary file is %q", got)
	}
	if got := single.DataFiles["notes.txt"]; got != "hi\n" {
		t.Errorf("the text file after it is %q", got)
	}
	modules, err := FromMarkedSource("// FILE: main.nomi\nfn main() end\n\n// FILE: nomi.toml\n[module]\nname = \"a\"\n\n[dependencies]\n\n// FILE: engine/x.wasm  (base64)\n" + body)
	if err != nil {
		t.Fatal(err)
	}
	if got := modules.Files["engine/x.wasm"]; got != string(data) {
		t.Errorf("the module's binary file is %q", got)
	}
	if names := FileNames("// FILE: a.wasm (base64)\nAA==\n// FILE: b.txt\n"); strings.Join(names, " ") != "a.wasm b.txt" {
		t.Errorf("FileNames answers %v", names)
	}
	for src, msg := range map[string]string{
		"// FILE: main.nomi\nfn main() end\n// FILE: x.bin (base64)\nnot base64!\n": "// FILE: x.bin is marked (base64), and its section is not base64",
		"// FILE: main.nomi (base64)\nAA==\n":                                       "// FILE: main.nomi is marked (base64), and a Nomi file or a .toml file is text",
	} {
		_, err := FromMarkedSource(src)
		if err == nil || !strings.HasPrefix(err.Error(), msg) {
			t.Errorf("%q answers %v, want %q", src, err, msg)
		}
	}
}
