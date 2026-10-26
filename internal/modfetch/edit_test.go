package modfetch

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAddLocal_WritesRequireAndReplace(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	app, _ := setup(t, p)
	lib := filepath.Join(app, "macros", "csvdata")
	writeFile(t, filepath.Join(lib, "go.mod"), "module csvdata\n\ngo 1.27.0\n\nrequire example.com/strutil v1.0.0\n")
	writeFile(t, filepath.Join(lib, "nomi.toml"), "[module]\nname = \"csvdata\"\n")

	added, err := AddLocal(app, lib, Options{})
	if err != nil {
		t.Fatalf("AddLocal: %v", err)
	}
	if added.Module.Path != "csvdata" || added.Module.Version != "v0.0.0" || added.Replace != "./macros/csvdata" {
		t.Fatalf("AddLocal answered %+v", added)
	}
	goMod := readFile(t, filepath.Join(app, "go.mod"))
	for _, want := range []string{
		"\tcsvdata v0.0.0\n",
		"\texample.com/strutil v1.0.0 // indirect\n",
		"replace csvdata => ./macros/csvdata\n",
	} {
		if !strings.Contains(goMod, want) {
			t.Errorf("go.mod lacks %q:\n%s", want, goMod)
		}
	}
	if goSum := readFile(t, filepath.Join(app, "go.sum")); !strings.Contains(goSum, "example.com/strutil v1.0.0 ") {
		t.Errorf("go.sum lacks what the local module requires:\n%s", goSum)
	}

	// A sibling directory is written relative to the project root.
	sib := filepath.Join(filepath.Dir(app), "shared")
	writeFile(t, filepath.Join(sib, "go.mod"), "module example.com/shared\n\ngo 1.27.0\n")
	writeFile(t, filepath.Join(sib, "nomi.toml"), "[module]\nname = \"shared\"\n")
	if _, err := AddLocal(app, sib, Options{}); err != nil {
		t.Fatalf("AddLocal of a sibling: %v", err)
	}
	if goMod := readFile(t, filepath.Join(app, "go.mod")); !strings.Contains(goMod, "replace example.com/shared => ../shared\n") {
		t.Errorf("go.mod lacks the sibling's replace:\n%s", goMod)
	}
}

func TestAddLocal_RefusesADirectoryThatIsNotANomiModule(t *testing.T) {
	p := newTestProxy(t)
	app, _ := setup(t, p)
	before := readFile(t, filepath.Join(app, "go.mod"))

	noGoMod := filepath.Join(app, "nogomod")
	writeFile(t, filepath.Join(noGoMod, "nomi.toml"), "[module]\nname = \"nogomod\"\n")
	_, err := AddLocal(app, noGoMod, Options{})
	if err == nil || !strings.Contains(err.Error(), "has no go.mod") || !strings.Contains(err.Error(), "nomi init <module path>") {
		t.Errorf("AddLocal of a directory with no go.mod: %v", err)
	}

	noManifest := filepath.Join(app, "gocode")
	writeFile(t, filepath.Join(noManifest, "go.mod"), "module example.com/gocode\n\ngo 1.27.0\n")
	_, err = AddLocal(app, noManifest, Options{})
	if err == nil || !strings.Contains(err.Error(), "has no nomi.toml, so it is not a Nomi module") {
		t.Errorf("AddLocal of a directory with no nomi.toml: %v", err)
	}

	_, err = AddLocal(app, filepath.Join(app, "missing"), Options{})
	if err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("AddLocal of a missing directory: %v", err)
	}
	if after := readFile(t, filepath.Join(app, "go.mod")); after != before {
		t.Errorf("a refused AddLocal changed go.mod:\n%s", after)
	}
}

func TestAdd_APackagePathAddsItsModule(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	app, _ := setup(t, p)
	added, err := Add(app, "example.com/strutil/inner", Options{})
	if err != nil {
		t.Fatalf("Add of a package path: %v", err)
	}
	if added.Module.Path != "example.com/strutil" {
		t.Fatalf("Add of a package path added %s", added.Module.Path)
	}
}

func TestRemove_DropsTheRequirementAndWhatOnlyItNeeded(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	p.publish(t, "example.com/other", "v1.0.0", map[string]string{
		"go.mod":     "module example.com/other\n\ngo 1.22\n",
		"nomi.toml":  "[module]\nname = \"other\"\n",
		"other.nomi": "pub fn one() -> Int {\n    1\n}\n",
	})
	app, _ := setup(t, p)
	for _, m := range []string{"example.com/greet@v0.1.0", "example.com/other@v1.0.0"} {
		if _, err := Add(app, m, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Remove(app, "example.com/greet")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if removed.Module.Version != "v0.1.0" || len(removed.Unneeded) != 1 || removed.Unneeded[0].Path != "example.com/strutil" {
		t.Fatalf("Remove answered %+v", removed)
	}
	goMod := readFile(t, filepath.Join(app, "go.mod"))
	if strings.Contains(goMod, "greet") || strings.Contains(goMod, "strutil") || !strings.Contains(goMod, "example.com/other v1.0.0") {
		t.Errorf("go.mod after Remove:\n%s", goMod)
	}
	goSum := readFile(t, filepath.Join(app, "go.sum"))
	if strings.Contains(goSum, "greet") || strings.Contains(goSum, "strutil") || !strings.Contains(goSum, "example.com/other v1.0.0 ") {
		t.Errorf("go.sum after Remove:\n%s", goSum)
	}

	if _, err := Remove(app, "example.com/greet"); err == nil || !strings.Contains(err.Error(), "go.mod does not require example.com/greet") {
		t.Errorf("Remove of a module go.mod does not require: %v", err)
	}
}

func TestRemove_KeepsAnIndirectRequirementAnotherModuleNeeds(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0")
	p.publish(t, "example.com/loud", "v1.0.0", map[string]string{
		"go.mod":    "module example.com/loud\n\ngo 1.22\n\nrequire example.com/strutil v1.0.0\n",
		"nomi.toml": "[module]\nname = \"loud\"\n",
		"loud.nomi": "pub fn one() -> Int {\n    1\n}\n",
	})
	app, _ := setup(t, p)
	for _, m := range []string{"example.com/greet@v0.1.0", "example.com/loud@v1.0.0"} {
		if _, err := Add(app, m, Options{}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Remove(app, "example.com/greet")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Unneeded) != 0 {
		t.Fatalf("Remove dropped %v, which example.com/loud still requires", removed.Unneeded)
	}
	if goMod := readFile(t, filepath.Join(app, "go.mod")); !strings.Contains(goMod, "example.com/strutil v1.0.0 // indirect") {
		t.Errorf("go.mod lost a requirement another module needs:\n%s", goMod)
	}
}

func TestRemove_TakesOutALocalModulesReplace(t *testing.T) {
	p := newTestProxy(t)
	app, _ := setup(t, p)
	lib := filepath.Join(app, "lib")
	writeFile(t, filepath.Join(lib, "go.mod"), "module lib\n\ngo 1.27.0\n")
	writeFile(t, filepath.Join(lib, "nomi.toml"), "[module]\nname = \"lib\"\n")
	if _, err := AddLocal(app, lib, Options{}); err != nil {
		t.Fatal(err)
	}
	removed, err := Remove(app, "lib")
	if err != nil {
		t.Fatal(err)
	}
	if removed.Replace != "./lib" {
		t.Errorf("Remove answered replace %q", removed.Replace)
	}
	if goMod := readFile(t, filepath.Join(app, "go.mod")); strings.Contains(goMod, "lib") {
		t.Errorf("go.mod after removing a local module:\n%s", goMod)
	}
}

func TestUpdate_StaysWithinTheModulePath(t *testing.T) {
	p := newTestProxy(t)
	publishGreet(t, p, "v0.1.0", "v0.2.0", "v0.3.0-rc.1")
	p.publish(t, "example.com/strutil", "v1.1.0", map[string]string{
		"go.mod":     "module example.com/strutil\n\ngo 1.22\n",
		"strutil.go": "package strutil\n",
	})
	// A new major version is another module path, which update never takes.
	p.publish(t, "example.com/greet/v2", "v2.0.0", map[string]string{
		"go.mod":     "module example.com/greet/v2\n\ngo 1.22\n",
		"nomi.toml":  "[module]\nname = \"greet\"\n",
		"greet.nomi": "pub fn hello() -> Int {\n    2\n}\n",
	})
	app, _ := setup(t, p)
	if _, err := Add(app, "example.com/greet@v0.1.0", Options{}); err != nil {
		t.Fatal(err)
	}

	changes, err := Update(app, "example.com/greet", Options{})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(changes) != 1 || changes[0] != (Change{Path: "example.com/greet", From: "v0.1.0", To: "v0.2.0"}) {
		t.Fatalf("Update of one module answered %+v", changes)
	}
	goSum := readFile(t, filepath.Join(app, "go.sum"))
	if strings.Contains(goSum, "example.com/greet v0.1.0 h1:") || !strings.Contains(goSum, "example.com/greet v0.2.0 h1:") {
		t.Errorf("go.sum after Update:\n%s", goSum)
	}

	changes, err = Update(app, "", Options{})
	if err != nil {
		t.Fatalf("Update of every requirement: %v", err)
	}
	if len(changes) != 1 || changes[0] != (Change{Path: "example.com/strutil", From: "v1.0.0", To: "v1.1.0"}) {
		t.Fatalf("Update of every requirement answered %+v", changes)
	}
	goMod := readFile(t, filepath.Join(app, "go.mod"))
	for _, want := range []string{"require example.com/greet v0.2.0\n", "require example.com/strutil v1.1.0 // indirect\n"} {
		if !strings.Contains(goMod, want) {
			t.Errorf("go.mod lacks %q:\n%s", want, goMod)
		}
	}

	if changes, err := Update(app, "", Options{}); err != nil || len(changes) != 0 {
		t.Errorf("a second Update answered %+v, %v", changes, err)
	}
	if _, err := Update(app, "example.com/none", Options{}); err == nil || !strings.Contains(err.Error(), "go.mod does not require example.com/none") {
		t.Errorf("Update of a module go.mod does not require: %v", err)
	}
}
