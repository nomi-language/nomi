package format

import "testing"

// A comment on the line of an anonymous struct literal's `{` is kept. It
// used to be dropped: the parser read it while deciding what the `{`
// opened, and nothing carried it into the literal.
func TestFormat_AnonStructLiteralKeepsItsOpeningComment(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"before the first field": {
			src: "point = { // origin\n    x: 1.0, y: 2.0}\n",
			want: "point = {\n" +
				"    // origin\n" +
				"    x: 1.0,\n" +
				"    y: 2.0,\n" +
				"}\n",
		},
		"before a spread": {
			src: "moved = { // moved\n    ..point, x: 3.0}\n",
			want: "moved = {\n" +
				"    ..point,\n" +
				"    // moved\n" +
				"    x: 3.0,\n" +
				"}\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := Format(tc.src)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got:\n%s\nwant:\n%s", got, tc.want)
			}
			if err := SameMeaning(tc.src, got); err != nil {
				t.Fatal(err)
			}
			if again, _ := Format(got); again != got {
				t.Fatalf("not idempotent:\n%s", again)
			}
		})
	}
}
