// Command runner runs Nomi's benchmark suite and writes benchmarks/RESULTS.md.
//
// `benchmarks/run` starts it. It builds `nomi` from this checkout and the
// Go baselines in benchmarks/game/go, then for each benchmark:
//
//  1. checks the Nomi program's output at the benchmark's check size: a
//     Benchmarks Game program against the published output file in
//     benchmarks/game/expected, an Are We Fast Yet program by its own
//     verification (it prints "verified" and exits 0);
//  2. builds it with `nomi build`;
//  3. times it at its fixed size under `nomi run` and as the built
//     executable, and a Benchmarks Game program's Go baseline too. Each
//     timing is the median wall time of -runs runs after -warmup runs. The
//     first warm-up run's output is checked: a Benchmarks Game program's
//     against its Go baseline's output at the same size.
//
// The Go baselines run with GOMAXPROCS=1, since every Nomi program here runs
// on one task: the ratio compares the same work on one core. Several of the
// Go programs spread their work over goroutines and would be faster with
// every core.
//
// The programs that read a FASTA file (k-nucleotide, regex-redux,
// reverse-complement) read one the Go fasta program writes at the input size
// the benchmark names.
//
// Flags: -runs, -warmup, -only (a regexp over benchmark names), -out (the
// results file; "-" prints it), -check (output checks only, no timing).
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
)

type suite string

const (
	game suite = "Benchmarks Game"
	awfy suite = "Are We Fast Yet"
)

type benchmark struct {
	suite suite
	name  string
	// file is the program under benchmarks/game or benchmarks/awfy.
	file string
	// checkArgs and checkFasta are the check size; expected names the
	// published output in benchmarks/game/expected.
	checkArgs  []string
	checkFasta int
	expected   string
	// args and fasta are the timed size. fasta, when set, is the size of the
	// FASTA input the program reads on stdin.
	args  []string
	fasta int
	// goName is the Go baseline's directory under benchmarks/game/go.
	goName string
}

// The fixed sizes. Each is picked so the Nomi program runs for a few
// seconds; the Benchmarks Game's own sizes would take minutes to hours.
var benchmarks = []benchmark{
	{suite: game, name: "binary-trees", file: "binarytrees", checkArgs: []string{"10"}, expected: "binarytrees-output.txt", args: []string{"16"}, goName: "binarytrees"},
	{suite: game, name: "fannkuch-redux", file: "fannkuchredux", checkArgs: []string{"7"}, expected: "fannkuchredux-output.txt", args: []string{"9"}, goName: "fannkuchredux"},
	{suite: game, name: "fasta", file: "fasta", checkArgs: []string{"1000"}, expected: "fasta-output.txt", args: []string{"250000"}, goName: "fasta"},
	{suite: game, name: "k-nucleotide", file: "knucleotide", checkFasta: 1000, expected: "knucleotide-output.txt", fasta: 25000, goName: "knucleotide"},
	{suite: game, name: "mandelbrot", file: "mandelbrot", checkArgs: []string{"200"}, expected: "mandelbrot-output.txt", args: []string{"1000"}, goName: "mandelbrot"},
	{suite: game, name: "n-body", file: "nbody", checkArgs: []string{"1000"}, expected: "nbody-output.txt", args: []string{"200000"}, goName: "nbody"},
	{suite: game, name: "pidigits", file: "pidigits", checkArgs: []string{"30"}, expected: "pidigits-output.txt", args: []string{"10000"}, goName: "pidigits"},
	{suite: game, name: "regex-redux", file: "regexredux", checkFasta: 1000, expected: "regexredux-output.txt", fasta: 500000, goName: "regexredux"},
	{suite: game, name: "reverse-complement", file: "revcomp", checkFasta: 1000, expected: "revcomp-output.txt", fasta: 1000000, goName: "revcomp"},
	{suite: game, name: "spectral-norm", file: "spectralnorm", checkArgs: []string{"100"}, expected: "spectralnorm-output.txt", args: []string{"400"}, goName: "spectralnorm"},

	{suite: awfy, name: "Bounce", file: "bounce", checkArgs: []string{"1", "1"}, args: []string{"1", "200"}},
	{suite: awfy, name: "List", file: "list", checkArgs: []string{"1", "1"}, args: []string{"1", "300"}},
	{suite: awfy, name: "Mandelbrot", file: "mandelbrot", checkArgs: []string{"1", "1"}, args: []string{"1", "750"}},
	{suite: awfy, name: "NBody", file: "nbody", checkArgs: []string{"1", "1"}, args: []string{"1", "250000"}},
	{suite: awfy, name: "Permute", file: "permute", checkArgs: []string{"1", "1"}, args: []string{"1", "200"}},
	{suite: awfy, name: "Queens", file: "queens", checkArgs: []string{"1", "1"}, args: []string{"1", "100"}},
	{suite: awfy, name: "Sieve", file: "sieve", checkArgs: []string{"1", "1"}, args: []string{"1", "10"}},
	{suite: awfy, name: "Storage", file: "storage", checkArgs: []string{"1", "1"}, args: []string{"1", "100"}},
	{suite: awfy, name: "Towers", file: "towers", checkArgs: []string{"1", "1"}, args: []string{"1", "100"}},
	{suite: awfy, name: "CD", file: "cd", checkArgs: []string{"1", "10"}, args: []string{"1", "250"}},
	{suite: awfy, name: "DeltaBlue", file: "deltablue", checkArgs: []string{"1", "1"}, args: []string{"1", "3000"}},
	{suite: awfy, name: "Havlak", file: "havlak", checkArgs: []string{"1", "1"}, args: []string{"1", "1"}},
	{suite: awfy, name: "Json", file: "json", checkArgs: []string{"1", "1"}, args: []string{"1", "50"}},
	{suite: awfy, name: "Richards", file: "richards", checkArgs: []string{"1", "1"}, args: []string{"1", "10"}},
}

type result struct {
	bench benchmark
	run   time.Duration
	built time.Duration
	goRun time.Duration
	err   error
}

func main() {
	runs := flag.Int("runs", 5, "timed runs per measurement; the median is reported")
	warmup := flag.Int("warmup", 1, "untimed runs before the timed ones")
	only := flag.String("only", "", "run only the benchmarks whose name matches this regexp")
	out := flag.String("out", "benchmarks/RESULTS.md", `the results file, "-" for stdout`)
	checkOnly := flag.Bool("check", false, "check each program's output and time nothing")
	flag.Parse()
	if *warmup < 1 {
		*warmup = 1 // the first warm-up run is the output check
	}

	root, err := repoRoot()
	if err != nil {
		fail(err)
	}
	var filter *regexp.Regexp
	if *only != "" {
		if filter, err = regexp.Compile(*only); err != nil {
			fail(err)
		}
	}

	work, err := os.MkdirTemp("", "nomi-bench-")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(work)

	r := &runner{root: root, work: work, runs: *runs, warmup: *warmup, loadBefore: loadAverage()}
	if err := r.setup(); err != nil {
		fail(err)
	}

	var results []result
	failed := false
	for _, b := range benchmarks {
		if filter != nil && !filter.MatchString(b.name) {
			continue
		}
		res := r.measure(b, *checkOnly)
		if res.err != nil {
			failed = true
			fmt.Fprintf(os.Stderr, "%-20s FAILED: %v\n", b.name, res.err)
		} else if *checkOnly {
			fmt.Fprintf(os.Stderr, "%-20s ok\n", b.name)
		} else {
			fmt.Fprintf(os.Stderr, "%-20s run %s  built %s  go %s\n", b.name, secs(res.run), secs(res.built), secs(res.goRun))
		}
		results = append(results, res)
	}

	if !*checkOnly {
		r.loadAfter = loadAverage()
		report := r.report(results)
		if *out == "-" {
			fmt.Print(report)
		} else if err := os.WriteFile(filepath.Join(root, *out), []byte(report), 0o644); err != nil {
			fail(err)
		}
	}
	if failed {
		os.Exit(1)
	}
}

type runner struct {
	root, work   string
	runs, warmup int
	nomi         string
	goBin        string
	fastaFiles   map[int]string
	// loadBefore and loadAfter are the one-minute load average when the run
	// started and ended, so a reader can tell a run on a busy machine.
	loadBefore, loadAfter string
}

// setup builds nomi from this checkout and the Go baselines.
func (r *runner) setup() error {
	r.nomi = filepath.Join(r.work, "nomi")
	fmt.Fprintln(os.Stderr, "building nomi and the Go baselines ...")
	if err := run(r.root, nil, "go", "build", "-o", r.nomi, "./cmd/nomi"); err != nil {
		return err
	}
	r.goBin = filepath.Join(r.work, "go")
	if err := os.MkdirAll(r.goBin, 0o755); err != nil {
		return err
	}
	if err := run(filepath.Join(r.root, "benchmarks/game/go"), nil, "go", "build", "-o", r.goBin+string(filepath.Separator), "./..."); err != nil {
		return err
	}
	r.fastaFiles = map[int]string{}
	return nil
}

// fastaInput is the path of a FASTA file of size n, written by the Go fasta
// baseline the first time it is asked for.
func (r *runner) fastaInput(n int) (string, error) {
	if path, ok := r.fastaFiles[n]; ok {
		return path, nil
	}
	path := filepath.Join(r.work, fmt.Sprintf("fasta-%d.txt", n))
	out, err := capture(r.root, nil, "", filepath.Join(r.goBin, "fasta"), fmt.Sprint(n))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", err
	}
	r.fastaFiles[n] = path
	return path, nil
}

func (r *runner) program(b benchmark) string {
	dir := "game"
	if b.suite == awfy {
		dir = "awfy"
	}
	return filepath.Join(r.root, "benchmarks", dir, b.file+".nomi")
}

func (r *runner) measure(b benchmark, checkOnly bool) result {
	res := result{bench: b}
	res.err = r.check(b)
	if res.err != nil || checkOnly {
		return res
	}

	exe := filepath.Join(r.work, b.suite.short()+"-"+b.file)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if err := run(r.root, nil, r.nomi, "build", r.program(b), "-o", exe); err != nil {
		res.err = fmt.Errorf("nomi build: %w", err)
		return res
	}

	stdin := ""
	if b.fasta > 0 {
		var err error
		if stdin, err = r.fastaInput(b.fasta); err != nil {
			res.err = err
			return res
		}
	}

	var want []byte
	if b.goName != "" {
		goArgs := b.args
		if b.fasta > 0 {
			goArgs = []string{"0"}
		}
		goCmd := append([]string{filepath.Join(r.goBin, b.goName)}, goArgs...)
		out, d, err := r.time(goCmd, stdin, []string{"GOMAXPROCS=1"})
		if err != nil {
			res.err = fmt.Errorf("go: %w", err)
			return res
		}
		want, res.goRun = out, d
	}

	runCmd := append([]string{r.nomi, "run", r.program(b)}, b.args...)
	out, d, err := r.time(runCmd, stdin, nil)
	if err == nil {
		err = verify(b, out, want)
	}
	if err != nil {
		res.err = fmt.Errorf("nomi run: %w", err)
		return res
	}
	res.run = d

	out, d, err = r.time(append([]string{exe}, b.args...), stdin, nil)
	if err == nil {
		err = verify(b, out, want)
	}
	if err != nil {
		res.err = fmt.Errorf("built: %w", err)
		return res
	}
	res.built = d
	return res
}

// check runs the program at its check size and compares its output with the
// published output, or with its own verification.
func (r *runner) check(b benchmark) error {
	stdin := ""
	if b.checkFasta > 0 {
		var err error
		if stdin, err = r.fastaInput(b.checkFasta); err != nil {
			return err
		}
	}
	out, err := capture(r.root, nil, stdin, r.nomi, append([]string{"run", r.program(b)}, b.checkArgs...)...)
	if err != nil {
		return fmt.Errorf("check run: %w", err)
	}
	if b.suite == awfy {
		return verify(b, out, nil)
	}
	want, err := os.ReadFile(filepath.Join(r.root, "benchmarks/game/expected", b.expected))
	if err != nil {
		return err
	}
	if !bytes.Equal(out, want) {
		return fmt.Errorf("output at the check size differs from %s", b.expected)
	}
	return nil
}

// verify checks a timed run's output: an Are We Fast Yet program reports
// its own verification, and a Benchmarks Game program's output matches its
// Go baseline's at the same size.
func verify(b benchmark, out, want []byte) error {
	if b.suite == awfy {
		if !bytes.Contains(out, []byte(" verified\n")) {
			return fmt.Errorf("did not verify:\n%s", out)
		}
		return nil
	}
	if !bytes.Equal(out, want) {
		return fmt.Errorf("output differs from the Go baseline's")
	}
	return nil
}

// time runs argv warm-up times, checking the output of the first, then
// runs it r.runs times and returns the first output and the median time.
func (r *runner) time(argv []string, stdin string, env []string) ([]byte, time.Duration, error) {
	var first []byte
	for i := 0; i < r.warmup; i++ {
		out, err := capture(r.root, env, stdin, argv[0], argv[1:]...)
		if err != nil {
			return nil, 0, err
		}
		if i == 0 {
			first = out
		}
	}
	var times []time.Duration
	for i := 0; i < r.runs; i++ {
		start := time.Now()
		if err := runTo(io.Discard, r.root, env, stdin, argv[0], argv[1:]...); err != nil {
			return nil, 0, err
		}
		times = append(times, time.Since(start))
	}
	return first, median(times), nil
}

func median(times []time.Duration) time.Duration {
	if len(times) == 0 {
		return 0
	}
	slices.Sort(times)
	n := len(times)
	if n%2 == 1 {
		return times[n/2]
	}
	return (times[n/2-1] + times[n/2]) / 2
}

func (s suite) short() string {
	if s == game {
		return "game"
	}
	return "awfy"
}

func (r *runner) report(results []result) string {
	var b strings.Builder
	commit, _ := capture(r.root, nil, "", "git", "rev-parse", "--short", "HEAD")
	goVersion, _ := capture(r.root, nil, "", "go", "version")
	b.WriteString("# Benchmark results\n\n")
	b.WriteString("Written by `benchmarks/run`; do not edit by hand. Each time is the median wall time of ")
	fmt.Fprintf(&b, "%d runs after %d warm-up run(s), in seconds. ", r.runs, r.warmup)
	b.WriteString("`nomi run` includes loading the program, from the artifact cache after the warm-up; the built executable is the program `nomi build` wrote. ")
	b.WriteString("Go is the Benchmarks Game's Go program at the same size, run with `GOMAXPROCS=1` (the Nomi programs run on one task). ")
	b.WriteString("The ratios are Nomi's time over Go's. Sizes are fixed so a Nomi run takes seconds; they are far below the Benchmarks Game's published sizes, so the Go times include more of Go's process start than at full size. ")
	b.WriteString("How each program is written, and where the time goes, are in benchmarks/README.md and benchmarks/FINDINGS.md.\n\n")
	fmt.Fprintf(&b, "- Commit: `%s`\n", strings.TrimSpace(string(commit)))
	fmt.Fprintf(&b, "- Machine: %s\n", machine())
	fmt.Fprintf(&b, "- Go: `%s`\n", strings.TrimSpace(string(goVersion)))
	if r.loadBefore != "" {
		fmt.Fprintf(&b, "- Load average (1 min): %s at the start, %s at the end\n", r.loadBefore, r.loadAfter)
	}
	fmt.Fprintf(&b, "- Date: %s\n\n", time.Now().UTC().Format("2006-01-02"))

	for _, s := range []suite{game, awfy} {
		var rows []result
		for _, res := range results {
			if res.bench.suite == s {
				rows = append(rows, res)
			}
		}
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", s)
		if s == game {
			b.WriteString("| Benchmark | Size | `nomi run` | Built | Go | Run / Go | Built / Go |\n|---|---|---:|---:|---:|---:|---:|\n")
		} else {
			b.WriteString("| Benchmark | Iterations, inner | `nomi run` | Built |\n|---|---|---:|---:|\n")
		}
		for _, res := range rows {
			size := strings.Join(res.bench.args, ", ")
			if res.bench.fasta > 0 {
				size = fmt.Sprintf("fasta %d", res.bench.fasta)
			}
			if res.err != nil {
				fmt.Fprintf(&b, "| %s | %s | failed: %s |\n", res.bench.name, size, firstLine(res.err.Error()))
				continue
			}
			if s == game {
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", res.bench.name, size,
					secs(res.run), secs(res.built), secs(res.goRun), ratio(res.run, res.goRun), ratio(res.built, res.goRun))
			} else {
				fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", res.bench.name, size, secs(res.run), secs(res.built))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func machine() string {
	parts := []string{runtime.GOOS + "/" + runtime.GOARCH}
	if runtime.GOOS == "darwin" {
		if cpu, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
			parts = append(parts, strings.TrimSpace(string(cpu)))
		}
	}
	parts = append(parts, fmt.Sprintf("%d CPUs", runtime.NumCPU()))
	return strings.Join(parts, ", ")
}

// loadAverage is the one-minute load average, or "" where it cannot be read.
func loadAverage() string {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output()
		if err != nil {
			return ""
		}
		fields := strings.Fields(strings.Trim(strings.TrimSpace(string(out)), "{}"))
		if len(fields) > 0 {
			return fields[0]
		}
	case "linux":
		data, err := os.ReadFile("/proc/loadavg")
		if err != nil {
			return ""
		}
		if fields := strings.Fields(string(data)); len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

func secs(d time.Duration) string { return fmt.Sprintf("%.3f", d.Seconds()) }

func ratio(a, b time.Duration) string {
	if b == 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f×", a.Seconds()/b.Seconds())
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil && bytes.HasPrefix(data, []byte("module github.com/nomi-language/nomi\n")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside the nomi repository")
		}
		dir = parent
	}
}

func run(dir string, env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func capture(dir string, env []string, stdin string, name string, args ...string) ([]byte, error) {
	var out bytes.Buffer
	err := runTo(&out, dir, env, stdin, name, args...)
	return out.Bytes(), err
}

func runTo(w io.Writer, dir string, env []string, stdin string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = w
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if stdin != "" {
		f, err := os.Open(stdin)
		if err != nil {
			return err
		}
		defer f.Close()
		cmd.Stdin = f
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w\n%s", strings.Join(append([]string{filepath.Base(name)}, args...), " "), err, stderr.String())
	}
	return nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "runner:", err)
	os.Exit(1)
}
