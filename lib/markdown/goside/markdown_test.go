package goside

import "testing"

func TestToHTML(t *testing.T) {
	cases := map[string]string{
		"# Title\n\nSome *text*.":            "<h1>Title</h1>\n<p>Some <em>text</em>.</p>\n",
		"see https://example.com/a now":      "<p>see <a href=\"https://example.com/a\">https://example.com/a</a> now</p>\n",
		"| a | b |\n|---|---|\n| 1 | 2 |":    "<table>\n<thead>\n<tr>\n<th>a</th>\n<th>b</th>\n</tr>\n</thead>\n<tbody>\n<tr>\n<td>1</td>\n<td>2</td>\n</tr>\n</tbody>\n</table>\n",
		"hi <script>alert(1)</script> there": "<p>hi <!-- raw HTML omitted -->alert(1)<!-- raw HTML omitted --> there</p>\n",
		"[x](javascript:alert(1))":           "<p><a href=\"\">x</a></p>\n",
		"a & b < c":                          "<p>a &amp; b &lt; c</p>\n",
	}
	for in, want := range cases {
		if got := ToHTML(in); got != want {
			t.Errorf("ToHTML(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}
