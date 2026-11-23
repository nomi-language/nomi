package vm_test

import (
	"os"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
)

// Reads of a value's storage that a fused crossing answers in the dispatch
// loop (optionfuse.go's fastPaths, frame.readFused): each test runs a
// program with the reads in the loop and with each read made as its
// crossing (NoFastReadsEnv), and requires the same transcript.

// readsBothWays runs src with the reads in the loop and as crossings,
// requires function fn to make reads of its reads in the loop in the first
// and none in the second, requires the same transcript and answers it.
func readsBothWays(t *testing.T, src, fn string, reads int) string {
	t.Helper()
	run := func(want int) string {
		listing, err := depthProgram(t, src).Disassemble(fn)
		if err != nil {
			t.Fatalf("%s: %v", fn, err)
		}
		if got := strings.Count(listing, "read in the loop"); got != want {
			t.Fatalf("%s makes %d reads in the loop, want %d:\n%s", fn, got, want, listing)
		}
		return inlineRun(t, src, nil)
	}
	with := run(reads)
	t.Setenv(vm.NoFastReadsEnv, "1")
	without := run(0)
	os.Unsetenv(vm.NoFastReadsEnv)
	if with != without {
		t.Fatalf("read in the loop:\n%s\nas crossings:\n%s", with, without)
	}
	return with
}

// Every read the loop runs answers what its crossing answers: each width,
// byte order and signedness, Bytes.get and Vector.get, at the first and
// last index that fit, past the end, before the start and at an index past
// int64, a payload nothing reads, and an unsigned 64-bit Int past int64,
// which the crossing answers. All seventeen reads in all run in the loop.
func TestFastReads_AnswerAsTheirCrossings(t *testing.T) {
	got := readsBothWays(t, `import std::io

fn show(m: Option<Int>) -> String
    case m
        Some(x) -> "${x}"
        None    -> "-"
    end
end

fn all(data: Bytes, v: Vector<Int>, i: Int) -> String
    b = case Bytes.get(data, i)
        Some(x) -> "${Byte.to_int(x)}"
        None    -> "-"
    end

    seen = case Bytes.uint16_le_at(data, i)
        Some(_) -> "y"
        None    -> "n"
    end

    reads = [
        show(Bytes.uint8_at(data, i)),
        show(Bytes.int8_at(data, i)),
        show(Bytes.uint16_be_at(data, i)),
        show(Bytes.uint16_le_at(data, i)),
        show(Bytes.int16_be_at(data, i)),
        show(Bytes.int16_le_at(data, i)),
        show(Bytes.uint32_be_at(data, i)),
        show(Bytes.uint32_le_at(data, i)),
        show(Bytes.int32_be_at(data, i)),
        show(Bytes.int32_le_at(data, i)),
        show(Bytes.uint64_be_at(data, i)),
        show(Bytes.uint64_le_at(data, i)),
        show(Bytes.int64_be_at(data, i)),
        show(Bytes.int64_le_at(data, i)),
        show(Vector.get(v, i)),
    ]
    "${i}: ${b} ${seen} ${String.join(reads, " ")}"
end

fn main()
    data = Bytes.from_list([255, 254, 1, 128, 0, 0, 0, 0, 7])
    v = Vector[10, 20, 30, 100000000000000000000]
    for_each = fn i: Int -> io.print(all(data, v, i))
    for_each(0)
    for_each(1)
    for_each(3)
    for_each(7)
    for_each(8)
    for_each(9)
    for_each(-1)
    for_each(100000000000000000000)
end
`, "all", 17)
	for _, want := range []string{
		"0: 255 y 255 -1 65534 65279 -2 -257 4294836608 2147614463 -130688 -2147352833 18446182773023571968 2147614463 -561300685979648 2147614463 10\n",
		"1: 254 y 254 -2 65025 510 -511 510 4261511168 8389118 -33456128 8389118 18303051098098761735 504403158273884670 -143692975610789881 504403158273884670 20\n",
		"3: 128 y 128 -128 32768 128 -32768 128 2147483648 128 -2147483648 128 - - - - 100000000000000000000\n",
		"7: 0 y 0 0 7 1792 7 1792 - - - - - - - - -\n",
		"8: 7 n 7 7 - - - - - - - - - - - - -\n",
		"9: - n - - - - - - - - - - - - - - -\n",
		"-1: - n - - - - - - - - - - - - - - -\n",
		"100000000000000000000: - n - - - - - - - - - - - - - - -\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the transcript lacks %q:\n%s", want, got)
		}
	}
}

// A read in the loop allocates nothing: a loop matching a byte, a 32-bit
// read past 255 and a vector element each iteration.
func TestFastReads_AReadAllocatesNothing(t *testing.T) {
	const src = `import std::io

fn down(n: Int, data: Bytes, v: Vector<Int>) -> Int
    if n < 1 then
        0
    else
        a = case Bytes.uint8_at(data, n %% 4)
            Some(x) -> x
            None    -> -1
        end
        b = case Bytes.int32_be_at(data, 0)
            Some(x) -> x
            None    -> -1
        end
        c = case Vector.get(v, n %% 4)
            Some(x) -> x
            None    -> -1
        end
        down(n - 1, data, v) + a + b + c
    end
end

fn main()
    down(%d, Bytes.from_list([1, 2, 3, 4, 5]), Vector[1000, 2000, 3000]) |> io.print()
end
`
	listing, err := depthProgram(t, strings.ReplaceAll(strings.ReplaceAll(src, "%d", "1"), "%%", "%")).Disassemble("down")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(listing, "read in the loop"); n != 3 {
		t.Fatalf("down reads %d of its three lookups in the loop, so this measures the wrong path:\n%s", n, listing)
	}
	if per := allocsPerActivation(t, src); per > 0.1 {
		t.Errorf("one activation allocates %.2f times, want 0", per)
	}
}
