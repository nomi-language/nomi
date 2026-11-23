package irbuild

import "testing"

// An impl of a generic interface belongs to the type its header names,
// whichever file writes it and however the using file names the type: a
// `try_from`, a `+` and a checked literal reach another file's type
// through a selective import, and an `impl Add<lib.Money, lib.Money> for
// lib.Money` written beside the caller is lib's Money's.
func TestGenericImplIdentity_AnotherFilesTypeKeepsItsImpls(t *testing.T) {
	verifyLambdaProgram(t, `import
    std::io
    lib.{Tag, Weekday}
    lib
end

impl Add<lib.Money, lib.Money> for lib.Money
    fn add(lhs: lib.Money, rhs: lib.Money) -> lib.Money
        lib.Money{cents: lhs.cents + rhs.cents}
    end
end

fn main()
    io.print("${Weekday.try_from(2) == Some(Weekday.Tuesday)}")
    total = lib.Money{cents: 1} + lib.Money{cents: 2}
    io.print("${total.cents}")
    t = Tag"hello"
    io.print(t.text)
end
`, "True\n3\nbuilt by lib from 1 fragment\n", map[string]string{"lib.nomi": `import
    std::literals.{CheckedLiteral, Fragment, LiteralError}
end

pub enum Weekday
    Monday
    Tuesday
end

impl TryFrom<Int> for Weekday
    fn try_from(value: Int) -> Option<Weekday>
        case value
            1 -> Some(Weekday.Monday)
            2 -> Some(Weekday.Tuesday)
            _ -> None
        end
    end
end

pub struct Money
    cents: Int
end

pub struct Tag
    text: String
end

impl CheckedLiteral<String, Tag> for Tag
    fn check(statics: List<String>) -> Result<Unit, LiteralError>
        _ = statics
        Ok(Unit)
    end

    fn build(fragments: List<Fragment<String>>) -> Tag
        Tag{text: "built by lib from ${List.length(fragments)} fragment"}
    end
end
`})
}
