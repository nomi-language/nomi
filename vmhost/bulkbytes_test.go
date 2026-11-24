package vmhost_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/vmhost"
)

// bulkProbes are std functions that read every byte of a buffer at once,
// each as the body of a function of no parameters over `data`. Each is a
// host call however long the buffer is.
var bulkProbes = []struct{ name, body string }{
	{"crc32", "checksum.crc32(data)"},
	{"crc32c", "checksum.crc32c(data)"},
	{"crc64_iso", "checksum.crc64_iso(data)"},
	{"crc64_ecma", "checksum.crc64_ecma(data)"},
	{"adler32", "checksum.adler32(data)"},
	{"fnv1a_32", "checksum.fnv1a_32(data)"},
	{"fnv1a_64", "checksum.fnv1a_64(data)"},
	{"digest", "Bytes.length(hash.digest(Algorithm.Blake2b256, data))"},
	{"hmac", "Bytes.length(hash.hmac(Algorithm.Sha3With256, data, data))"},
	{"streamed", "Hasher.new(Algorithm.Sha256) |> Hasher.update(data) |> Hasher.update(data) |> Hasher.finish() |> Bytes.length()"},
	{"xor", "Bytes.xor(data, data) |> Option.map(Bytes.length) |> Option.with_default(-1)"},
	{"xor_repeating", "Bytes.length(Bytes.xor_repeating(data, Bytes.from_list([1, 2, 3, 4])))"},
	{"reverse", "Bytes.length(Bytes.reverse(data))"},
	{"code_point_at", "Bytes.code_point_at(data, Bytes.length(data) - 1) |> Option.map(.second) |> Option.with_default(0)"},
	{"index_of_stop", "Bytes.index_of(data, String.to_bytes(\"b\"), stop: Bytes.length(data) - 1) |> Option.with_default(-1)"},
	{"index_of_none", "Bytes.index_of_none(data, String.to_bytes(\"a\")) |> Option.with_default(-1)"},
	{"last_index_of_none", "Bytes.last_index_of_none(data, String.to_bytes(\"a\")) |> Option.with_default(-1)"},
	{"hex_decode_bytes", "hex.decode_bytes(data) |> Result.map(Bytes.length) |> Result.with_default(-1)"},
	{"base64_decode_bytes", "base64.decode_bytes(data) |> Result.map(Bytes.length) |> Result.with_default(-1)"},
}

// bulkProgram is a program whose `data` is n bytes, with a function per
// probe.
func bulkProgram(n int) string {
	var b strings.Builder
	b.WriteString("import\n    std::base64\n    std::checksum\n    std::hash.{self, Algorithm, Hasher}\n    std::hex\n    std::io\nend\n\n")
	fmt.Fprintf(&b, "once data = String.to_bytes(%q)\n\n", strings.Repeat("a", n))
	var calls []string
	for _, p := range bulkProbes {
		fmt.Fprintf(&b, "fn probe_%s() -> Int\n    %s\nend\n\n", p.name, p.body)
		calls = append(calls, "probe_"+p.name+"()")
	}
	// The control: a fold over the bytes in Nomi, which the step count
	// must catch.
	b.WriteString("fn probe_loop() -> Int\n    Iter.reduce(data, 0, fn n, _ -> n + 1)\nend\n\n")
	calls = append(calls, "probe_loop()")
	b.WriteString("fn main()\n    io.print(" + strings.Join(calls, " + ") + ")\nend\n")
	return b.String()
}

// Each probe takes the machine as many steps over 1 MiB as over 16 bytes:
// it is one host call whatever the length, and a rewrite as a Nomi loop
// over the bytes, which runs at about 10 MB/s, would cost a step or more a
// byte. Bytes.fill is not here: its cost is the length it is asked for, so
// an evaluation refuses it as unbounded.
func TestBulkBytes_StepsDoNotGrowWithTheLength(t *testing.T) {
	var programs [2]*vmhost.Program
	for i, n := range []int{16, 1 << 20} {
		p, err := vmhost.LoadSource("main", bulkProgram(n))
		if err != nil {
			t.Fatalf("the program over %d bytes does not load: %v", n, err)
		}
		programs[i] = p
	}
	for _, probe := range bulkProbes {
		var steps [2]int64
		for i, p := range programs {
			if _, err := p.Evaluate(context.Background(), "probe_"+probe.name, vm.Limits{Steps: 10_000, Spent: &steps[i]}); err != nil {
				t.Fatalf("%s: %v", probe.name, err)
			}
		}
		if steps[0] == 0 {
			t.Errorf("%s counted no steps, so this test counts nothing", probe.name)
		}
		if steps[0] != steps[1] {
			t.Errorf("%s takes %d steps over 16 bytes and %d over 1 MiB", probe.name, steps[0], steps[1])
		}
	}
	if _, err := programs[0].Evaluate(context.Background(), "probe_loop", vm.Limits{Steps: 10_000}); err != nil {
		t.Fatalf("a fold over 16 bytes: %v", err)
	}
	if _, err := programs[1].Evaluate(context.Background(), "probe_loop", vm.Limits{Steps: 10_000}); err != vmhost.ErrEvalLimit {
		t.Errorf("a fold over 1 MiB in Nomi answers %v within the steps a host call takes, so the count would not catch a loop", err)
	}
}

// A program that streams through Hashers, keeps one and forks it, prints
// the same digests on a machine that writes updates in place and on one
// that copies every update (vm.NoInPlaceEnv), and they are the digests of
// the whole inputs.
func TestHasher_InPlaceAndCopyingMachinesAgree(t *testing.T) {
	const src = `import
    std::hash.{self, Algorithm, Hasher}
    std::io
end

struct State
    hasher: Hasher
    count:  Int
end

fn feed(state: State, chunks: List<Bytes>) -> State
    case chunks
        [] -> state
        [chunk, ..rest] -> feed({..state, hasher: Hasher.update(state.hasher, chunk), count: .. + 1}, rest)
    end
end

fn main()
    chunks = ["The quick ", "brown fox ", "jumps over ", "the lazy dog"] |> Iter.map(String.to_bytes) |> Iter.to_list()
    whole = String.to_bytes("The quick brown fox jumps over the lazy dog")
    start = State{hasher: Hasher.new(Algorithm.Blake2b256), count: 0}
    half = feed(start, chunks |> Iter.take(2) |> Iter.to_list())
    done = feed(half, chunks |> Iter.drop(2) |> Iter.to_list())
    other = feed(half, [String.to_bytes("cats")])
    io.print(hash.digest_hex(Algorithm.Blake2b256, whole))
    io.print(Hasher.finish(done.hasher) == hash.digest(Algorithm.Blake2b256, whole))
    io.print(Hasher.finish(other.hasher) == hash.digest(Algorithm.Blake2b256, String.to_bytes("The quick brown fox cats")))
    io.print(Hasher.finish(half.hasher) == hash.digest(Algorithm.Blake2b256, String.to_bytes("The quick brown fox ")))
    io.print((done.count, other.count, half.count))
end
`
	run := func(copying string) string {
		t.Setenv(vm.NoInPlaceEnv, copying)
		p, err := vmhost.LoadSource("main", src)
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		if err := p.Run(context.Background(), &out, nil, false); err != nil {
			t.Fatalf("run: %v\n%s", err, out.String())
		}
		return out.String()
	}
	inPlace, copied := run(""), run("1")
	want := "01718cec35cd3d796dd00020e0bfecb473ad23457d063b75eff29c0ffa2e58a9\nTrue\nTrue\nTrue\n(4, 3, 2)\n"
	if inPlace != want {
		t.Errorf("in place the program prints:\n%s\nwant:\n%s", inPlace, want)
	}
	if copied != inPlace {
		t.Errorf("copying, the program prints:\n%s\nin place:\n%s", copied, inPlace)
	}
}
