package format

import "testing"

// A bare `..` written last in a vector ends it with no trailing comma, as a
// list's last spread does: nomi fmt writes a broken vector or list ending in
// a spread without the comma every other broken list gets. A comment ending
// the `..`'s line is the `..`'s own; the
// parser gave it to the element before, so it moved above the `..`. The
// same comments and breaks around a list's tail, a set's, a map's and a
// struct's `..` already kept their place.
func TestFormat_BareSpreadLayout(t *testing.T) {
	const head = "fn f(p: P, t: Int) -> P\n"
	const tail = "end\n"
	for _, c := range []struct{ name, src, want string }{
		{
			"a vector's last `..` after a comment",
			head + "    {..p, recent: Vector[ // a\n        6,\n        // b\n        ..]}\n" + tail,
			head + "    {..p, recent: Vector[\n        // a\n        6,\n        // b\n        ..\n    ]}\n" + tail,
		},
		{
			"a comment ending a vector's last `..`",
			head + "    {..p, recent: Vector[6, .. // a\n    ]}\n" + tail,
			head + "    {..p, recent: Vector[\n        6,\n        .. // a\n    ]}\n" + tail,
		},
		{
			"a comment below a vector's last `..`",
			head + "    {..p, recent: Vector[6, ..\n        // a\n    ]}\n" + tail,
			head + "    {..p, recent: Vector[\n        6,\n        ..\n        // a\n    ]}\n" + tail,
		},
		{
			"a vector's last `..` broken for width",
			head + "    {..p, recent: Vector[1111111111111111, 2222222222222222, 3333333333333333, 4444444444444444, 5555555555555555, ..]}\n" + tail,
			head + "    {\n        ..p,\n        recent: Vector[\n            1111111111111111,\n            2222222222222222,\n            3333333333333333,\n            4444444444444444,\n            5555555555555555,\n            ..\n        ],\n    }\n" + tail,
		},
		{
			"a vector's first `..` with a comment",
			head + "    {..p, recent: Vector[.., // a\n        6]}\n" + tail,
			head + "    {..p, recent: Vector[\n        .., // a\n        6,\n    ]}\n" + tail,
		},
		{
			"a list's tail with comments",
			head + "    {..p, log: [\n        t, // a\n        // b\n        .. // c\n    ]}\n" + tail,
			head + "    {..p, log: [\n        t, // a\n        // b\n        .. // c\n    ]}\n" + tail,
		},
		{
			"a set's `..` with comments",
			head + "    {..p, seen: Set[ // a\n        .., // b\n        // c\n        7]}\n" + tail,
			head + "    {..p, seen: Set[\n        // a\n        .., // b\n        // c\n        7,\n    ]}\n" + tail,
		},
		{
			"a map's `..` with comments",
			head + "    {..p, notes: {\n        .., // a\n        // b\n        \"k\" => \"v\"}}\n" + tail,
			head + "    {..p, notes: {\n        .., // a\n        // b\n        \"k\" => \"v\",\n    }}\n" + tail,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}

// An unnamed list with a spread adds no compound level, so three literals
// around one stay on one line when they fit; broken after its `[` by its
// author, it stays broken.
func TestFormat_SpreadListDepthAndAuthorBreak(t *testing.T) {
	const head = "fn f(z: Z) -> Maybe<Z>\n"
	const tail = "end\n"
	for _, c := range []struct{ name, src, want string }{
		{
			"a struct around a spread list around a struct stays flat",
			head + "    Some(Zipper{focus: z.left, path: [.Up{value: z.value}, ..z.path]})\n" + tail,
			head + "    Some(Zipper{focus: z.left, path: [.Up{value: z.value}, ..z.path]})\n" + tail,
		},
		{
			"nested cons lists stay flat",
			"fn f() -> List<Int>\n    [1, ..[2, ..[3, ..[]]]]\n" + tail,
			"fn f() -> List<Int>\n    [1, ..[2, ..[3, ..[]]]]\n" + tail,
		},
		{
			"a spread list broken after its `[` stays broken",
			head + "    path = [\n        .Up{value: z.value}, ..z.path]\n    Some(Zipper{focus: z.left, path:})\n" + tail,
			head + "    path = [\n        .Up{value: z.value},\n        ..z.path\n    ]\n\n    Some(Zipper{focus: z.left, path:})\n" + tail,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			formatsKeepingMeaning(t, c.src, c.want)
		})
	}
}
