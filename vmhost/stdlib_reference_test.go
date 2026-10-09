package vmhost_test

import (
	"io"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/std"
	"github.com/nomi-language/nomi/vmhost"
)

// A reference editor checks its stdlib module's source afresh
// (LoadStdlibSource). A typed literal there must be the module's own type:
// Regex`\d+` passed to `Regex.find` was refused with "argument 1: expected
// Regex, got Regex" while the impl index held the shared stdlib's analysis of
// std/regex in place of the fresh one, so `from_fragments` returned the shared
// Regex and `find` took the fresh one. `Regex` is a non-generic `host type`,
// which was then compared by object rather than by (file, name), so it split
// where calendar's opaque structs did not. Either fix alone passes this: the
// fresh analysis in the index, or host-type identity by (file, name).
func TestStdlibReference_TypedLiteralIsTheModulesOwnType(t *testing.T) {
	for _, c := range []struct{ module, body string }{
		{"regex", "assert Regex.find(Regex`\\d+`, \"order 66\") == Maybe.Some(\"66\")"},
		{"regex", "assert Regex.replace_all(Regex`\\d+`, \"6 and 7\", \"#\") == \"# and #\""},
		{"calendar", "assert Date.days_between(try Date\"2026-05-04\", try Date\"2026-05-07\") == 3"},
		{"toml", "assert Toml.text(Toml\"a = 1\") == \"a = 1\""},
	} {
		cases, err := vmhost.StdlibReference(c.module, c.body, "", io.Discard)
		if err != nil {
			t.Errorf("%s: %s: the module's source does not check: %v", c.module, c.body, err)
			continue
		}
		if len(cases) != 1 || cases[0].Err != nil || cases[0].Blocked != nil {
			t.Errorf("%s: %s: want one passing case, got %+v", c.module, c.body, cases)
		}
	}
}

// Checking std/duration afresh while the shared std/calendar keeps the shared
// analysis's Duration: the fresh module's `Duration.hours(1)` is passed to the
// shared `OffsetDateTime.with_offset`, and the shared `Time.between` result is
// read back through the fresh `Duration.as_seconds`. One declaration is one type
// however many analyses of it there are, so both directions type-check.
func TestStdlibSource_RecheckedDurationIsTheSharedDuration(t *testing.T) {
	src, ok := std.ReadFile("duration")
	if !ok {
		t.Fatal("std/duration is not embedded")
	}
	text := strings.Replace(string(src), "import {\n",
		"import {\n    calendar.{Date, NaiveDateTime, OffsetDateTime, Time}\n    results.Result\n", 1)
	if text == string(src) {
		t.Fatal("std/duration.nomi no longer opens with an import block")
	}
	text += "\ntest \"shared calendar, fresh duration\" {\n" +
		"  earlier = try Time.new(10, 0)\n" +
		"  later = try Time.new(10, 1)\n" +
		"  assert Duration.as_seconds(Time.between(earlier, later)) == 60\n" +
		"  day = try Date.new(2024, 1, 2)\n" +
		"  assert Result.ok?(OffsetDateTime.with_offset(NaiveDateTime.at_midnight(day), Duration.hours(1)))\n" +
		"}\n"
	p, err := vmhost.LoadStdlibSource("duration", text)
	if err != nil {
		t.Fatalf("the re-checked std/duration does not check against the shared std/calendar: %v", err)
	}
	cases := p.Cases(io.Discard, vmhost.TestOptions{})
	if len(cases) == 0 {
		t.Fatal("no cases ran")
	}
	for _, c := range cases {
		if c.Err != nil || c.Blocked != nil {
			t.Errorf("case %q: %v %v", c.Name, c.Err, c.Blocked)
		}
	}
}
