package format

import "testing"

// A lambda whose body is one discard, `_ = expr`, keeps the line its body was
// written on: on the `->`'s line it stays there while it fits, as an
// expression body does, and below the `->` it stays below, however short.
func TestFormat_LambdaDiscardBodyKeepsItsLine(t *testing.T) {
	for name, src := range map[string]string{
		"a tail argument": "fn main()\n" +
			"    Iter.each(items, fn i -> _ = Sender.send(out, i))\n" +
			"end\n",
		"bound to a name": "fn main()\n" +
			"    f = fn i: Int -> _ = check(i)\n" +
			"    f(1)\n" +
			"end\n",
		"a then stage": "fn main()\n" +
			"    5\n" +
			"    |> then fn v -> _ = check(v)\n" +
			"    |> then fn u -> u\n" +
			"end\n",
		"a pipe in the value": "fn main()\n" +
			"    f = fn i: Int -> _ = check(i) |> Result.is_ok()\n" +
			"    f(1)\n" +
			"end\n",
		"below the arrow": "fn main()\n" +
			"    Iter.each(items, fn i ->\n" +
			"        _ = Sender.send(out, i)\n" +
			"    end)\n" +
			"end\n",
	} {
		t.Run(name, func(t *testing.T) { roundTrip(t, src) })
	}
}

// A discard body too long for the parameters' line moves below them, closed
// by `end`, as an expression body does, and stays there.
func TestFormat_LambdaDiscardBodyTooLongMovesBelow(t *testing.T) {
	label := `"a rather long label that pushes the line well past the width it may have"`
	migrates(t,
		"fn main()\n"+
			"    Iter.each(items, fn i -> _ = check(i, "+label+"))\n"+
			"end\n",
		"fn main()\n"+
			"    Iter.each(items, fn i ->\n"+
			"        _ = check(i, "+label+")\n"+
			"    end)\n"+
			"end\n")
}
