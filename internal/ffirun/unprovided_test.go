package ffirun

import (
	"path/filepath"
	"strings"
	"testing"
)

const unprovidedBinding = `gopkg "github.com/netshade/showmi" as go_http

pub fn hello() -> String go go_http.Hello
`

const unprovidedGoCode = "package go_http\n\nfunc Hello() string { return \"hi\" }\n"

// TestUnprovided_NamesTheCommandThatFixesIt: a `gopkg` no go.mod provides
// is explained by where the Go code and the project files are, and each
// explanation names the `nomi` command that fixes it, in the run's
// validation and in the checker's problem and hint alike.
func TestUnprovided_NamesTheCommandThatFixesIt(t *testing.T) {
	t.Setenv("NOMI_FFIRUN_CACHE_ROOT", t.TempDir())
	t.Setenv("NOMI_TOOLCHAIN_ROOT", t.TempDir())
	cases := []struct {
		name  string
		files map[string]string
		file  string // the declaring file, under the case's root
		want  string // the validation message after the location
	}{
		{
			name:  "Go code beside the file, no project",
			files: map[string]string{"go_http.go": unprovidedGoCode},
			file:  "go_http.nomi",
			want: `Go package "github.com/netshade/showmi" is not in the Go standard library, and the Go code beside this file is not in a Nomi project yet; ` +
				"run `nomi init github.com/netshade/showmi` in this file's directory to make it one " +
				"(it writes nomi.toml, and a go.mod whose module path github.com/netshade/showmi names that Go code)",
		},
		{
			name:  "Go code beside the file, nomi.toml and no go.mod",
			files: map[string]string{"go_http.go": unprovidedGoCode, "nomi.toml": "[module]\nname = \"showmi\"\n"},
			file:  "go_http.nomi",
			want: `Go package "github.com/netshade/showmi" is not in the Go standard library, and this project has a nomi.toml but no go.mod to name the Go code beside this file; ` +
				"run `nomi init github.com/netshade/showmi` in this file's directory to add the go.mod",
		},
		{
			name: "Go code in a subdirectory, nomi.toml above and no go.mod",
			files: map[string]string{
				"showmi/go_http.go": unprovidedGoCode,
				"nomi.toml":         "[module]\nname = \"showmi\"\n",
			},
			file: "showmi/go_http.nomi",
			want: "run `nomi init github.com/netshade` in the directory that holds nomi.toml to add the go.mod",
		},
		{
			name:  "no Go code, no project",
			files: map[string]string{},
			file:  "go_http.nomi",
			want: `Go package "github.com/netshade/showmi" is not in the Go standard library, and no go.mod above the declaring file provides it; ` +
				"make this file's directory a project with `nomi init <module path>`, then run `nomi add github.com/netshade/showmi`",
		},
		{
			name:  "no Go code, nomi.toml and no go.mod",
			files: map[string]string{"nomi.toml": "[module]\nname = \"app\"\n"},
			file:  "go_http.nomi",
			want: `Go package "github.com/netshade/showmi" is not in the Go standard library, and this project has a nomi.toml but no go.mod to require its module; ` +
				"run `nomi init <module path>` in this file's directory, then `nomi add github.com/netshade/showmi`",
		},
		{
			name: "Go code beside the file, another module's go.mod above",
			files: map[string]string{
				"go.mod":            "module example.com/outer\n\ngo 1.26.3\n",
				"app/go_http.go":    unprovidedGoCode,
				"app/unrelated.txt": "",
			},
			file: "app/go_http.nomi",
			want: `Go package "github.com/netshade/showmi" is outside the Go module example.com/outer of the go.mod at <root>, and the Go code beside this file is not in a Nomi project of its own; ` +
				"run `nomi init github.com/netshade/showmi` in this file's directory to make it one, " +
				"or write `gopkg \"example.com/outer/app\"` to bind that Go code as a package of example.com/outer",
		},
		{
			name: "Go code beside the file inside the project",
			files: map[string]string{
				"go.mod":         "module example.com/outer\n\ngo 1.26.3\n",
				"nomi.toml":      "[module]\nname = \"outer\"\n",
				"app/go_http.go": unprovidedGoCode,
			},
			file: "app/go_http.nomi",
			want: `Go package "github.com/netshade/showmi" is outside the project's own Go module example.com/outer, the Go standard library, and every module its go.mod requires or replaces; ` +
				"the Go code beside this file is package example.com/outer/app: write `gopkg \"example.com/outer/app\"` to bind it, " +
				"or run `nomi add github.com/netshade/showmi` if github.com/netshade/showmi is a package of another module",
		},
		{
			name: "a dependency the project lacks",
			files: map[string]string{
				"go.mod":    "module example.com/outer\n\ngo 1.26.3\n",
				"nomi.toml": "[module]\nname = \"outer\"\n",
			},
			file: "go_http.nomi",
			want: `Go package "github.com/netshade/showmi" is outside the project's own Go module example.com/outer, the Go standard library, and every module its go.mod requires or replaces; ` +
				"run `nomi add github.com/netshade/showmi` to add its module",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := noGoModRoot(t)
			for name, text := range c.files {
				mustWriteHelper(t, filepath.Join(root, name), text)
			}
			file := filepath.Join(root, c.file)
			mustWriteHelper(t, file, unprovidedBinding)
			want := strings.ReplaceAll(c.want, "<root>", root)
			_, err := Prepare(file)
			if err == nil {
				t.Fatal("Prepare accepted the binding")
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Prepare's error\n%v\nwant it to hold\n%s", err, want)
			}
			why, fix := GoImportProblemFix(file, "github.com/netshade/showmi")
			if got := `Go package "github.com/netshade/showmi" is ` + why + "; " + fix; !strings.Contains(got, want) {
				t.Errorf("the checker's problem and hint\n%s\nwant them to hold\n%s", got, want)
			}
		})
	}
}
