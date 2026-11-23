package vmhost

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/rotation"
	"github.com/nomi-language/nomi/internal/vm"
)

// Large functions, a class of the run harness's inputs (run_fuzz_test.go).
// The generated programs there are small: a few bindings, and recursion
// only in helpers of two or three parameters. A program the VM runs wrong
// the same way on every machine that shares a path (the plain machine, the
// one copying every update and the built image all take the same self tail
// call) passes those comparisons, and nothing there knows what a literal
// should print.
//
// largeFunctionProgram writes one function, walk, of many parameters in
// every register bank (Ints, a big one among them, Strings, Lists), an Option,
// many locals bound to string literals, a lambda capturing a parameter, a
// lambda that calls walk in tail position, and a `case` nested in a `case`.
// walk loops by tail calls that rotate each bank's parameters, half of them
// direct self calls and half through the lambda, and answers an
// interpolation of every literal and parameter. The generator computes that
// answer as well, so a literal that reads wrong fails the run even when
// every machine agrees on it. TestLargeFunctionsRunAsWritten runs each
// program through runInput (the plain machine, a second run copying every
// update, the built image), then again on a debug machine, which takes the
// general tail transfer, and requires the generator's answer from both.

// largeFunctionCount is how many programs the fixed set and the rotating
// set check.
const largeFunctionCount = 40

// largeProgram is a generated program and the output its main prints.
type largeProgram struct {
	loweringSeed
	want string
}

// largeFunctionProgram is large-function program i.
func largeFunctionProgram(i int) largeProgram {
	r := rand.New(rand.NewSource(int64(i)))
	word := func() string {
		const letters = "abcdefghijklmnopqrstuvwxyz"
		var b strings.Builder
		for n := 2 + r.Intn(6); n > 0; n-- {
			b.WriteByte(letters[r.Intn(len(letters))])
		}
		return b.String()
	}
	// The thresholds a self tail call's fast path holds are eight words,
	// four strings and four references; each bank's count straddles its.
	nW, nS, nR, nL := r.Intn(12), r.Intn(8), r.Intn(8), r.Intn(40)

	var params, rotated []string
	bank := func(prefix, typ string, n int) {
		for j := 0; j < n; j++ {
			params = append(params, fmt.Sprintf("%s%d: %s", prefix, j, typ))
			rotated = append(rotated, fmt.Sprintf("%s%d", prefix, (j+1)%n))
		}
	}
	bank("w", "Int", nW)
	bank("s", "String", nS)
	bank("r", "List<Int>", nR)
	params = append(params, "m: Option<String>", "steps: Int")

	// segments are the answer's parts: a literal before each local and
	// parameter, which the answer interpolates, and what each prints.
	var src, want []string
	var locals []string
	for j := 0; j < nL; j++ {
		lit := word()
		locals = append(locals, fmt.Sprintf("    l%d = \"%s\"\n", j, lit))
		src = append(src, word())
		want = append(want, src[len(src)-1])
		src = append(src, fmt.Sprintf("${l%d}", j))
		want = append(want, lit)
	}
	for _, p := range params[:nW+nS+nR] {
		name := p[:strings.Index(p, ":")]
		lit := word() + " "
		src = append(src, lit+"${"+name+"}")
		want = append(want, lit, "\x00"+name)
	}
	some, none, lamA, lamB := word(), word(), word(), word()

	var b strings.Builder
	b.WriteString("import std::io\n\n")
	b.WriteString("fn walk(\n")
	for _, p := range params {
		b.WriteString("    " + p + ",\n")
	}
	b.WriteString(") -> String\n")
	for _, l := range locals {
		b.WriteString(l)
	}
	fmt.Fprintf(&b, "    mark = fn x: String -> \"%s ${x} %s\"\n\n", lamA, lamB)
	b.WriteString("    case steps\n")
	b.WriteString("        0 ->\n")
	b.WriteString("            case m\n")
	fmt.Fprintf(&b, "                Some(t) -> \"%s%s${mark(t)}\"\n", some, strings.Join(src, ""))
	fmt.Fprintf(&b, "                None    -> \"%s%s\"\n", none, strings.Join(src, ""))
	b.WriteString("            end\n\n")
	b.WriteString("        _ ->\n")
	call := strings.Join(append(append([]string{}, rotated...), "m"), ", ")
	fmt.Fprintf(&b, "            next = fn k: Int -> walk(%s, k)\n", call)
	fmt.Fprintf(&b, "            if steps %% 2 == 0 then next(steps - 1) else walk(%s, steps - 1)\n", call)
	b.WriteString("    end\n")
	b.WriteString("end\n\n")

	// Each run's arguments: Ints, one of them past int64 at times, words,
	// and short lists.
	ints := []string{"0", "7", "-3", "1024", "9223372036854775808", "-9223372036854775809", "4611686018427387904"}
	b.WriteString("fn main()\n")
	var out strings.Builder
	for run := 0; run < 3; run++ {
		values := map[string]string{}
		var args, shown []string
		for j := 0; j < nW; j++ {
			v := ints[r.Intn(len(ints))]
			args, shown = append(args, v), append(shown, v)
		}
		for j := 0; j < nS; j++ {
			v := word()
			args, shown = append(args, "\""+v+"\""), append(shown, v)
		}
		for j := 0; j < nR; j++ {
			var items []string
			for n := r.Intn(3); n > 0; n-- {
				items = append(items, fmt.Sprint(r.Intn(100)))
			}
			v := "[" + strings.Join(items, ", ") + "]"
			args, shown = append(args, v), append(shown, v)
		}
		steps := r.Intn(6)
		// After steps rotations, parameter j of a bank of n holds the
		// argument at (j + steps) % n.
		k := 0
		for _, n := range []int{nW, nS, nR} {
			for j := 0; j < n; j++ {
				name := params[k+j][:strings.Index(params[k+j], ":")]
				values[name] = shown[k+(j+steps)%n]
			}
			k += n
		}
		tag := ""
		option := "None"
		if r.Intn(2) == 0 {
			tag = word()
			option = "Some(\"" + tag + "\")"
		}
		fmt.Fprintf(&b, "    io.print(walk(%s))\n", strings.Join(append(args, option, fmt.Sprint(steps)), ", "))
		if tag != "" {
			out.WriteString(some)
		} else {
			out.WriteString(none)
		}
		for _, part := range want {
			if strings.HasPrefix(part, "\x00") {
				out.WriteString(values[part[1:]])
			} else {
				out.WriteString(part)
			}
		}
		if tag != "" {
			out.WriteString(lamA + " " + tag + " " + lamB)
		}
		out.WriteString("\n")
	}
	b.WriteString("end\n")
	return largeProgram{loweringSeed{name: fmt.Sprintf("large/%03d", i), src: b.String()}, out.String()}
}

func largeFunctionPrograms() []loweringSeed {
	out := make([]loweringSeed, largeFunctionCount)
	for i := range out {
		out[i] = largeFunctionProgram(i).loweringSeed
	}
	return out
}

// checkLargeFunction runs g on the run harness's machines and on a debug
// machine, and requires the generator's answer from each.
func checkLargeFunction(t *testing.T, g largeProgram, failure string) (ran bool) {
	t.Helper()
	want := g.want + "\n--- ok: <nil>"
	o := runInput(t.TempDir(), g.src)
	if o.class != runOK {
		t.Errorf("%s: %sclass %s, want ok%s", g.name, failure, o.class, o.describe(g.src))
		return false
	}
	if o.transcript != want {
		t.Errorf("%s: %sprints, on the plain machine:\n%s\nwant:\n%s\nprogram:\n%s", g.name, failure, o.transcript, want, g.src)
		return true
	}
	restore := vm.DebugEveryMachine()
	d := runInput(t.TempDir(), g.src)
	restore()
	if d.transcript != want {
		t.Errorf("%s: %sprints, on a debug machine:\n%s\nwant:\n%s\nprogram:\n%s", g.name, failure, d.transcript, want, g.src)
	}
	return true
}

// TestLargeFunctionsRunAsWritten runs the fixed set of large-function
// programs.
func TestLargeFunctionsRunAsWritten(t *testing.T) {
	ran := 0
	for i := 0; i < largeFunctionCount; i++ {
		if checkLargeFunction(t, largeFunctionProgram(i), "") {
			ran++
		}
	}
	if ran != largeFunctionCount {
		t.Errorf("%d of %d large-function programs ran", ran, largeFunctionCount)
	}
	t.Logf("%d large-function programs ran on four machines each", ran)
}

// TestLargeFunctionsRunAsWrittenRotating runs largeFunctionCount programs
// from a start seed that changes every UTC day (internal/rotation).
func TestLargeFunctionsRunAsWrittenRotating(t *testing.T) {
	set := rotation.For(t, "./vmhost", largeFunctionCount)
	for _, seed := range set.Seeds() {
		checkLargeFunction(t, largeFunctionProgram(int(seed)), set.Failure(seed))
	}
}
