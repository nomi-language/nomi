package irbuild

import "testing"

// A parameter that takes apart a single-variant enum whose payload has no
// slot binds the payload's one value: Unit, a marker, and a marker an
// `embeds` variant carries, in a function, an impl function and a lambda.
func TestSlotlessPayloadParameter_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import std::io

type Mark

enum Only
    One Unit
end

enum OnlyMark
    One Mark
end

enum Embeds
    embeds Mark
end

fn open(Only.One(p)) -> Unit
    p
end

fn open_mark(OnlyMark.One(p)) -> Mark
    p
end

fn open_embedded(Embeds.Mark(p)) -> Mark
    p
end

struct Opener end

impl Opener
    fn open(Only.One(p)) -> String
        Debug.inspect(p)
    end
end

fn main()
    io.inspect(open(Only.One(Unit)))
    io.inspect(open_mark(OnlyMark.One(Mark)))
    io.inspect(open_embedded(Embeds.Mark))
    io.print(Opener.open(Only.One(Unit)))
    f = fn Only.One(p) -> Debug.inspect(p)
    io.print(f(Only.One(Unit)))
end
`, "Unit\nMark\nMark\nUnit\nUnit\n")
}
