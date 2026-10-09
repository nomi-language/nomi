package format

import "testing"

// A literal broken by a deeper literal's depth cascade keeps its comments: a
// struct field's leading comment and the comments before a closing bracket.
func TestFormat_DepthCascadeKeepsComments(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main() {\n    io.print(Debug.inspect([[Pt{\n        // why\n        zz: 5,\n        a: 6,\n    }]]))\n}\n",
			"fn main() {\n    io.print(Debug.inspect([\n            [\n                Pt{\n                    // why\n                    zz: 5,\n                    a: 6,\n                },\n            ],\n        ]))\n}\n",
		},
		{
			"fn main() {\n    x = Some([[{a: [1, 2] // end\n    }]])\n}\n",
			"fn main() {\n    x = Some([\n        [\n            {\n                a: [\n                    1,\n                    2,\n                ],\n                // end\n            },\n        ],\n    ])\n}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
