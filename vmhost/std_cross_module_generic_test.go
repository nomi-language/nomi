package vmhost_test

import "testing"

// A non-generic std body that calls a generic function of another std
// module is built per program, as one calling a generic of its own module
// is. std/regex's `impl Debug for Regex` calls std/strings'
// `String.contains?<M>`, so the shared std cache cannot retain it; every way
// a program reaches it (io.inspect, Debug.inspect, the impl's own name,
// `dbg`, inside a list, a struct and a Maybe) runs the per-program body.
// `String.strip_prefix` and `strip_suffix` call their own module's generic
// `starts_with?` and `ends_with?`.
func TestStdCrossModuleGeneric_NonGenericBodyCallsAnotherModulesGeneric(t *testing.T) {
	got := runSourceOutput(t, "import std/io\n"+
		"import std/regex.Regex\n"+
		"\n"+
		"struct Rule {\n"+
		"    name: String\n"+
		"    re: Regex\n"+
		"}\n"+
		"\n"+
		"fn main() {\n"+
		"    re = Regex`\\d+`\n"+
		"    io.inspect(re)\n"+
		"    io.print(Debug.inspect(re))\n"+
		"    io.print(Regex.inspect(re))\n"+
		"    _ = dbg re\n"+
		"    io.inspect([re])\n"+
		"    io.inspect(Rule{name: \"n\", re: re})\n"+
		"    io.inspect(Some(re))\n"+
		"    tick = Regex.compile(\"a`b\") |> Result.with_default(re)\n"+
		"    io.inspect(tick)\n"+
		"    io.inspect(String.strip_prefix(\"hello world\", \"hello \"))\n"+
		"    io.inspect(String.strip_suffix(\"hello.txt\", \".txt\"))\n"+
		"    io.inspect(String.strip_prefix(\"hello\", \"xyz\"))\n"+
		"}\n")
	want := "Regex`\\d+`\n" +
		"Regex`\\d+`\n" +
		"Regex`\\d+`\n" +
		"dbg line 14: re = Regex`\\d+`\n" +
		"[Regex`\\d+`]\n" +
		"Rule{name: \"n\", re: Regex`\\d+`}\n" +
		"Some(Regex`\\d+`)\n" +
		"Regex\"a`b\"\n" +
		"Some(\"world\")\n" +
		"Some(\"hello\")\n" +
		"None\n"
	if got != want {
		t.Fatalf("output:\n%s\nwant:\n%s", got, want)
	}
}
