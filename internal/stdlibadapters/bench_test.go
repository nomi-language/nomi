package stdlibadapters

import (
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/rt"
)

// The per-call cost of one crossing through an adapter, on three shapes: a
// struct in and an Int out, a struct in and a struct out, and a handle and a
// String in with a Bool out. Record operands are laid onto the adapters' own
// descriptors, as an engine that links descriptors hands them over.
//
//	go test ./internal/stdlibadapters -run XXX -bench Crossing
func BenchmarkCrossing(b *testing.B) {
	env := &hostadapt.Env{}
	table, err := Bind(env)
	if err != nil {
		b.Fatal(err)
	}
	byName := map[string]*rt.TypeDesc{}
	for _, spec := range Specs() {
		d, _ := env.Desc(spec)
		byName[spec.Name] = d
	}
	zone := zoned(1_700_000_000_000_000_000, "America/New_York")
	day := date(2024, 2, 29)
	compiled, err := table["Regex.compile"](nil, []rt.Value{"a+b"})
	if err != nil {
		b.Fatal(err)
	}
	re := compiled.(*rt.Record).Field(0)
	cases := []struct {
		name string
		args []rt.Value
	}{
		{"DateTime.year", []rt.Value{zone}},
		{"calendar.date_add_days", []rt.Value{day, int64(40)}},
		{"Regex.match?", []rt.Value{re, "xxaab"}},
	}
	for _, c := range cases {
		rtArgs := make([]rt.Value, len(c.args))
		for k, x := range c.args {
			rtArgs[k] = x
			if r, ok := x.(*rt.Record); ok {
				if d := byName[r.Desc.Name]; d != nil {
					vals := make([]any, len(d.Fields))
					for f := range d.Fields {
						vals[f], _ = r.FieldNamed(d.Fields[f].Name)
					}
					rtArgs[k] = d.Make(vals...)
				}
			}
		}
		fn := table[c.name]
		b.Run(c.name+"/adapter", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := fn(nil, rtArgs); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
