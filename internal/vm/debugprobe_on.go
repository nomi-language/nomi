//go:build nomi_debugprobe

package vm

// The Debug probe: at every activation of a function the probe's target
// says is a synthesized Debug body, it renders the argument as rt's
// structural Debug does too and writes both, so the two implementations
// of one rendering can be compared over every program a test runs. A
// nested value whose type has a Debug body that is not a target renders
// through that body, as the program's own rendering would; a nested value
// of a target type renders structurally, so rt is held to every level.
// Run with NOMI_NO_INLINE=1, since an inlined body opens no activation.

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

const debugProbeOn = true

var (
	probeTarget func(*ir.Func) string
	probeOut    io.Writer
	probeMu     sync.Mutex
	probeClass  sync.Map // *ir.Func -> string
	probeIndex  sync.Map // *Machine -> *probeBodies
)

// SetDebugProbe makes every machine report each activation of a function
// target classes (a non-empty class) to out.
func SetDebugProbe(target func(*ir.Func) string, out io.Writer) {
	probeTarget, probeOut = target, out
}

type probeBodies struct {
	once   sync.Once
	byType map[[2]string]*ir.Func
	byName map[string][]*ir.Func
}

func (m *Machine) probeClassOf(f *ir.Func) string {
	if c, ok := probeClass.Load(f); ok {
		return c.(string)
	}
	c := probeTarget(f)
	probeClass.Store(f, c)
	return c
}

func (m *Machine) probeBodiesOf() *probeBodies {
	v, _ := probeIndex.LoadOrStore(m, &probeBodies{})
	b := v.(*probeBodies)
	b.once.Do(func() {
		b.byType, b.byName = map[[2]string]*ir.Func{}, map[string][]*ir.Func{}
		seen := map[*ir.Func]bool{}
		add := func(f *ir.Func) {
			if f == nil || seen[f] || f.Sym() == nil || len(f.Params()) != 1 {
				return
			}
			seen[f] = true
			impl, ok := f.Sym().Impl()
			if !ok || impl.Interface != "Debug" || impl.Func != "inspect" {
				return
			}
			t := f.TempType(f.Params()[0].Temp)
			var name, inst string
			if d := descOfType(t); d != nil {
				name, inst = d.Name, d.Inst
			} else if t != nil && t.Sym() != nil {
				name = t.Sym().Name()
			}
			if name == "" {
				return
			}
			b.byType[[2]string{name, inst}] = f
			b.byName[name] = append(b.byName[name], f)
		}
		for _, f := range m.mod.Funcs() {
			add(f)
		}
		for _, f := range m.link {
			add(f)
		}
		for mod := range m.modules {
			for _, f := range mod.Funcs() {
				add(f)
			}
		}
	})
	return b
}

type probeCall struct {
	fn    *ir.Func
	class string
	arg   any
}

func (m *Machine) probeEnter(fr *frame) *probeCall {
	if probeTarget == nil || len(fr.fn.Params()) != 1 {
		return nil
	}
	class := m.probeClassOf(fr.fn)
	if class == "" {
		return nil
	}
	arg, err := fr.read(fr.fn.Params()[0].Temp)
	if err != nil {
		return nil
	}
	return &probeCall{fn: fr.fn, class: class, arg: arg}
}

func (m *Machine) execProbed(fr *frame) error {
	p := m.probeEnter(fr)
	stk := fr.stk
	err := m.runBody(fr)
	stk.pop(fr)
	if p != nil && err == nil {
		m.probeLeave(p, stk.ret())
	}
	return err
}

func probeShape(v any) (shape, name string) {
	x := rt.Unbox(v)
	r, ok := x.(*rt.Record)
	if !ok || r == nil {
		if h, ok := x.(rt.HostHandle); ok {
			return "host", h.TypeName
		}
		return fmt.Sprintf("%T", x), ""
	}
	name = r.Desc.Name
	switch r.Desc.Kind {
	case rt.KindStruct:
		shape = "struct"
		if len(r.Layout().Fields) == 0 {
			shape = "struct-empty"
		}
	case rt.KindEnum:
		vd := r.Variant()
		switch vd.Shape {
		case rt.VariantBare:
			shape = "enum:bare"
		case rt.VariantFields:
			shape = "enum:fields"
		case rt.VariantEmbedded:
			shape = "enum:embedded"
		default:
			shape = "enum:positional"
			if pr, ok := r.Field(0).(*rt.Record); ok && pr != nil && pr.Desc.Kind == rt.KindTuple {
				shape = "enum:positional-tuple"
			}
		}
	case rt.KindDistinct:
		switch {
		case r.NumFields() == 0:
			shape = "marker"
		default:
			shape = "distinct"
			switch in := r.Field(0).(type) {
			case *rt.Record:
				if in != nil && in.Desc.Kind == rt.KindTuple {
					shape = "distinct-tuple"
				} else if in != nil {
					shape = "distinct-record"
				}
			case *rt.List[any], rt.Vector[any], rt.Map[any, any]:
				shape = "distinct-container"
			}
		}
	default:
		shape = fmt.Sprintf("kind%d", r.Desc.Kind)
	}
	if r.Desc.Inst != "" {
		shape += "+generic"
	}
	return shape, name
}

func (m *Machine) probeLeave(p *probeCall, out any) {
	got, _ := out.(string)
	bodies := m.probeBodiesOf()
	shape, top := probeShape(p.arg)
	features := map[string]bool{}
	hook := func(v any) (string, bool, error) {
		var name, inst string
		switch x := v.(type) {
		case *rt.Record:
			if x == nil {
				return "", false, nil
			}
			switch x.Desc.Kind {
			case rt.KindStruct, rt.KindEnum, rt.KindDistinct:
				name, inst = x.Desc.Name, x.Desc.Inst
			case rt.KindBoxed:
				features["boxed"] = true
				name = x.Desc.Name
				v = rt.Unbox(x)
			default:
				return "", false, nil
			}
		case rt.HostHandle:
			name = x.TypeName
		case *rt.List[any], rt.Vector[any], rt.Map[any, any]:
			features["container"] = true
			return "", false, nil
		default:
			return "", false, nil
		}
		if name == top {
			features["recursive"] = true
		}
		f := bodies.byType[[2]string{name, inst}]
		if f == nil && len(bodies.byName[name]) == 1 {
			f = bodies.byName[name][0]
		}
		if f == nil {
			features["nobody:"+name] = true
			return "", false, nil
		}
		if m.probeClassOf(f) != "" {
			features["universal"] = true
			return "", false, nil
		}
		features["written"] = true
		res, err := m.call(f, []any{v})
		if err != nil {
			return "", true, err
		}
		s, _ := res.(string)
		return s, true, nil
	}
	// The top-level value is the target's own, which renders structurally.
	// The top-level value renders structurally when it is of the target's
	// own type. An `embeds` enum's body is handed the embedded value, of
	// another type, which renders as that type does.
	first := true
	if d := descOfType(p.fn.TempType(p.fn.Params()[0].Temp)); d != nil && d.Name != top {
		first = false
		features["embedded"] = true
	}
	want, err := rt.DebugText(p.arg, func(v any) (string, bool, error) {
		if first {
			first = false
			return "", false, nil
		}
		return hook(v)
	})
	if err != nil {
		want = "ERROR: " + err.Error()
	}
	var fs []string
	for f := range features {
		fs = append(fs, f)
	}
	sort.Strings(fs)
	verdict := "same"
	if got != want {
		verdict = "DIFF"
	}
	line := strings.Join([]string{verdict, p.class, shape, top, strings.Join(fs, ","), p.fn.Name(),
		strings.ReplaceAll(got, "\n", `\n`), strings.ReplaceAll(want, "\n", `\n`)}, "\t")
	probeMu.Lock()
	fmt.Fprintln(probeOut, line)
	probeMu.Unlock()
}
