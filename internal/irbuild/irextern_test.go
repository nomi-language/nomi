package irbuild

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIRExtern_TourRegexLiterals(t *testing.T) {
	verifyLambdaProgram(t, "import {\n  std/regex.Regex\n}\n\nfn main(): Result<Unit, String> {\n  digits = try Regex`\\d+`\n  prefixed = try Regex\"room ${Regex.pattern(digits)}\"\n  text = \"room 42, floor 7\"\n\n  dbg Regex.pattern(digits)\n  dbg Regex.match?(digits, text)\n  dbg Regex.find(digits, text)\n  dbg Regex.find_all(digits, text)\n  dbg Regex.match?(prefixed, text)\n\n  Ok(Unit)\n}\n", "dbg line 10: Regex.pattern(digits) = \"\\\\d+\"\ndbg line 11: Regex.match?(digits, text) = True\ndbg line 12: Regex.find(digits, text) = Some(\"42\")\ndbg line 13: Regex.find_all(digits, text) = [\"42\", \"7\"]\ndbg line 14: Regex.match?(prefixed, text) = True\n")
}

// A `try` in main leaves main with the Err, which fails the run: the error on
// stderr, exit 1, and nothing after the `try` runs.
func TestIRExtern_RegexHandlesAndErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.nomi")
	src := `import {
  std/io
  std/regex.Regex
}

fn words(re: Regex, text: String): List<String> {
  Regex.split(re, text)
}

fn compiled(pattern: String): Result<Regex, String> {
  re = try Regex.compile(pattern)
  Ok(re)
}

fn main(): Result<Unit, String> {
  spaces = try compiled("\\s+")
  dbg words(spaces, "a b  c")
  dbg Regex.replace_all(spaces, "a b  c", "_")
  dbg Regex.find(spaces, "none")
  case compiled("(") {
    Ok(_) -> io.print("compiled")
    Err(e) -> io.print(e)
  }
  io.print("before")
  _ = try compiled("[")
  io.print("unreachable")
  Ok(Unit)
}
`
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	want := "dbg line 17: words(spaces, \"a b  c\") = [\"a\", \"b\", \"c\"]\ndbg line 18: Regex.replace_all(spaces, \"a b  c\", \"_\") = \"a_b_c\"\ndbg line 19: Regex.find(spaces, \"none\") = None\nerror parsing regexp: missing closing ): `(`\nbefore\n"
	wantErr := "error: error parsing regexp: missing closing ]: `[`\n"
	if got := vmReference(path); got.stdout != want || got.stderr != wantErr || got.exit != 1 {
		t.Fatalf("vm command: %s; want stdout %q, stderr %q, exit 1", got, want, wantErr)
	}
}
