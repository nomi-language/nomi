package vmhost_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// runModule loads files as a module and runs its main.nomi.
func runModule(t *testing.T, files map[string]string) string {
	t.Helper()
	p, err := loadModule(t, files)
	if err != nil {
		t.Fatalf("the front end refuses this program: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	return out.String()
}

// refusesModule loads files as a module and requires an error holding want.
func refusesModule(t *testing.T, files map[string]string, want string) {
	t.Helper()
	_, err := loadModule(t, files)
	if err == nil {
		t.Fatal("the front end admits this program")
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q lacks %q", err, want)
	}
}

// The value ranges of std's Duration functions follow the type a call's
// qualifier names. A program's own type imported as `Duration` gets none:
// read by the name, its `abs` was taken for std's, its result for a
// positive Duration, and `10 % n` was admitted and then divided by zero.
// std's Duration imported as `Dur` keeps them, so the same `%` compiles.
func TestQualifierIdentity_DurationRangesFollowTheType(t *testing.T) {
	refusesModule(t, map[string]string{
		"tally.nomi": `import std::duration.{Duration, PositiveDuration}

pub struct Tally
    n: Int
end

impl Tally
    pub fn abs(_d: Duration) -> Duration
        Duration.seconds(0)
    end

    pub fn as_nanos(d: Duration) -> Int
        Duration.as_nanos(d)
    end
end

pub fn five() -> PositiveDuration
    PositiveDuration.try_from(Duration.seconds(5))!
end
`,
		"main.nomi": `import
    std::io
    std::duration.{PositiveDuration}
    tally.{self, Tally as Duration}
end

fn share(d: PositiveDuration) -> Int
    n = Duration.as_nanos(Duration.abs(d))
    10 % n
end

fn main()
    io.print(share(tally.five()))
end
`,
	}, "`%` divides by a NonZeroInt; `n` is an Int")

	out := runModule(t, map[string]string{
		"main.nomi": `import
    std::io
    std::duration.{Duration as Dur, PositiveDuration}
end

fn share(d: PositiveDuration) -> Int
    n = Dur.as_nanos(Dur.abs(d))
    10 % n
end

fn main()
    io.print(share(PositiveDuration.try_from(Dur.nanoseconds(3))!))
end
`,
	})
	if out != "1\n" {
		t.Fatalf("output %q, want %q", out, "1\n")
	}
}

// A `Struct.update` patch's fields are update positions, where a bare `..`
// is the field's value, by the interface the qualifier names. std's
// Struct imported as `Rec` keeps them; read by the name, `Rec.update`'s
// patch refused its `..`. A program's own type imported as `Struct` has
// none; read by the name, its patch admitted a `..` the builder then
// declined.
func TestQualifierIdentity_StructUpdatePatchFollowsTheInterface(t *testing.T) {
	const shapes = `pub struct Inner
    a: Int
    b: Int
end

pub struct Outer
    inner: Inner
    n: Int
end
`
	out := runModule(t, map[string]string{
		"shapes.nomi": shapes,
		"main.nomi": `import
    std::io
    std::structs.{Struct as Rec}
    shapes.{Inner, Outer}
end

fn main()
    o = Outer{inner: Inner{a: 1, b: 2}, n: 3}
    p = Rec.update(o, {inner: {.., a: 9}})
    io.print("${p.inner.a} ${p.inner.b} ${p.n}")
end
`,
	})
	if out != "9 2 3\n" {
		t.Fatalf("output %q, want %q", out, "9 2 3\n")
	}

	refusesModule(t, map[string]string{
		"shapes.nomi": shapes + `
pub struct Patcher
    n: Int
end

impl Patcher
    pub fn update(o: Outer, p: {inner: Inner}) -> Outer
        {..o, inner: p.inner}
    end
end
`,
		"main.nomi": `import
    std::io
    shapes.{Inner, Outer, Patcher as Struct}
end

fn main()
    o = Outer{inner: Inner{a: 1, b: 2}, n: 3}
    p = Struct.update(o, {inner: {.., a: 9}})
    io.print(p.inner.a)
end
`,
	}, "a bare `..` has no value to update here")
}
