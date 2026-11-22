package vmrunner_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmrunner"
)

// refused boots src held by opts and answers the *EffectError the boot
// refuses it with. The boot must refuse.
func refused(t *testing.T, src string, opts ...vmrunner.Option) *vmrunner.EffectError {
	t.Helper()
	var out bytes.Buffer
	in, err := vmrunner.Boot(webImage(t, src), &out, &out, nil, nil, opts...)
	if err == nil {
		in.Close()
		t.Fatalf("the boot admits a program its list does not allow:\n%s", src)
	}
	var ce *vmrunner.EffectError
	if !errors.As(err, &ce) {
		t.Fatalf("the boot fails with %T, not a *EffectError: %v", err, err)
	}
	if out.Len() > 0 {
		t.Fatalf("the refused program ran: %q", out.String())
	}
	return ce
}

const stampSource = `import std::instant.Instant

fn helper() -> Int
    _ = Instant.now()
    1
end

export fn stamp() -> Int
    helper()
end

export fn add(a: Int, b: Int) -> Int
    a + b
end
`

// An exported function that reads the clock is refused under a list
// without std::instant, naming the call, its site and how it is reached,
// in the words vmhost's host list uses; the same program loads when the
// list allows it.
func TestWithEffects_ARefusalNamesEachCall(t *testing.T) {
	ce := refused(t, stampSource, vmrunner.WithEffects("std::io"))
	if len(ce.Calls) != 1 {
		t.Fatalf("want one call, got %+v", ce.Calls)
	}
	c := ce.Calls[0]
	if c.Name != "std::instant" || c.Site != "main.nomi:4" || c.Callee != "instant.Instant.now" {
		t.Errorf("the call is %+v, want std::instant at main.nomi:4 calling instant.Instant.now", c)
	}
	if len(c.Chain) != 2 || c.Chain[0].Func != "stamp" || c.Chain[0].Edge != vm.EdgeRoot || c.Chain[1].Func != "helper" || c.Chain[1].Edge != vm.EdgeCall {
		t.Errorf("the chain is %+v, want stamp then helper", c.Chain)
	}
	want := "main.nomi:4: this program has the effect std::instant, and the host allows only std::io\n" +
		"main.nomi:4: help: reached from `stamp`; `stamp` calls `helper`\n" +
		"main.nomi:4: help: add \"std::instant\" to the names the host passes to vmrunner.WithEffects, or remove the call"
	if ce.Error() != want {
		t.Errorf("the refusal reads\n%s\nwant\n%s", ce, want)
	}

	var out bytes.Buffer
	in, err := vmrunner.Boot(webImage(t, stampSource), &out, &out, nil, nil, vmrunner.WithEffects("std::instant"))
	if err != nil {
		t.Fatalf("the list allows std::instant, and the boot refuses: %v", err)
	}
	defer in.Close()
	if got := call(t, in, "stamp", "[]"); got != "1" {
		t.Errorf("stamp = %s", got)
	}
}

// With no names the program may reach nothing outside, and the refusal
// says so; a program that reaches nothing outside loads and answers.
func TestWithEffects_NoNames(t *testing.T) {
	ce := refused(t, stampSource, vmrunner.WithEffects())
	if got, _, _ := strings.Cut(ce.Error(), "\n"); got != "main.nomi:4: this program has the effect std::instant, and the host allows no effects" {
		t.Errorf("the refusal's first line is %q", got)
	}

	pure := "export fn add(a: Int, b: Int) -> Int\n    a + b\nend\n"
	var out bytes.Buffer
	in, err := vmrunner.Boot(webImage(t, pure), &out, &out, nil, nil, vmrunner.WithEffects())
	if err != nil {
		t.Fatalf("a program that reaches nothing outside is refused under no names: %v", err)
	}
	defer in.Close()
	if got := call(t, in, "add", "[2, 3]"); got != "5" {
		t.Errorf("add = %s", got)
	}
}

// Every root a host can run is held: main, the boot and an exported
// function, each named with its own edge.
func TestWithEffects_EachRoot(t *testing.T) {
	cases := []struct {
		name, src, call string
		edge            vm.EdgeKind
	}{
		{"main", `import std::io

fn main()
    io.print("hi")
end
`, "std::io", vm.EdgeRoot},
		{"boot", `struct App
    greeting: String
end

fn boot(startup: Startup) -> Result<App, String>
    Ok(App{greeting: Map.get(startup.env, "GREETING") |> Maybe.with_default("hello")})
end

fn main()
    _ = App.greeting
end
`, "std::startup", vm.EdgeBoot},
		{"export", stampSource, "std::instant", vm.EdgeRoot},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ce := refused(t, c.src, vmrunner.WithEffects())
			got := ce.Calls[0]
			if got.Name != c.call || len(got.Chain) == 0 || got.Chain[0].Edge != c.edge {
				t.Errorf("the call is %+v, want %s from a root of kind %s", got, c.call, c.edge)
			}
			if c.edge == vm.EdgeBoot && !strings.HasSuffix(ce.Error(), "or have the boot take no Startup") {
				t.Errorf("the boot's refusal does not say to take no Startup:\n%s", ce)
			}
		})
	}
}

// A list that names nothing a list can allow fails the load, before the
// walk, in the words vmhost.WithEffects uses.
func TestWithEffects_BadNames(t *testing.T) {
	pure := webImage(t, "export fn add(a: Int, b: Int) -> Int\n    a + b\nend\n")
	cases := []struct {
		names []string
		want  string
	}{
		{[]string{"files"}, `vmrunner.WithEffects: "files" is not a std module; write one as an import does, such as "std::fs"`},
		// The runner links no front end and knows the std modules all the
		// same, so a misspelled one fails in vmhost.WithEffects' words.
		{[]string{"std::io", "std::fss"}, `vmrunner.WithEffects names std::fss, which is not a std module (did you mean "std::fs"?)`},
		{[]string{"std::nothing_like_it"}, `vmrunner.WithEffects names std::nothing_like_it, which is not a std module (name a std module as an import writes it, such as std::fs)`},
		{[]string{"std::io", "std::io"}, "vmrunner.WithEffects lists std::io twice"},
		{[]string{"go:example.com/pkg"}, `vmrunner.WithEffects: "go:example.com/pkg" is not a std module; write one as an import does, such as "std::fs"`},
		{[]string{"host:lookup"}, "vmrunner.WithEffects names host:lookup, which no table passed with vmrunner.WithHosts answers (no table is passed with vmrunner.WithHosts)"},
	}
	for _, c := range cases {
		var out bytes.Buffer
		in, err := vmrunner.Boot(pure, &out, &out, nil, nil, vmrunner.WithEffects(c.names...))
		if err == nil {
			in.Close()
			t.Errorf("%q: the boot admits the list", c.names)
			continue
		}
		if err.Error() != c.want {
			t.Errorf("%q: %v\nwant %s", c.names, err, c.want)
		}
	}
	// The reactor's "std" option is held the same way.
	var out bytes.Buffer
	in, err := vmrunner.Boot(pure, &out, &out, nil, nil, vmrunner.WithEffectsFrom(`nomi_load_with's "std"`, []string{"std::fss"}, `nomi_load_with's "host"`, nil))
	if err == nil {
		in.Close()
		t.Fatal("the boot admits the reactor's misspelled std name")
	}
	if want := `nomi_load_with's "std" names std::fss, which is not a std module (did you mean "std::fs"?)`; err.Error() != want {
		t.Errorf("the reactor's misspelled std name: %v\nwant %s", err, want)
	}
}

// Without the option nothing is held, and Run holds a run as Boot holds a
// load.
func TestWithEffects_RunIsHeld(t *testing.T) {
	src := "import std::io\n\nfn main()\n    io.print(\"hi\")\nend\n"
	image := webImage(t, src)
	var out, errOut bytes.Buffer
	if code := vmrunner.Run(image, &out, &errOut, nil, nil, false); code != 0 || out.String() != "hi\n" {
		t.Fatalf("unheld run: exit %d, %q %q", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := vmrunner.Run(image, &out, &errOut, nil, nil, false, vmrunner.WithEffects()); code != 1 || out.Len() > 0 ||
		!strings.Contains(errOut.String(), "this program has the effect std::io, and the host allows no effects") {
		t.Fatalf("held run: exit %d, %q %q", code, out.String(), errOut.String())
	}
}
