package hostgen

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/lexer"
	"github.com/nomi-language/nomi/internal/parser"
	"github.com/nomi-language/nomi/rt"
)

// TestTaggedLayoutMatchesRtTagConstants holds the tag the generator assigns
// each variant of rt's tagged-struct enums (declaration position plus one) to
// the constant rt itself spells for that variant. The generator derives its
// tag from the Nomi declaration and rt writes its own; this is where the two
// meet.
func TestTaggedLayoutMatchesRtTagConstants(t *testing.T) {
	cases := []struct {
		module, enum string
		goType       reflect.Type
		tags         map[string]uint8
	}{
		{"json", "Json", reflect.TypeFor[rt.Json](), map[string]uint8{
			"String": rt.TagJsonString, "Int": rt.TagJsonInt, "Float": rt.TagJsonFloat, "Bool": rt.TagJsonBool,
			"Arr": rt.TagJsonArr, "Obj": rt.TagJsonObj, "Null": rt.TagJsonNull}},
		{"comparable", "Ordering", reflect.TypeFor[rt.Ordering](), map[string]uint8{
			"Less": rt.TagLess, "Equal": rt.TagEqual, "Greater": rt.TagGreater}},
		{"supervisors", "Restart", reflect.TypeFor[rt.Restart](), map[string]uint8{
			"Temporary": rt.TagTemporary, "Transient": rt.TagTransient, "Permanent": rt.TagPermanent}},
		{"supervisors", "Backoff", reflect.TypeFor[rt.Backoff](), map[string]uint8{
			"Exponential": rt.TagExponential}},
		{"supervisors", "GiveUp", reflect.TypeFor[rt.GiveUp](), map[string]uint8{
			"Report": rt.TagReport, "Exit": rt.TagExit}},
		{"supervisors", "Wait", reflect.TypeFor[rt.Wait](), map[string]uint8{
			"Forever": rt.TagForever, "UpTo": rt.TagUpTo}},
		{"supervisors", "FlushOutcome", reflect.TypeFor[rt.FlushOutcome](), map[string]uint8{
			"Flushed": rt.TagFlushed, "TimedOut": rt.TagTimedOut}},
	}
	var mods []*Module
	load := func(name string) *Module {
		src, ok := stdSource(name)
		if !ok {
			t.Fatalf("no std/%s", name)
		}
		nodes, err := parser.Parse(lexer.Lex(string(src)))
		if err != nil {
			t.Fatal(err)
		}
		return &Module{Name: name, Nodes: nodes}
	}
	for _, name := range []string{"json", "comparable", "supervisors", "duration", "maybe", "results"} {
		mods = append(mods, load(name))
	}
	res := NewResolver(mods...)
	for _, c := range cases {
		s, err := res.named(res.Modules[c.module], c.enum, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.enum, err)
		}
		tvs, err := taggedLayout(s, ReflectType(c.goType))
		if err != nil {
			t.Fatalf("%s: %v", c.enum, err)
		}
		if len(tvs) != len(c.tags) {
			t.Errorf("%s: %d variants, rt spells %d", c.enum, len(tvs), len(c.tags))
		}
		for _, tv := range tvs {
			if want, ok := c.tags[tv.Name]; !ok || int(want) != tv.tag {
				t.Errorf("%s.%s: the generator writes tag %d, rt spells %d", c.enum, tv.Name, tv.tag, want)
			}
		}
	}
}

// TestTaggedLayoutRefusesDrift plants a Go tagged struct that disagrees with
// its enum each way and requires generation to refuse.
func TestTaggedLayoutRefusesDrift(t *testing.T) {
	src := `pub enum Shape {
  Dot
  Circle Float
}
`
	nodes, err := parser.Parse(lexer.Lex(src))
	if err != nil {
		t.Fatal(err)
	}
	res := NewResolver(&Module{Name: "t", Nodes: nodes})
	s, err := res.named(res.Modules["t"], "Shape", nil)
	if err != nil {
		t.Fatal(err)
	}
	// rt.Wait has an UpTo field no variant of Shape claims, and no Circle.
	if _, err := taggedLayout(s, ReflectType(reflect.TypeFor[rt.Wait]())); err == nil || !strings.Contains(err.Error(), "has no field for t.Shape.Circle") {
		t.Errorf("a missing payload field: %v", err)
	}
	// A struct outside rt is not the tagged encoding, whatever its fields.
	type local struct{ Tag uint8 }
	if _, err := taggedLayout(s, ReflectType(reflect.TypeFor[local]())); err == nil || !strings.Contains(err.Error(), "a user enum does not cross") {
		t.Errorf("a non-rt struct: %v", err)
	}
	// An enum whose variants all fit, against a Go struct with a field none
	// of them claims.
	bare := `pub enum Two {
  A
  B
}
`
	nodes, _ = parser.Parse(lexer.Lex(bare))
	res = NewResolver(&Module{Name: "u", Nodes: nodes})
	two, err := res.named(res.Modules["u"], "Two", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taggedLayout(two, ReflectType(reflect.TypeFor[rt.Wait]())); err == nil || !strings.Contains(err.Error(), "which no variant of u.Two claims") {
		t.Errorf("an unclaimed Go field: %v", err)
	}
}
