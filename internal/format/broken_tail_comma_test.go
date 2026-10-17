package format

import "testing"

// A broken list writes a comma after its last item, and a call ending that
// item has to fit with it. The comma is IfBroken, and the call's fits check
// read it flat, as nothing, so a field one column too wide stayed on its line
// and broke on the next format, when the literal was the author's stacked
// one and its comma plain text.
func TestFormat_LastItemFitsWithItsComma(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{
			"fn main() {\n" +
				"    d = Config{name: \"x\", timeout_rather_long_rather_long_rather_long_rather_long_rather_long_xxx: Duration.minutes(1)}\n" +
				"}\n",
			"fn main() {\n" +
				"    d = Config{\n" +
				"        name: \"x\",\n" +
				"        timeout_rather_long_rather_long_rather_long_rather_long_rather_long_xxx: Duration.minutes(\n" +
				"            1\n" +
				"        ),\n" +
				"    }\n" +
				"}\n",
		},
		// One column narrower it fits, comma and all.
		{
			"fn main() {\n" +
				"    c = Config{name: \"x\", timeout_rather_long_rather_long_rather_long_rather_long_rather_long_xx: Duration.minutes(1)}\n" +
				"}\n",
			"fn main() {\n" +
				"    c = Config{\n" +
				"        name: \"x\",\n" +
				"        timeout_rather_long_rather_long_rather_long_rather_long_rather_long_xx: Duration.minutes(1),\n" +
				"    }\n" +
				"}\n",
		},
		{
			"fn failure(assertion: EqualsAssertion) -> Maybe<AssertionDetails> {\n" +
				"    Some(AssertionDetails{\n" +
				"        reason: \"values were not equal\",\n" +
				"        details_rather_long: [AssertionDetail{ label: \"expected\", value_rather_long_rather_long_rather_long_rather_: Debug.inspect(assertion.expected),\n" +
				"        }],\n" +
				"    })\n" +
				"}\n",
			"fn failure(assertion: EqualsAssertion) -> Maybe<AssertionDetails> {\n" +
				"    Some(AssertionDetails{\n" +
				"        reason: \"values were not equal\",\n" +
				"        details_rather_long: [\n" +
				"            AssertionDetail{\n" +
				"                label: \"expected\",\n" +
				"                value_rather_long_rather_long_rather_long_rather_: Debug.inspect(\n" +
				"                    assertion.expected\n" +
				"                ),\n" +
				"            },\n" +
				"        ],\n" +
				"    })\n" +
				"}\n",
		},
	} {
		formatsKeepingMeaning(t, c.src, c.want)
	}
}
