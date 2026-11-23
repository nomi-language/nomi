// Package hostadapt is the runtime half of generated host-function adapters:
// Go functions that call a Go host function with rt values and no reflection.
//
// An adapter is generated from a Nomi `host fn` declaration and the Go
// function its binding names (internal/hostgen). It reads its operands as
// rt.Value, converts each to the Go parameter type the declaration projects
// to, calls the Go function directly, and converts the Go results back. The
// projection rules are these (internal/hostgen package doc, "Projection
// rows"): a Go multiple return is a tuple, a trailing `error` wraps the rest
// in `Result<_, String>`, a `*T` result is `Option<T>`, a Go struct is the
// Nomi struct the declaration names, and a registered host type is an opaque
// handle. A std row's Go function, linked into the generator, may also
// answer a tuple as an anonymous struct of fields F0, F1, ..., as an
// rt.Option's payload (`rt.Option[struct{ F0 rt.Int; F1 int64 }]` is
// `Option<(Int, Int)>`), which is how internal/irbuild reads an rt
// signature's tuple.
//
// Every binding of at most MaxSlots operands is generated as its register
// form (RegFunc) instead: the
// same call over operands and an answer kept unboxed in Slots, as an
// engine's registers hold them. An Int that fits int64, a Float, a Bool, a
// Byte, a String and a Bytes cross it without a box, a tuple answers its
// components and an Option or a Result its payload and whether it carries
// one, so an engine that reads and writes registers builds no box, no
// tuple and no Some or Ok for the call. A generated table lists these
// forms as rows (Row), and Bind hands each to Env.AddRegs with the slot
// types of its operands and its answer (Regs, BindRows). The binding's
// adapter is that form over boxed values (Regs.Func): its operands laid
// into slots, refused as a conversion refuses a value of another kind,
// and its answer built whole from the parts (Regs.Answer), which an
// engine that keeps the answer whole calls too. So each binding's
// conversions are written once, and its two forms fail alike.
//
// This package is public because an embedder is a separate Go
// module and can import only public packages. It imports rt and nothing else
// from this repository, so an adapter links no front end.
//
// # Descriptors
//
// A record an adapter builds needs a *rt.TypeDesc, and the descriptor is not
// the adapter's to invent: an engine that reads a field at a fixed offset or
// matches a variant by descriptor pointer needs the adapter to build with the
// same descriptor it built at link time. So generated code carries a static
// DescSpec per record shape, derived from the Nomi declaration in declaration
// order, and asks the Env for the descriptor once, when the adapters are bound.
// Env's default interns a descriptor per spec; an engine supplies Resolve to
// hand back its own, and Env checks the engine's layout against the spec.
package hostadapt

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/nomi-language/nomi/rt"
)

// Func is one bound adapter: the caller's frame and its operands in, one
// result out. The error is an engine-level failure (an operand of the wrong
// shape, a panic in the Go function); a Go `error` the host function returns
// is data, a `Result.Err`, and never reaches this error.
//
// The frame is the calling activation's. A Go function whose first parameter
// is *rt.Frame receives it (`timer.sleep` waits on its cancellation), and a
// callback the Go function invokes runs on it through the Invoker. An adapter
// for a function that needs neither ignores it, so nil is a legal frame there.
type Func func(fr *rt.Frame, args []rt.Value) (rt.Value, error)

// Invoker calls a Nomi function value with rt operands, on the frame of the
// host call that is calling back. An adapter whose Go function takes a
// callback builds a Go func that calls back through it, so the engine that
// owns function values is the one that calls them.
type Invoker func(fr *rt.Frame, fn rt.Value, args []rt.Value) (rt.Value, error)

// DescSpec is the static description of one record shape an adapter builds or
// reads: a named struct, an enum instantiation (Option<String> and Option<Int>
// are two specs), or a distinct type. Fields and variants are in declaration
// order, which is the layout the descriptor gets.
type DescSpec struct {
	Name     string
	Kind     rt.RecordKind
	Fields   []rt.FieldSpec   // KindStruct
	Variants []rt.VariantSpec // KindEnum
	Inner    *rt.SlotType     // KindDistinct; nil for a marker
}

// Key is a stable text key for the spec's shape, unique per shape.
func (s *DescSpec) Key() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d|%s|", s.Kind, s.Name)
	for _, f := range s.Fields {
		fmt.Fprintf(&b, "%s:%d,", f.Name, f.Type)
	}
	for _, v := range s.Variants {
		fmt.Fprintf(&b, "%s/%d(", v.Name, v.Shape)
		for _, f := range v.Fields {
			fmt.Fprintf(&b, "%s:%d,", f.Name, f.Type)
		}
		b.WriteString(")")
	}
	if s.Inner != nil {
		fmt.Fprintf(&b, "inner:%d", *s.Inner)
	}
	return b.String()
}

// Build makes a fresh descriptor for the spec.
func (s *DescSpec) Build() *rt.TypeDesc {
	switch s.Kind {
	case rt.KindStruct:
		return rt.NewStructDesc(s.Name, s.Fields)
	case rt.KindEnum:
		return rt.NewEnumDesc(s.Name, s.Variants)
	case rt.KindDistinct:
		return rt.NewDistinctDesc(s.Name, s.Inner)
	}
	panic(fmt.Sprintf("hostadapt: DescSpec %q has kind %d, which no adapter builds", s.Name, s.Kind))
}

// Matches reports whether d has exactly the spec's name, kind and layout, so
// an adapter may write its slots by the offsets the spec implies.
func (s *DescSpec) Matches(d *rt.TypeDesc) error {
	if d == nil {
		return errors.New("no descriptor")
	}
	if d.Name != s.Name || d.Kind != s.Kind {
		return fmt.Errorf("descriptor is %s (kind %d), spec is %s (kind %d)", d.Name, d.Kind, s.Name, s.Kind)
	}
	switch s.Kind {
	case rt.KindStruct:
		return layoutMatches("", &d.Layout, s.Fields)
	case rt.KindDistinct:
		if s.Inner == nil {
			return layoutMatches("", &d.Layout, nil)
		}
		return layoutMatches("", &d.Layout, []rt.FieldSpec{{Type: *s.Inner}})
	case rt.KindEnum:
		if len(d.Variants) != len(s.Variants) {
			return fmt.Errorf("%d variants, spec has %d", len(d.Variants), len(s.Variants))
		}
		for i, v := range s.Variants {
			dv := &d.Variants[i]
			if dv.Name != v.Name || dv.Shape != v.Shape {
				return fmt.Errorf("variant %d is %s/%d, spec has %s/%d", i, dv.Name, dv.Shape, v.Name, v.Shape)
			}
			fields := v.Fields
			if v.Shape == rt.VariantBare {
				fields = nil
			}
			if err := layoutMatches(v.Name+": ", &dv.Layout, fields); err != nil {
				return err
			}
		}
	}
	return nil
}

func layoutMatches(where string, l *rt.Layout, fields []rt.FieldSpec) error {
	if len(l.Fields) != len(fields) {
		return fmt.Errorf("%s%d fields, spec has %d", where, len(l.Fields), len(fields))
	}
	for i, f := range fields {
		if l.Fields[i].Name != f.Name || l.Fields[i].Type != f.Type {
			return fmt.Errorf("%sfield %d is %q slot %d, spec has %q slot %d",
				where, i, l.Fields[i].Name, l.Fields[i].Type, f.Name, f.Type)
		}
	}
	return nil
}

// Env is what binding a table of adapters needs from the engine: where
// descriptors come from, and how to call a function value.
type Env struct {
	// Resolve, when set, answers the engine's descriptor for a spec. Nil
	// builds one per spec and interns it in this Env.
	Resolve func(*DescSpec) (*rt.TypeDesc, error)
	// Invoke calls a Nomi function value. Nil fails every callback.
	Invoke Invoker
	// AddRegs, when set, receives the register form of each binding that
	// has one, by binding name, as Bind runs.
	AddRegs func(name string, r Regs)

	mu    sync.Mutex
	descs map[string]*rt.TypeDesc
	// bySpec is each spec's answer: a table asks for one more than once.
	bySpec map[*DescSpec]*rt.TypeDesc
}

// Desc answers the descriptor for spec, checked against the spec's layout.
func (e *Env) Desc(spec *DescSpec) (*rt.TypeDesc, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if d, ok := e.bySpec[spec]; ok {
		return d, nil
	}
	d, err := e.desc(spec)
	if err != nil {
		return nil, err
	}
	if e.bySpec == nil {
		e.bySpec = map[*DescSpec]*rt.TypeDesc{}
	}
	e.bySpec[spec] = d
	return d, nil
}

func (e *Env) desc(spec *DescSpec) (*rt.TypeDesc, error) {
	if e.Resolve != nil {
		d, err := e.Resolve(spec)
		if err != nil {
			return nil, fmt.Errorf("descriptor for %s: %w", spec.Name, err)
		}
		if err := spec.Matches(d); err != nil {
			return nil, fmt.Errorf("descriptor for %s does not match the declaration: %w", spec.Name, err)
		}
		return d, nil
	}
	key := spec.Key()
	if d, ok := e.descs[key]; ok {
		return d, nil
	}
	if e.descs == nil {
		e.descs = map[string]*rt.TypeDesc{}
	}
	d := spec.Build()
	e.descs[key] = d
	return d, nil
}

// Call invokes fn through the Env's Invoker.
func (e *Env) Call(fr *rt.Frame, fn rt.Value, args []rt.Value) (rt.Value, error) {
	if e == nil || e.Invoke == nil {
		return nil, errors.New("this engine supplied no Invoker, so a host function cannot call back into Nomi")
	}
	return e.Invoke(fr, fn, args)
}
