package analysis_test

import (
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/analysis"
)

// A condition, branch, element, key or value that does not fit is the
// error's span: the offending part, not the whole expression or the start
// of its line.
func TestMismatchErrorsSpanTheOffendingPart(t *testing.T) {
	for _, tc := range []struct {
		name, src, message, span string
	}{
		{"if condition",
			"fn main() { _ = if 1 { 2 } else { 3 } }\n",
			"if condition must be Bool, got Int", "1:20-1:21"},
		{"if/else branches, at the else branch's value",
			"fn main() {\n    _ = if True { 1 } else { \"a\" }\n}\n",
			"if/else branch type mismatch: then is Int, else is String", "2:30-2:33"},
		{"list spread head",
			"fn main() {\n    xs = [1]\n    _ = [1, \"a\", ..xs]\n}\n",
			"list element type mismatch: Int vs String", "3:13-3:16"},
		{"list spread tail",
			"fn main() {\n    _ = [1, ..[\"a\"]]\n}\n",
			"list spread tail element type mismatch: Int vs String", "2:15-2:20"},
		{"map value",
			"fn main() {\n    _ = %{1 => \"a\", 2 => 3}\n}\n",
			"map value type mismatch: expected String, got Int", "2:26-2:27"},
		{"map key",
			"fn main() {\n    _ = %{1 => \"a\", \"b\" => \"c\"}\n}\n",
			"map key type mismatch: expected Int, got String", "2:21-2:24"},
		{"map value against an annotation",
			"fn main() {\n    m: Map<Int, String> = %{1 => 2}\n    _ = m\n}\n",
			"map value type mismatch: expected String, got Int", "2:34-2:35"},
		{"map key against an annotation",
			"fn main() {\n    m: Map<Int, String> = %{\"k\" => \"v\"}\n    _ = m\n}\n",
			"map key type mismatch: expected Int, got String", "2:29-2:32"},
		{"named list element",
			"type Ids List<Int>\n\nfn main() {\n    _ = Ids[1, \"a\"]\n}\n",
			"Ids element type: expected Int, got String", "4:16-4:19"},
		{"named map value",
			"type Kvs Map<String, Int>\n\nfn main() {\n    _ = %Kvs{\"a\" => \"b\"}\n}\n",
			"Kvs value type: expected Int, got String", "4:21-4:24"},
		{"named map key",
			"type Kvs Map<String, Int>\n\nfn main() {\n    _ = %Kvs{1 => 2}\n}\n",
			"Kvs key type: expected String, got Int", "4:14-4:15"},
		{"struct call form with a non-record argument",
			"struct P {\n    x: Int\n}\n\nfn main() {\n    _ = P(1)\n}\n",
			"P expects an anonymous struct literal #{...} matching its fields, got Int", "6:11-6:12"},
		{"bound alias entry that is not an interface",
			"typealias Both Display and Int\n\nfn f<T>(x: T) -> T where T: Both {\n    x\n}\n\nfn main() {\n    _ = f(1)\n}\n",
			"interface-bound alias entry must be an interface, got Int", "1:28-1:31"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errs := checkSourceWithStdlib(tc.src)
			errs = analysis.CompleteSpans(tc.src, errs)
			var all []string
			for _, e := range errs {
				if e.Message == tc.message {
					if got := shapeOf(e); got != tc.span {
						t.Errorf("span = %s, want %s", got, tc.span)
					}
					return
				}
				all = append(all, e.Error())
			}
			t.Fatalf("no error %q; got:\n  %s", tc.message, strings.Join(all, "\n  "))
		})
	}
}
