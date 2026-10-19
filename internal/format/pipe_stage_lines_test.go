package format

import "testing"

// A pipeline is written across lines when a stage starts on a line after the
// one the stage before it ends on. A stage written across lines does not
// make it so: `x |> dbg f(#P{...})` is written `x |> f(#P{...}) |> dbg`, the
// `|> dbg` on the line the literal closes, and the next format keeps it.
func TestFormat_StageAfterStackedStageKeepsItsLine(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main() {\n    g = 5 |> dbg double(#P{\n        a: 1,\n        b: 2,\n    })\n}\n",
			"fn main() {\n    g = 5 |> double(#P{\n        a: 1,\n        b: 2,\n    }) |> dbg\n}\n",
		},
		{
			"fn main() {\n    k = s |> case (#P{\n        a: 1,\n        b: 2,\n    }) {\n        _ -> 1\n    }\n}\n",
			"fn main() {\n    k = s |> case (#P{\n        a: 1,\n        b: 2,\n    }) {\n        _ -> 1\n    }\n}\n",
		},
		// A stage that starts a line still stacks the pipeline.
		{
			"fn main() {\n    g = f(#P{\n        a: 1,\n        b: 2,\n    })\n    |> dbg\n}\n",
			"fn main() {\n    g =\n        f(#P{\n            a: 1,\n            b: 2,\n        })\n        |> dbg\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
