package vmhost

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A program's lowering and its run do not depend on which programs the
// process loaded before it. Two child processes load the same sample of
// corpus, tour, spec and generated programs, one in order and one in
// reverse, and every program's declines and run transcript must agree.
// Within the forward process each program follows the ones before it, and
// within the reverse one the ones after it, so a cache or table that one
// load leaves behind and a later load reads shows up as a difference. The
// two processes also differ in Go's map iteration seeds.
//
// NOMI_ORDER_COUNT=<n> changes the sample's size (default 40).
func TestLoadOrderDoesNotChangeAProgram(t *testing.T) {
	if mode := os.Getenv("NOMI_ORDER_CHILD"); mode != "" {
		loadOrderChild(t, mode, os.Getenv("NOMI_ORDER_OUT"))
		return
	}
	dir := t.TempDir()
	results := map[string]map[string]string{}
	for _, mode := range []string{"forward", "reverse"} {
		out := filepath.Join(dir, mode+".json")
		cmd := exec.Command(os.Args[0], "-test.run=^TestLoadOrderDoesNotChangeAProgram$", "-test.count=1", "-test.timeout=0")
		cmd.Env = append(os.Environ(), "NOMI_ORDER_CHILD="+mode, "NOMI_ORDER_OUT="+out)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the %s child failed: %v\n%s", mode, err, b)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		results[mode] = m
	}
	fwd, rev := results["forward"], results["reverse"]
	if len(fwd) != len(rev) || len(fwd) == 0 {
		t.Fatalf("forward checked %d programs, reverse %d", len(fwd), len(rev))
	}
	names := make([]string, 0, len(fwd))
	for name := range fwd {
		names = append(names, name)
	}
	sort.Strings(names)
	differ := 0
	for _, name := range names {
		if fwd[name] != rev[name] {
			differ++
			t.Errorf("%s differs with load order\nforward:\n%s\nreverse:\n%s", name, fwd[name], rev[name])
		}
	}
	t.Logf("%d programs, %d differ", len(names), differ)
}

// loadOrderSample is every k-th seed, so the sample spans the corpus, tour,
// spec and generated programs.
func loadOrderSample(t *testing.T) []loweringSeed {
	all := append(loweringSeeds(t), generatedPrograms()...)
	n := 40
	if s := os.Getenv("NOMI_ORDER_COUNT"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 {
			t.Fatalf("NOMI_ORDER_COUNT=%q is not a positive count", s)
		}
		n = v
	}
	// Always in the sample: libraries' impls for std receivers, which the
	// impl indexes key by a receiver no program file declares.
	// And a program whose front end builds and runs a macro, whose
	// build and expansion are cached for the process.
	fixed := []loweringSeed{
		{name: "local type argument impls", src: localTypeArgumentProgram},
		{name: "a macro's expansion", src: macroLoadOrderProgram},
	}
	if n >= len(all) {
		return append(all, fixed...)
	}
	step := len(all) / n
	out := make([]loweringSeed, 0, n+len(fixed))
	for i := 0; i < len(all) && len(out) < n; i += step {
		out = append(out, all[i])
	}
	return append(out, fixed...)
}

func loadOrderChild(t *testing.T, mode, outPath string) {
	seeds := loadOrderSample(t)
	order := make([]int, len(seeds))
	for i := range order {
		order[i] = i
		if mode == "reverse" {
			order[i] = len(seeds) - 1 - i
		}
	}
	got := map[string]string{}
	base := t.TempDir()
	for _, i := range order {
		s := seeds[i]
		// The seed's forward position names its directory in both
		// children, so a path in a diagnostic does not differ by order.
		dir := filepath.Join(base, fmt.Sprintf("p%04d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		got[s.name] = strings.ReplaceAll(loadOrderFingerprint(dir, s.src), base, "<base>")
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// loadOrderFingerprint is what one load decides: every decline the builder
// took (body and first reason), the program's unsupported diagnostic, and
// its run's outcome and transcript.
func loadOrderFingerprint(dir, src string) string {
	entry, err := writeInput(dir, src)
	if err != nil {
		return "not an input: " + err.Error()
	}
	var b strings.Builder
	func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(&b, "panic: %v\n", r)
			}
		}()
		p, err := Load(entry, WithOutput(io.Discard), WithErrorOutput(io.Discard))
		if err != nil {
			fmt.Fprintf(&b, "load: %v\n", err)
			return
		}
		fns := make([]string, 0, len(p.declines))
		for fn := range p.declines {
			fns = append(fns, fn)
		}
		sort.Strings(fns)
		for _, fn := range fns {
			fmt.Fprintf(&b, "decline %s: %s\n", fn, p.declines[fn])
		}
		if u := p.Unsupported(); u != nil {
			fmt.Fprintf(&b, "unsupported: %v\n", u)
		}
	}()
	o := runInput(dir, src)
	if o.once {
		// A clock, randomness or concurrency: its transcript is its own
		// to vary (a supervisor's jittered retry delay), so only the
		// outcome's class is compared.
		fmt.Fprintf(&b, "run: %s, nondeterministic\n", o.class)
		return b.String()
	}
	fmt.Fprintf(&b, "run: %s skip=%q\n%s\n", o.class, o.skip, o.transcript)
	return b.String()
}
