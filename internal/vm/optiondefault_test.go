package vm_test

import (
	"os"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

// `Option.with_default` and `Result.with_default` of a fused Option or Result
// (optionfuse.go): each test runs a program with the default taken from the
// registers and with the intrinsic called on the record, and requires the
// same transcript.

// defaultBothWays runs src with fused defaults and without, requires each
// function of fused to compile opDefaultM only in the first, requires the
// same transcript and answers it.
func defaultBothWays(t *testing.T, src string, fused []string) string {
	t.Helper()
	run := func(want bool) string {
		p := depthProgram(t, src)
		for _, fn := range fused {
			listing, err := p.Disassemble(fn)
			if err != nil {
				t.Fatalf("%s: %v", fn, err)
			}
			if got := opcodesOf(listing)["defaultm"]; got != want {
				t.Fatalf("%s takes a default from registers: %v, want %v:\n%s", fn, got, want, listing)
			}
		}
		return inlineRun(t, src, nil)
	}
	with := run(true)
	t.Setenv(vm.NoFusedDefaultEnv, "1")
	without := run(false)
	os.Unsetenv(vm.NoFusedDefaultEnv)
	if with != without {
		t.Fatalf("from registers:\n%s\nfrom the record:\n%s", with, without)
	}
	return with
}

// A default taken from a crossing's fused answer builds no Some and boxes no
// default: a loop reading a vector and a byte with defaults allocates
// nothing per iteration, where the intrinsic allocates the Some and the
// boxed default.
func TestOptionFuse_ADefaultBuildsNothing(t *testing.T) {
	const src = `import std::io

fn down(n: Int, v: Vector<Int>, data: Bytes) -> Int
    if n < 1 then
        0
    else
        x = Vector.get(v, n %% 4) |> Option.with_default(-1)
        y = Bytes.uint8_at(data, n %% 4) |> Option.with_default(-1)
        down(n - 1, v, data) + x + y
    end
end

fn main()
    down(%d, Vector[1000, 2000, 3000], Bytes.from_list([1, 2, 3])) |> io.print()
end
`
	with := allocsPerActivation(t, src)
	t.Setenv(vm.NoFusedDefaultEnv, "1")
	without := allocsPerActivation(t, src)
	os.Unsetenv(vm.NoFusedDefaultEnv)
	if with > 0.1 {
		t.Errorf("one activation allocates %.2f times with its defaults taken from registers, want 0", with)
	}
	if without < 1 {
		t.Errorf("one activation allocates %.2f times through the intrinsic, so the count above measures nothing", without)
	}
	defaultBothWays(t, strings.ReplaceAll(strings.ReplaceAll(src, "%d", "9"), "%%", "%"), []string{"down"})
}

// A default answers what the intrinsic answers for every payload bank (an
// Int, a big Int beside its shadow, a Float, a String, a List), for a Some
// and a None, an Ok and an Err, and for a Result a Nomi function returns.
func TestOptionFuse_ADefaultAnswersAsTheIntrinsic(t *testing.T) {
	got := defaultBothWays(t, `import std::io

fn half(n: Int) -> Result<Int, String>
    if n % 2 == 0 then Ok(n / 2) else Err("odd")
end

fn show(i: Int) -> String
    ints = Vector[7, 8]
    bigs = Vector[100000000000000000000, 200000000000000000000]
    floats = Vector[1.5, 2.5]
    words = {"a" => "apple"}
    lists = Vector[[1, 2]]
    keys = Vector["a", "b"]
    key = Vector.get(keys, i) |> Option.with_default("z")
    a = Vector.get(ints, i) |> Option.with_default(-1)
    b = Vector.get(bigs, i) |> Option.with_default(300000000000000000000)
    c = Vector.get(floats, i) |> Option.with_default(0.25)
    d = Map.get(words, key) |> Option.with_default("none")
    e = Vector.get(lists, i) |> Option.with_default([9])
    f = half(i) |> Result.with_default(-2)
    "${a} ${b} ${c} ${d} ${e} ${f}"
end

fn main()
    io.print(show(0))
    io.print(show(1))
    io.print(show(2))
end
`, []string{"show"})
	want := "7 100000000000000000000 1.5 apple [1, 2] 0\n" +
		"8 200000000000000000000 2.5 none [9] -2\n" +
		"-1 300000000000000000000 0.25 none [9] 1\n"
	if got != want {
		t.Fatalf("transcript:\n%s\nwant:\n%s", got, want)
	}
}
