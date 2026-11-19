package vmhost_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/rt"
	"github.com/nomi-language/nomi/vmhost"
)

// refusedByHost loads src with opts, which hold it to a list, and answers
// the *CapabilityError the load refuses it with. The load must refuse.
func refusedByHost(t *testing.T, src string, opts ...vmhost.Option) *vmhost.CapabilityError {
	t.Helper()
	_, err := vmhost.LoadSource("main", src, opts...)
	if err == nil {
		t.Fatalf("the load admits a program its list does not allow:\n%s", src)
	}
	var ce *vmhost.CapabilityError
	if !errors.As(err, &ce) {
		t.Fatalf("the load fails with %T, not a *CapabilityError: %v", err, err)
	}
	var ds vmhost.Diagnostics
	if !errors.As(err, &ds) || len(ds) != len(ce.Calls) {
		t.Fatalf("the refusal's diagnostics are not one per call: %v", err)
	}
	return ce
}

// firstLine is the first line of err's text.
func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")
	return line
}

// With no names the host allows nothing outside the program: a load that
// prints, reads a file, reads the clock or takes a Startup is refused,
// naming the call, before anything runs.
func TestWithCapabilities_NoNamesRefusesEachOutsideCall(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"output", `import std::io

fn main()
    io.print("hi")
end
`, "main.nomi:4:5: this program reaches std::io, and the host allows nothing outside the program"},
		{"files", `import
    std::fs
    std::io
end

fn main() -> Result<Unit, io.Error>
    _ = fs.read_text("notes.txt")?
    Ok(Unit)
end
`, "main.nomi:7:9: this program reaches std::fs, and the host allows nothing outside the program"},
		{"clock", `import std::instant.Instant

fn main()
    _ = Instant.now()
end
`, "main.nomi:4:9: this program reaches std::instant, and the host allows nothing outside the program"},
		{"startup", `struct App
    greeting: String
end

fn boot(startup: Startup) -> Result<App, String>
    Ok(App{greeting: Map.get(startup.env, "GREETING") |> Maybe.with_default("hello")})
end

fn main()
    _ = App.greeting
end
`, "main.nomi:5:4: this program reaches std::startup, and the host allows nothing outside the program"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ce := refusedByHost(t, c.src, vmhost.WithCapabilities())
			if got := firstLine(ce); got != c.want {
				t.Errorf("refusal:\n%s\nwant first line:\n%s", ce, c.want)
			}
			if !ce.Calls[0].Host || len(ce.Calls[0].Chain) == 0 {
				t.Errorf("the call is not over the host's list with a chain: %+v", ce.Calls[0])
			}
		})
	}
}

// Allowing std::io admits the program that prints, and it runs.
func TestWithCapabilities_AllowingANameAdmitsIt(t *testing.T) {
	p, err := vmhost.LoadSource("main", "import std::io\n\nfn main()\n    io.print(\"hi\")\nend\n", vmhost.WithCapabilities("std::io"))
	if err != nil {
		t.Fatalf("the host allows std::io, and the load refuses: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil || out.String() != "hi\n" {
		t.Fatalf("run: %q, %v", out.String(), err)
	}
}

const pureRule = `struct Order
    items: Int
    loyal: Bool
end

fn discount(order: Order) -> Decimal
    if order.loyal and order.items >= 10 then
        0.15d
    else if order.loyal then
        0.05d
    else
        0.00d
    end
end
`

// A rule that reaches nothing outside loads under no names and answers a
// value through Call; the same rule with a network call is refused at the
// load, though no main reaches it, since a host may call any function.
func TestWithCapabilities_AnEmbeddedRule(t *testing.T) {
	p, err := vmhost.LoadSource("main", pureRule, vmhost.WithCapabilities())
	if err != nil {
		t.Fatalf("a pure rule is refused under no names: %v", err)
	}
	v, err := p.Call(context.Background(), "discount", vmhost.Fields{"items": 12, "loyal": true})
	if err != nil {
		t.Fatal(err)
	}
	if got := rt.DisplayText(v); got != "0.15" {
		t.Errorf("discount = %s, want 0.15", got)
	}

	withFetch := `import
    std::http
    std::url.Url
end

` + strings.Replace(pureRule, "    if order.loyal and", "    _ = http.get(Url\"https://rates.example.com/today\")\n    if order.loyal and", 1)
	ce := refusedByHost(t, withFetch, vmhost.WithCapabilities())
	if got, want := firstLine(ce), "main.nomi:12:9: this program reaches std::http, and the host allows nothing outside the program"; got != want {
		t.Errorf("refusal:\n%s\nwant first line:\n%s", ce, want)
	}
	n := ce.Calls[0]
	if n.Name != "std::http" || n.Site != "main.nomi:12:17" || len(n.Chain) != 1 || n.Chain[0].Func != "discount" {
		t.Errorf("the call as data: %s at %s through %v", n.Name, n.Site, n.Chain)
	}
	if !strings.Contains(ce.Error(), `add "std::http" to the names the host passes to vmhost.WithCapabilities, or remove the call`) {
		t.Errorf("the refusal says nothing of how to allow it:\n%s", ce)
	}
}

// Call holds the function it calls, and the boot, to the lists: a program
// held by its nomi.toml and loaded without the option refuses a Call of a
// function that reaches past them, and still calls one that does not.
func TestWithCapabilities_CallIsHeld(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"nomi.toml": "[module]\nname = \"app\"\n\n[capabilities.self]\nstd = []\n",
		"rules.nomi": `import std::io

pub fn quiet(n: Int) -> Int
    n + 1
end

pub fn loud(n: Int) -> Int
    io.print("loud")
    n + 1
end
`,
	}
	writeFiles(t, dir, files)
	var out bytes.Buffer
	p, err := vmhost.Load(filepath.Join(dir, "rules.nomi"), vmhost.WithOutput(&out))
	if err != nil {
		t.Fatalf("the load holds no function a host has not called yet: %v", err)
	}
	if v, err := p.Call(context.Background(), "quiet", 1); err != nil || rt.DisplayText(v) != "2" {
		t.Fatalf("quiet(1) = %v, %v", v, err)
	}
	_, err = p.Call(context.Background(), "loud", 1)
	var ce *vmhost.CapabilityError
	if !errors.As(err, &ce) || ce.Calls[0].Name != "std::io" || ce.Calls[0].Host {
		t.Fatalf("Call of a function over nomi.toml's list: %v", err)
	}
	if out.Len() > 0 {
		t.Fatalf("the refused call printed %q", out.String())
	}
}

// The host's list and nomi.toml's both hold the program: each refuses
// what the other allows.
func TestWithCapabilities_AndTheManifestEachNarrow(t *testing.T) {
	main := `import
    std::fs
    std::io
end

fn main() -> Result<Unit, io.Error>
    io.print(fs.read_text("notes.txt")?)
    Ok(Unit)
end
`
	load := func(allow string, opts ...vmhost.Option) error {
		dir := t.TempDir()
		writeFiles(t, dir, map[string]string{
			"nomi.toml": "[module]\nname = \"app\"\nentry_points = [\"main\"]\n\n[capabilities.self]\nstd = [" + allow + "]\n",
			"main.nomi": main,
		})
		_, err := vmhost.Load(filepath.Join(dir, "main.nomi"), opts...)
		if err != nil {
			return errors.New(strings.ReplaceAll(err.Error(), dir+string(filepath.Separator), ""))
		}
		return nil
	}
	if err := load(`"std::io", "std::fs"`, vmhost.WithCapabilities("std::io", "std::fs")); err != nil {
		t.Fatalf("both lists allow the program: %v", err)
	}
	err := load(`"std::io", "std::fs"`, vmhost.WithCapabilities("std::io"))
	if err == nil || firstLine(err) != "main.nomi:7:14: this program reaches std::fs, and the host allows only std::io" {
		t.Errorf("the host narrows what nomi.toml allows: %v", err)
	}
	err = load(`"std::io"`, vmhost.WithCapabilities("std::io", "std::fs"))
	if err == nil || firstLine(err) != "main.nomi:7:14: this module's own code reaches std::fs, and [capabilities.self] allows it only std::io" {
		t.Errorf("nomi.toml narrows what the host allows: %v", err)
	}
}

// A function a host table answers is named host: and its key, which the
// host allows or not like any other name. A host may call `shout` itself,
// so its declaration is reached as a root and reported first.
func TestWithCapabilities_HostFunctions(t *testing.T) {
	table := func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
		return map[string]hostadapt.Func{"shout": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) { return args[0], nil }}, nil
	}
	src := "host fn shout(s: String) -> String\n\nfn main()\n    _ = shout(\"hi\")\nend\n"
	if _, err := vmhost.LoadSource("main", src, vmhost.WithHosts(table), vmhost.WithCapabilities("host:shout")); err != nil {
		t.Fatalf("the host allows its own function, and the load refuses: %v", err)
	}
	ce := refusedByHost(t, src, vmhost.WithHosts(table), vmhost.WithCapabilities())
	if got, want := firstLine(ce), "main.nomi:1:9: this program reaches host:shout, and the host allows nothing outside the program"; got != want {
		t.Errorf("refusal:\n%s\nwant first line:\n%s", ce, want)
	}
}

// A name a list may not hold fails the load, in the words a nomi.toml
// entry's problem has.
func TestWithCapabilities_NamesThatAreNotNames(t *testing.T) {
	cases := []struct {
		names []string
		want  string
	}{
		{[]string{"std::fss"}, `vmhost.WithCapabilities names std::fss, which is not a std module (did you mean "std::fs"?)`},
		{[]string{"fs"}, `vmhost.WithCapabilities: "fs" is not a std module; write one as an import does, such as "std::fs"`},
		{[]string{"go:strings"}, `vmhost.WithCapabilities: "go:strings" names a Go package; Go is allowed by module, not by package: pass the module that binds it to vmhost.WithGo`},
		{[]string{"host:nothing"}, "vmhost.WithCapabilities names host:nothing, which no table passed with vmhost.WithHosts answers (no table is passed with vmhost.WithHosts)"},
		{[]string{"std::io", "std::io"}, "vmhost.WithCapabilities lists std::io twice"},
	}
	for _, c := range cases {
		_, err := vmhost.LoadSource("main", "fn main()\nend\n", vmhost.WithCapabilities(c.names...))
		if err == nil || err.Error() != c.want {
			t.Errorf("WithCapabilities(%q): %v\nwant: %s", c.names, err, c.want)
		}
	}
}

// writeFiles writes files, by slash-separated path, under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
