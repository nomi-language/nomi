package format

import "testing"

// A comment after a case's last arm, as the stage of a pipe, stays inside
// the case's braces. It used to be written there and again after the `}`,
// where the line comment swallowed the `) == 99` that followed, so the
// output did not parse.
// The same holds for a block that is a pipe's source.
func TestFormat_PipeStageCaseKeepsItsEndCommentInside(t *testing.T) {
	for name, src := range map[string]string{
		"a case stage": "fn main()\n" +
			"    x = (y |> case\n" +
			"        Some(n) -> n\n" +
			"        // the rest\n" +
			"    end) == 99\n" +
			"end\n",
		"a block source": "fn main()\n" +
			"    x = if c then\n" +
			"        1\n" +
			"    else\n" +
			"        begin\n" +
			"            y = 2\n" +
			"            y\n" +
			"            // the rest\n" +
			"        end\n" +
			"        |> f()\n" +
			"    end\n" +
			"end\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Format(src)
			if err != nil {
				t.Fatal(err)
			}
			if err := SameMeaning(src, got); err != nil {
				t.Fatalf("%v\n%s", err, got)
			}
			if again, _ := Format(got); again != got {
				t.Fatalf("not idempotent:\n%s\nthen:\n%s", got, again)
			}
		})
	}
}
