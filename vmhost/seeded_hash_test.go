package vmhost

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/rt"
)

// seededHashChild names the variable that makes
// TestSeededHash_CompileTimeChecksDoNotReadAHash run its cases in the
// process it is set in, a child started under a hash seed of its own.
const seededHashChild = "NOMI_SEEDED_HASH_CHILD"

// seededHashShapes is each way a rule or a literal's check can read a hash
// keyed by the run's seed, as an expression over an Int n and a String s:
// a String's own hash, Hashable.hash of a String, of a struct whose
// Hashable is derived, and of a List.
var seededHashShapes = []struct{ name, expr string }{
	{"String.hash", "String.hash(s)"},
	{"Hashable.hash of a String", "Hashable.hash(s)"},
	{"Hashable.hash of a derived struct", "Hashable.hash(Point{x: n, y: String.length(s)})"},
	{"Hashable.hash of a List", "Hashable.hash([n, String.length(s)])"},
}

const seededHashPoint = "struct Point\n    x: Int\n    y: Int\nend\n\nderive Hashable for Point\n\n"

// The compiler runs a refinement's rule, a typed literal's handler and a
// checked literal's check once, when it checks the program, and a hash is
// keyed by a seed each run draws. So one of them that can read a hash is
// an error with its own code, under every seed, and one that builds a Map
// and reads it back, or hashes an Int, is not: their answers are the same
// in every run. The cases run here and in two processes started under two
// seeds, which the children show differ by printing a String's hash.
func TestSeededHash_CompileTimeChecksDoNotReadAHash(t *testing.T) {
	if os.Getenv(seededHashChild) != "" {
		fmt.Printf("hash of a: %d\n", rt.StringHash("a"))
		seededHashCases(t)
		return
	}
	seededHashCases(t)
	var hashes, maps, digests []string
	for _, seed := range []string{"first seed", "second seed"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSeededHash_CompileTimeChecksDoNotReadAHash$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), seededHashChild+"=1", "NOMI_HASH_SEED="+seed)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("under the seed %q the cases fail: %v\n%s", seed, err, out)
		}
		hashes = append(hashes, lineStarting(t, out, "hash of a: "))
		maps = append(maps, lineStarting(t, out, "map rule: "))
		digests = append(digests, lineStarting(t, out, "digest rule: "))
	}
	if hashes[0] == hashes[1] {
		t.Fatalf("the two children hash \"a\" alike (%s): the seed did not reach them", hashes[0])
	}
	if maps[0] != maps[1] {
		t.Errorf("a rule that builds a Map answers %q under one seed and %q under the other", maps[0], maps[1])
	}
	if digests[0] != digests[1] {
		t.Errorf("a rule over digests and checksums answers %q under one seed and %q under the other", digests[0], digests[1])
	}
}

// digestRule is a refinement whose rule reads std::hash's digests, a
// Hasher, an HMAC and std::checksum's CRC-32. They are fixed functions of
// their operands, not the run's keyed hash, so the rule is checked at
// compile time like any pure rule, and answers the same under every seed.
// crc32 of "4" is even and of "3" odd.
const digestRule = "import\n    std::checksum\n    std::hash.{self, Algorithm, Hasher}\n    std::io\nend\n\n" +
	"refine EvenCrc Int when fn n ->\n" +
	"    s = String.to_bytes(Int.to_string(n))\n" +
	"    streamed = Hasher.new(Algorithm.Sha256) |> Hasher.update(s) |> Hasher.finish()\n" +
	"    checksum.crc32(s) % 2 == 0 and hash.digest(Algorithm.Sha256, s) == hash.sha256(s) and streamed == hash.sha256(s)\n" +
	"        and Bytes.length(hash.hmac(Algorithm.Blake2s256, s, s)) == 32\n" +
	"end\n\n" +
	"fn main()\n" +
	"    e: EvenCrc = 4\n" +
	"    io.inspect((e, EvenCrc.try_from(3), checksum.crc32(String.to_bytes(\"4\")), hash.sha256_hex(String.to_bytes(\"4\"))))\n" +
	"end\n"

// lineStarting is the line of out that starts with prefix.
func lineStarting(t *testing.T, out []byte, prefix string) string {
	t.Helper()
	for _, line := range bytes.Split(out, []byte("\n")) {
		if s := string(line); strings.HasPrefix(s, prefix) {
			return s
		}
	}
	t.Fatalf("no line starts with %q:\n%s", prefix, out)
	return ""
}

func seededHashCases(t *testing.T) {
	t.Helper()
	for _, shape := range seededHashShapes {
		rule := "import std::io\n\n" + seededHashPoint +
			"refine Hashed Int when fn n ->\n    s = Int.to_string(n)\n    " + shape.expr + " % 2 == 0\nend\n\n" +
			"fn main()\n    h: Hashed = 2\n    io.inspect(h)\nend\n"
		wantSeeded(t, "a rule: "+shape.name, rule)

		handler := "import\n    std::io\n    std::literals.{Fragment, Literal}\nend\n\n" + seededHashPoint +
			"struct Word\n    text: String\nend\n\n" +
			"impl Literal for Word\n" +
			"    fn from_fragments(fragments: List<Fragment<String>>) -> Word\n" +
			"        n = Iter.count(fragments)\n        s = Int.to_string(n)\n" +
			"        Word{text: Int.to_string(" + shape.expr + ")}\n" +
			"    end\nend\n\n" +
			"fn main()\n    io.print(Word`x`.text)\nend\n"
		wantSeeded(t, "a typed literal's handler: "+shape.name, handler)

		check := "import\n    std::io\n    std::literals.{CheckedLiteral, Fragment, LiteralError}\nend\n\n" + seededHashPoint +
			"type Even\n\n" +
			"impl CheckedLiteral<String, String> for Even\n" +
			"    fn check(statics: List<String>) -> Result<Unit, LiteralError>\n" +
			"        n = Iter.count(statics)\n        s = Int.to_string(n)\n" +
			"        if " + shape.expr + " % 2 == 0 then\n" +
			"            Ok(Unit)\n" +
			"        else\n" +
			"            Err(LiteralError{message: \"odd\", fragment: 0, offset: 0})\n" +
			"        end\n" +
			"    end\n\n" +
			"    fn build(fragments: List<Fragment<String>>) -> String\n" +
			"        \"${Iter.count(fragments)}\"\n" +
			"    end\n" +
			"end\n\n" +
			"fn main()\n    io.print(Even\"a\")\nend\n"
		wantSeeded(t, "a checked literal's check: "+shape.name, check)
	}

	// A rule that builds a Map and reads it back answers by the keys'
	// insertion order and equality, never by a hash.
	const mapped = "import std::io\n\n" +
		"refine Listed String when fn s ->\n" +
		"    m = {\"z\" => 1, \"a\" => 2, s => 3, \"m\" => 4}\n" +
		"    Map.keys(m) == [\"z\", \"a\", s, \"m\"] and Map.get(m, s) == Some(3) and Set.contains(Set[s, \"q\"], s)\n" +
		"end\n\n" +
		"fn main()\n    l: Listed = \"k\"\n    io.inspect((l, Listed.try_from(\"a\")))\nend\n"
	fmt.Printf("map rule: %s\n", runSeededHashProgram(t, mapped))

	// A rule over std::hash's digests and std::checksum is no seeded hash:
	// it is checked when the program is, and its literal that fails the
	// rule is the refinement's own error.
	digested := runSeededHashProgram(t, digestRule)
	if want := `(4, None, 4088798008, "4b227777d4dd1fc61c6f884f48641d02b4d121d3fd328cb08b5531fcacdabf8a")`; digested != want {
		t.Errorf("a rule over digests prints %q, want %q", digested, want)
	}
	fmt.Printf("digest rule: %s\n", digested)
	odd := literalDiagnostics(t, strings.Replace(digestRule, "e: EvenCrc = 4", "e: EvenCrc = 3", 1))
	if len(odd) != 1 || odd[0].Code != InvalidRefinementCode {
		t.Errorf("a literal failing a rule over digests: diagnostics = %v, want one %s", odd, InvalidRefinementCode)
	}

	// Int's hash is the Int itself, which no seed keys, past int64's
	// range too, so a rule may read it.
	const ints = "import std::io\n\n" +
		"refine EvenHash Int when fn n -> Hashable.hash(n) % 2 == 0\n\n" +
		"fn main()\n" +
		"    e: EvenHash = 2\n" +
		"    big = 100000000000000000000 * 3\n" +
		"    io.inspect((e, Hashable.hash(big) == big, EvenHash.try_from(big), EvenHash.try_from(3)))\n" +
		"end\n"
	if got, want := runSeededHashProgram(t, ints), "(2, True, Some(300000000000000000000), None)"; got != want {
		t.Errorf("a rule over Int's hash prints %q, want %q", got, want)
	}
	ds := literalDiagnostics(t, "import std::io\n\nrefine EvenHash Int when fn n -> Hashable.hash(n) % 2 == 0\n\nfn main()\n    o: EvenHash = 3\n    io.inspect(o)\nend\n")
	if len(ds) != 1 || ds[0].Code != InvalidRefinementCode {
		t.Errorf("an odd literal of a rule over Int's hash: diagnostics = %v, want one %s", ds, InvalidRefinementCode)
	}
}

// wantSeeded fails unless src is refused with one diagnostic, whose code
// is SeededHashCode and whose message names no crossing by the machine's
// own name (`vm.hash_value`), and answers its message.
func wantSeeded(t *testing.T, name, src string) string {
	t.Helper()
	ds := literalDiagnostics(t, src)
	if len(ds) != 1 || ds[0].Code != SeededHashCode {
		t.Errorf("%s: diagnostics = %v, want one %s", name, ds, SeededHashCode)
		return ""
	}
	if strings.Contains(ds[0].Message, "vm.") {
		t.Errorf("%s: the message names the machine's own crossing: %s", name, ds[0].Message)
	}
	return ds[0].Message
}

// A hash the machine computes from a value's shape is `Hashable.hash` to
// the program, and a refusal names it so.
func TestSeededHash_TheMessageNamesHashableHash(t *testing.T) {
	src := "import std::io\n\n" + seededHashPoint +
		"refine Hashed Int when fn n -> Hashable.hash(Point{x: n, y: 1}) % 2 == 0\n\n" +
		"fn main()\n    h: Hashed = 2\n    io.inspect(h)\nend\n"
	msg := wantSeeded(t, "a rule over a derived struct's hash", src)
	if !strings.Contains(msg, "calls Hashable.hash)") {
		t.Errorf("the message does not name Hashable.hash: %s", msg)
	}
}

// A Map key or Set element of a type with a hand-written Hashable is hashed
// by running it, so a rule that builds a Map and a Set keyed by a type whose
// `hash` reads a String's hash reaches that hash and is refused: where the
// hash disagrees with `equal` it decides which keys share an entry, and a
// `hash` that fails on the value it computes fails under some seeds only.
func TestSeededHash_AKeyWhoseHashReadsAHashIsRefused(t *testing.T) {
	src := "import std::io\n\n" +
		"struct Name\n    text: String\nend\n\n" +
		"impl Hashable for Name\n    fn hash(n: Name) -> Int\n        String.hash(n.text)\n    end\nend\n\n" +
		"refine Known String when fn s ->\n" +
		"    m = {Name{text: s} => 1, Name{text: \"a\"} => 2}\n" +
		"    Map.size(m) == 2 and Set.contains(Set[Name{text: \"b\"}], Name{text: s})\n" +
		"end\n\n" +
		"fn main()\n    k: Known = \"b\"\n    io.inspect(k)\nend\n"
	msg := wantSeeded(t, "a rule keying a Map by a hand-written hash over a String", src)
	if !strings.Contains(msg, "calls strings.String.hash") {
		t.Errorf("the message does not name the String hash the key's impl reads: %s", msg)
	}
}

// seededHashTag is a struct whose hand-written Hashable reads a String's
// hash, beside a written Equatable that agrees with it.
const seededHashTag = "struct Tag\n    name: String\nend\n\n" +
	"impl Equatable for Tag\n    fn equal(a: Tag, b: Tag) -> Bool\n        a.name == b.name\n    end\nend\n\n" +
	"impl Hashable for Tag\n    fn hash(tag: Tag) -> Int\n        Hashable.hash(tag.name)\n    end\nend\n\n"

// A type whose hand-written Hashable reads a String's hash is refused only
// in a literal's handler that can hash a value of it. A `Sql` literal's
// handler keys nothing of the program's: it hands a List of std::sql's
// Value, which holds itself through its Array variant, to a crossing, and
// the walk reads that List as holding Value and what Value's fields hold.
// A handler of the program's own over a type that holds itself the same
// way is checked alike.
func TestSeededHash_AWrittenHashElsewhereLeavesLiteralsAlone(t *testing.T) {
	src := "import\n    std::io\n    std::literals.{Fragment, Literal}\n    std::sql.Sql\nend\n\n" + seededHashTag +
		"enum Node\n    Text String\n    Group List<Node>\nend\n\n" +
		"struct Doc\n    nodes: List<Node>\nend\n\n" +
		"impl Literal for Doc\n" +
		"    fn from_fragments(fragments: List<Fragment<String>>) -> Doc\n" +
		"        nodes: List<Node> = [Node.Text(\"${Iter.count(fragments)}\")]\n" +
		"        Doc{nodes: nodes + [Node.Group(nodes)]}\n" +
		"    end\nend\n\n" +
		"fn main()\n" +
		"    io.print(Sql\"SELECT 1\")\n" +
		"    io.print(Iter.count(Doc`x`.nodes))\n" +
		"    io.print(Set.size(Set[Tag{name: \"a\"}, Tag{name: \"a\"}]))\n" +
		"end\n"
	if got, want := runSeededHashProgram(t, src), "SELECT 1\n2\n1"; got != want {
		t.Errorf("the program prints %q, want %q", got, want)
	}
}

// A literal's handler that does hash a value of a type whose hand-written
// Hashable reads a String's hash is refused, and the message names the
// String hash that impl reads.
func TestSeededHash_AHandlerThatHashesAWrittenHashIsRefused(t *testing.T) {
	src := "import\n    std::io\n    std::literals.{Fragment, Literal}\nend\n\n" + seededHashTag +
		"struct Count\n    n: Int\nend\n\n" +
		"impl Literal for Count\n" +
		"    fn from_fragments(fragments: List<Fragment<String>>) -> Count\n" +
		"        Count{n: Iter.count(fragments) + Set.size(Set[Tag{name: \"a\"}, Tag{name: \"b\"}])}\n" +
		"    end\nend\n\n" +
		"fn main()\n    io.print(Count`x`.n)\nend\n"
	msg := wantSeeded(t, "a typed literal's handler that builds a Set of a written hash", src)
	if !strings.Contains(msg, "Hashable.hash for Tag calls strings.String.hash") {
		t.Errorf("the message does not name the String hash Tag's impl reads: %s", msg)
	}
}

// runSeededHashProgram loads and runs src and answers its output's one
// line.
func runSeededHashProgram(t *testing.T, src string) string {
	t.Helper()
	p, err := LoadSource("main", src)
	if err != nil {
		var ds Diagnostics
		if errors.As(err, &ds) {
			t.Fatalf("the program does not load: %v", ds)
		}
		t.Fatalf("the program does not load: %v", err)
	}
	var out strings.Builder
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	return strings.TrimSpace(out.String())
}
