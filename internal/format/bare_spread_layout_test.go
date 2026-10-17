package format

import "testing"

// A bare `..` written last in a vector ends it: the parser takes no comma
// after it, as after a list's `..tail`. nomi fmt wrote a broken vector
// ending in `..` with the trailing comma every broken list gets, `..,`,
// which does not parse ("vector spread `..` must be the first or the last
// element"). A comment ending the `..`'s line is the `..`'s own; the
// parser gave it to the element before, so it moved above the `..`. The
// same comments and breaks around a list's tail, a set's, a map's and a
// struct's `..` already kept their place.
func TestFormat_BareSpreadLayout(t *testing.T) {
	const head = "fn f(p: P, t: Int) -> P {\n"
	const tail = "}\n"
	for _, c := range []struct{ name, src, want string }{
		{
			"a vector's last `..` after a comment",
			head + "    {..p, recent: #[ // a\n        6,\n        // b\n        ..]}\n" + tail,
			head + "    {..p, recent: #[\n        // a\n        6,\n        // b\n        ..\n    ]}\n" + tail,
		},
		{
			"a comment ending a vector's last `..`",
			head + "    {..p, recent: #[6, .. // a\n    ]}\n" + tail,
			head + "    {..p, recent: #[\n        6,\n        .. // a\n    ]}\n" + tail,
		},
		{
			"a comment below a vector's last `..`",
			head + "    {..p, recent: #[6, ..\n        // a\n    ]}\n" + tail,
			head + "    {..p, recent: #[\n        6,\n        ..\n        // a\n    ]}\n" + tail,
		},
		{
			"a vector's last `..` broken for width",
			head + "    {..p, recent: #[1111111111111111, 2222222222222222, 3333333333333333, 4444444444444444, 5555555555555555, ..]}\n" + tail,
			head + "    {\n        ..p,\n        recent: #[\n            1111111111111111,\n            2222222222222222,\n            3333333333333333,\n            4444444444444444,\n            5555555555555555,\n            ..\n        ],\n    }\n" + tail,
		},
		{
			"a vector's first `..` with a comment",
			head + "    {..p, recent: #[.., // a\n        6]}\n" + tail,
			head + "    {..p, recent: #[\n        .., // a\n        6,\n    ]}\n" + tail,
		},
		{
			"a list's tail with comments",
			head + "    {..p, log: [\n        t, // a\n        // b\n        .. // c\n    ]}\n" + tail,
			head + "    {..p, log: [\n        t, // a\n        // b\n        .. // c\n    ]}\n" + tail,
		},
		{
			"a set's `..` with comments",
			head + "    {..p, seen: #{ // a\n        .., // b\n        // c\n        7}}\n" + tail,
			head + "    {..p, seen: #{\n        // a\n        .., // b\n        // c\n        7,\n    }}\n" + tail,
		},
		{
			"a map's `..` with comments",
			head + "    {..p, notes: %{\n        .., // a\n        // b\n        \"k\" => \"v\"}}\n" + tail,
			head + "    {..p, notes: %{\n        .., // a\n        // b\n        \"k\" => \"v\",\n    }}\n" + tail,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}
