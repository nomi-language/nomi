package vmhost

import (
	"bytes"
	"context"
	"testing"
)

// sessionRun loads src as the file with key, runs it and commits it.
func sessionRun(t *testing.T, s *Session, key, src string) {
	t.Helper()
	p, err := s.LoadFile(key, src)
	if err != nil {
		s.Discard(key)
		t.Fatalf("%s: %v", key, err)
	}
	if err := s.Run(context.Background(), p); err != nil {
		s.Discard(key)
		t.Fatalf("%s runs: %v", key, err)
	}
	s.Commit(p)
}

// A later file imports an earlier one's function and type and runs on the
// same machine.
func TestSessionFiles_ALaterFileCallsAnEarlierOne(t *testing.T) {
	var out bytes.Buffer
	s := NewSession(&out, nil)
	sessionRun(t, s, "repl/in1", "pub\nfn f(n: Int) -> Int\n  n + 1\nend\npub\nstruct P\n  x: Int\nend\nfn main()\n  Unit\nend\n")
	sessionRun(t, s, "repl/in2", "import std::io\nimport repl::in1.{f, P}\nfn main()\n  p = P{x: f(1)}\n  io.inspect(p)\n  io.inspect(f(41))\nend\n")
	if got, want := out.String(), "P{x: 2}\n42\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Each kind of declaration an earlier file makes is read by a later one
// checked against its interface and lowered against its module: a generic
// function instantiated at new types, an inherent or interface impl in a
// file other than its type's, a once, a generic struct and an enum.
func TestSessionFiles_EarlierDeclarationsCarry(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"generic", []string{
			"pub\nfn id<T>(x: T) -> T\n  x\nend\nfn main()\n  Unit\nend\n",
			"import std::io\nimport repl::in1.{id}\nfn main()\n  io.inspect(id(5))\n  io.inspect(id(\"s\"))\nend\n"}, "5\n\"s\"\n"},
		{"inherent impl elsewhere", []string{
			"pub\nstruct P\n  x: Int\nend\nfn main()\n  Unit\nend\n",
			"import repl::in1.{P}\nimpl P\n  pub fn sum(p: P) -> Int\n    p.x + 1\n  end\nend\nfn main()\n  Unit\nend\n",
			"import std::io\nimport repl::in1.{P}\nimport repl::in2\nfn main()\n  io.inspect(P.sum(P{x: 2}))\nend\n"}, "3\n"},
		{"interface impl elsewhere", []string{
			"pub\nstruct P\n  x: Int\nend\nfn main()\n  Unit\nend\n",
			"import repl::in1.{P}\nimpl Display for P\n  fn to_string(_: P) -> String\n    \"P!\"\n  end\nend\nfn main()\n  Unit\nend\n",
			"import std::io\nimport repl::in1.{P}\nfn main()\n  io.print(\"${P{x: 2}}\")\nend\n"}, "P!\n"},
		{"once", []string{
			"pub\nonce o: Int = 7\nfn main()\n  Unit\nend\n",
			"import std::io\nimport repl::in1.{o}\nfn main()\n  io.inspect(o + 1)\nend\n"}, "8\n"},
		{"generic struct", []string{
			"pub\nstruct Box<T>\n  v: T\nend\npub\nfn unbox<T>(b: Box<T>) -> T\n  b.v\nend\nfn main()\n  Unit\nend\n",
			"import std::io\nimport repl::in1.{Box, unbox}\nfn main()\n  io.inspect(unbox(Box{v: [1]}))\n  io.inspect(Box{v: 2})\nend\n"}, "[1]\nBox{v: 2}\n"},
		{"enum", []string{
			"pub\nenum Color\n  Red\n  Green\nend\nfn main()\n  Unit\nend\n",
			"import std::io\nimport repl::in1.{Color}\nfn main()\n  c = Color.Red\n  io.inspect(c)\n  io.inspect(c == Color.Green)\nend\n"}, "Red\nFalse\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			s := NewSession(&out, nil)
			for i, f := range c.files {
				sessionRun(t, s, "repl/in"+string(rune('1'+i)), f)
			}
			if got := out.String(); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
