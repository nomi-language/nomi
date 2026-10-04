package analysis_test

import (
	"strings"
	"testing"
)

// A file API object reaches the file's root-level declarations only. A
// function or `once` declared in an `impl` block belongs to its owner, and a
// file-qualified spelling of it is an error that names the owner. Before this
// check, every one of these checked clean and the IR builder declined the call
// ("qualified call, file.fn: duration.seconds"), so the VM reported BLOCKED.
func TestFileQualifiedTypeOwnedMemberIsRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "inherent function",
			src:  "import std/duration\n\nfn main() {\n  d = duration.seconds(1)\n}\n",
			want: "file 'duration' has no member 'seconds'\nhelp: it belongs to Duration, so call it as Duration.seconds(...)",
		},
		{
			name: "inherent function on a prelude type",
			src:  "import std/strings\n\nfn main() {\n  s = strings.trim(\" a \")\n}\n",
			want: "file 'strings' has no member 'trim'\nhelp: it belongs to String, so call it as String.trim(...)",
		},
		{
			name: "piped inherent function",
			src:  "import std/strings\n\nfn main() {\n  s = \" a \" |> strings.trim()\n}\n",
			want: "file 'strings' has no member 'trim'\nhelp: it belongs to String, so call it as String.trim(...)",
		},
		{
			name: "inherent function as a value",
			src:  "import std/duration\n\nfn main() {\n  f = duration.seconds\n}\n",
			want: "file 'duration' has no member 'seconds'\nhelp: it belongs to Duration, so call it as Duration.seconds(...)",
		},
		{
			name: "interface implementation function",
			src:  "import std/int\n\nfn main() {\n  s = int.to_string(42)\n}\n",
			want: "file 'int' has no member 'to_string'\nhelp: it implements Display for Int, so call it as Int.to_string(...) or Display.to_string(...)",
		},
		{
			name: "type once",
			src:  "import std/int\n\nfn main() {\n  n = int.max_value\n}\n",
			want: "file 'int' has no member 'max_value'\nhelp: it belongs to Int, so read it as Int.max_value",
		},
		{
			name: "owner function on an interface",
			src:  "import std/iter\n\nfn main() {\n  n = iter.count([1, 2])\n}\n",
			want: "file 'iter' has no member 'count'\nhelp: it belongs to Iter, so call it as Iter.count(...)",
		},
		{
			name: "name nothing declares",
			src:  "import std/io\n\nfn main() {\n  io.bogus(1)\n}\n",
			want: "file 'io' has no member 'bogus'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			if len(errs) == 0 {
				t.Fatalf("the front end now ADMITS this, so the IR builder's file.fn decline is live again:\n%s", tc.src)
			}
			var found bool
			var got []string
			for _, e := range errs {
				got = append(got, diagText(e))
				if diagText(e) == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("want error %q, got:\n  %s", tc.want, strings.Join(got, "\n  "))
			}
		})
	}
}

// The owner spellings the diagnostic recommends check clean, and so does an
// ordinary root-level file function.
func TestOwnerQualifiedTypeOwnedMemberIsAccepted(t *testing.T) {
	src := `import std/io
import std/duration.Duration

fn main() {
  d = Duration.seconds(1)
  s = String.trim(" a ")
  t = Int.to_string(42)
  u = Display.to_string(42)
  n = Int.max_value
  c = Iter.count([1, 2])
  io.print(s)
}
`
	_, errs := checkSourceWithStdlib(src)
	for _, e := range errs {
		if strings.Contains(e.Message, "has no member") {
			t.Fatalf("owner-qualified spelling rejected: line %d: %s", e.Line, e.Message)
		}
	}
}
