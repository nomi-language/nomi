package rt

import "testing"

// The design's struct model timed `benchmarks/engines/structs.nomi`'s step
// function, 300,000 steps, per representation. These benchmarks run the same
// step over rt.Record so the numbers compare directly:
//
//	native Go value struct                              0.59 ms  0 allocs
//	descriptor plus split banks (the model)             9.7 ms   0.60 M allocs
//
// One benchmark op is the whole 300,000-step loop.

const engineSteps = 300000

type nativeParticle struct{ x, y, dx, dy int64 }

func nativeStep(p nativeParticle) nativeParticle {
	nx, ny := p.x+p.dx, p.y+p.dy
	ndx, ndy := p.dx, p.dy
	if nx > 1000 || nx < 0 {
		ndx = 0 - p.dx
	}
	if ny > 1000 || ny < 0 {
		ndy = 0 - p.dy
	}
	return nativeParticle{nx, ny, ndx, ndy}
}

var sinkInt int64

func BenchmarkStructs_NativeGo(b *testing.B) {
	for b.Loop() {
		p := nativeParticle{1, 2, 3, 5}
		for range engineSteps {
			p = nativeStep(p)
		}
		sinkInt = p.x + p.y
	}
}

// recordStep is the step as the bytecode would run it: PROJ_W reads by
// offset, one New, four word writes. The register holding the particle is
// erased (`any`), which is what a record in the ref bank is.
func recordStep(d *TypeDesc, reg any) any {
	p := reg.(*Record)
	x, y, dx, dy := WordInt(p.W[0]), WordInt(p.W[1]), WordInt(p.W[2]), WordInt(p.W[3])
	nx, ny := x+dx, y+dy
	ndx, ndy := dx, dy
	if nx > 1000 || nx < 0 {
		ndx = 0 - dx
	}
	if ny > 1000 || ny < 0 {
		ndy = 0 - dy
	}
	q := d.New()
	q.W[0], q.W[1], q.W[2], q.W[3] = IntWord(nx), IntWord(ny), IntWord(ndx), IntWord(ndy)
	return q
}

func BenchmarkStructs_RecordTypedSlots(b *testing.B) {
	d := particleDesc()
	b.ReportAllocs()
	for b.Loop() {
		var p any = d.Make(int64(1), int64(2), int64(3), int64(5))
		for range engineSteps {
			p = recordStep(d, p)
		}
		r := p.(*Record)
		sinkInt = WordInt(r.W[0]) + WordInt(r.W[1])
	}
}

// The boxed path: every read and write through Field and Make, the cost a
// converter or an unspecialized instruction pays.
func BenchmarkStructs_RecordBoxed(b *testing.B) {
	d := particleDesc()
	b.ReportAllocs()
	for b.Loop() {
		p := d.Make(int64(1), int64(2), int64(3), int64(5))
		for range engineSteps {
			x, y := p.Field(0).(int64), p.Field(1).(int64)
			dx, dy := p.Field(2).(int64), p.Field(3).(int64)
			nx, ny := x+dx, y+dy
			ndx, ndy := dx, dy
			if nx > 1000 || nx < 0 {
				ndx = 0 - dx
			}
			if ny > 1000 || ny < 0 {
				ndy = 0 - dy
			}
			p = d.Make(nx, ny, ndx, ndy)
		}
		sinkInt = p.Field(0).(int64)
	}
}

// Struct spread: `{..p, x: p.x + 1}` 300,000 times.
func BenchmarkStructs_RecordWith(b *testing.B) {
	d := particleDesc()
	idx := []int{0}
	vals := []any{nil}
	b.ReportAllocs()
	for b.Loop() {
		p := d.Make(int64(1), int64(2), int64(3), int64(5))
		for i := range engineSteps {
			vals[0] = int64(i)
			p = p.With(idx, vals)
		}
		sinkInt = WordInt(p.W[0])
	}
}

// benchmarks/engines/variants.nomi: construct and match 300,000 variants.
func BenchmarkVariants_Record(b *testing.B) {
	shape := NewEnumDesc("variants.Shape", []VariantSpec{
		{Name: "Circle", Shape: VariantPositional, Fields: []FieldSpec{{Type: SlotInt}}},
		{Name: "Square", Shape: VariantPositional, Fields: []FieldSpec{{Type: SlotInt}}},
		{Name: "Dot"},
	})
	regs := make([]any, 1) // the ref bank the value is written to
	b.ReportAllocs()
	for b.Loop() {
		var total int64
		for i := range int64(engineSteps) {
			var s *Record
			switch i % 3 {
			case 0:
				s = shape.NewVariant(0)
				s.W[0] = IntWord(i % 17)
			case 1:
				s = shape.NewVariant(1)
				s.W[0] = IntWord(i % 13)
			default:
				s = shape.NewVariant(2)
			}
			regs[0] = s
			r := regs[0].(*Record)
			switch {
			case r.Desc == shape && r.Tag == 0:
				total += 3 * WordInt(r.W[0]) * WordInt(r.W[0])
			case r.Desc == shape && r.Tag == 1:
				total += WordInt(r.W[0]) * WordInt(r.W[0])
			default:
				total++
			}
		}
		sinkInt = total
	}
}

var sinkBool bool
var sinkHash uint64

func BenchmarkRecord_EqualAndHash(b *testing.B) {
	d := particleDesc()
	x := d.Make(int64(1), int64(2), int64(3), int64(5))
	y := d.Make(int64(1), int64(2), int64(3), int64(5))
	b.ReportAllocs()
	for b.Loop() {
		sinkBool = Equal(x, y)
		sinkHash = Hash(x)
	}
}
