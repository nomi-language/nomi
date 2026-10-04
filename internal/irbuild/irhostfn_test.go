package irbuild

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	stdtime "time"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// The corpus's Go-bound `host fn` programs, run on the VM.
//
// `nomi run` runs each through ffirun's wrapper binary, which links the
// project's own Go package and calls it through the adapters it generates. This test
// binary cannot link it: each project is its own Go module, outside this
// one's module graph. So the machine binds a TRANSCRIPTION of each project's
// binding.go as a host table over rt values. What the VM leg checks is what
// irbuild and the VM own: that the retained caller links the host fn's body,
// that the body crosses under the key the wrapper's table is keyed on, and
// that the values crossing are the machine's own. It does not check the
// project's Go code or its generated adapters (internal/ffirun's
// TestGoSourceTypesGenerateTheReflectedAdapters does that for the generator).

type irHostFnBox struct{ label string }

var irHostFnDuration = &hostadapt.DescSpec{Name: "duration.Duration", Kind: rt.KindDistinct, Inner: hostadapt.Slot(rt.SlotInt)}
var irHostFnInstant = &hostadapt.DescSpec{Name: "instant.Instant", Kind: rt.KindDistinct, Inner: hostadapt.Slot(rt.SlotInt)}

// irHostFnDistinct is a table over one distinct-over-Int type: a nullary
// binding answering n, and a unary one answering f of its operand.
func irHostFnDistinct(spec *hostadapt.DescSpec, nullary string, n int64, unary string, f func(int64) int64) vm.HostTable {
	return func(env *hostadapt.Env) (map[string]hostadapt.Func, error) {
		d, err := env.Desc(spec)
		if err != nil {
			return nil, err
		}
		make := func(v int64) rt.Value {
			r := d.New()
			r.W[0] = rt.IntWord(v)
			return r
		}
		return map[string]hostadapt.Func{
			nullary: func(*rt.Frame, []rt.Value) (rt.Value, error) { return make(n), nil },
			unary: func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
				r, err := hostadapt.Record(args[0], spec.Name)
				if err != nil {
					return nil, err
				}
				x, err := hostadapt.IntField(r, d, 0, 0, "")
				if err != nil {
					return nil, err
				}
				return make(f(x)), nil
			},
		}, nil
	}
}

var irHostFnPrograms = []struct {
	dir      string
	want     string
	bindings []string
	hosts    vm.HostTable
}{
	{"duration_ffi_app", "3000\n", []string{"ffi.timeout", "ffi.double"},
		irHostFnDistinct(irHostFnDuration, "ffi.timeout", int64(1500*stdtime.Millisecond), "ffi.double", func(d int64) int64 { return d * 2 })},
	{"time_ffi_app", "1700000001\n", []string{"ffi.created_at", "ffi.plus_second"},
		irHostFnDistinct(irHostFnInstant, "ffi.created_at", stdtime.Unix(1700000000, 250000000).UnixNano(), "ffi.plus_second", func(t int64) int64 { return t + int64(stdtime.Second) })},
	{"inline_go_block_app", "INLINE\n", []string{"ffi.echo_upper", "ffi.make_box_raw", "ffi.box_label_raw"}, irHostFnBoxBindings},
	{"tagged_ffi_app", "FIXTURE\n", []string{"ffi.echo_upper", "ffi.make_box_raw", "ffi.box_label_raw"}, irHostFnBoxBindings},
}

func irHostFnBoxBindings(*hostadapt.Env) (map[string]hostadapt.Func, error) {
	return map[string]hostadapt.Func{
		"ffi.echo_upper": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
			return strings.ToUpper(args[0].(string)), nil
		},
		"ffi.make_box_raw": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
			return rt.HostHandle{TypeName: "ffi.RawBox", Value: &irHostFnBox{args[0].(string)}}, nil
		},
		"ffi.box_label_raw": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
			h, err := hostadapt.Handle(args[0], "ffi.RawBox")
			if err != nil {
				return nil, err
			}
			return h.(*irHostFnBox).label, nil
		},
	}, nil
}

func TestIRHostFn_GoBoundProgramsRunOnTheVM(t *testing.T) {
	for _, tc := range irHostFnPrograms {
		t.Run(tc.dir, func(t *testing.T) {
			path, err := filepath.Abs(filepath.Join("..", "..", "tests", "18-ffi-and-dynamic", tc.dir, "main.nomi"))
			if err != nil {
				t.Fatal(err)
			}
			p, err := Analyze(path)
			if err != nil {
				t.Fatal(err)
			}
			res, _, err := GenerateIR(p)
			if err != nil {
				t.Fatal(err)
			}

			// Every binding is a retained body whose whole work is one
			// crossing under the runtime's key.
			crossings := map[string]bool{}
			var entry *ir.Module
			for _, m := range res.IR {
				for _, f := range m.Funcs() {
					if f.Name() == "main" {
						entry = m
					}
					for _, b := range f.Blocks() {
						for _, in := range b.Instrs() {
							if c, isCall := in.(*ir.Call); isCall && c.Crosses() && strings.HasPrefix(c.Callee().Name(), "ffi.") {
								crossings[c.Callee().Name()] = true
							}
						}
					}
				}
			}
			for _, key := range tc.bindings {
				if !crossings[key] {
					t.Errorf("no retained body crosses into %s; crossings %v", key, crossings)
				}
			}
			if entry == nil {
				t.Fatal("main was not retained")
			}

			var out bytes.Buffer
			booted, err := vm.NewProgram(entry, res.IRModules(), &out).WithHosts(tc.hosts).Boot()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := booted.Run("main"); err != nil {
				t.Fatalf("VM: %v", err)
			}
			if out.String() != tc.want {
				t.Fatalf("VM output %q; want %q", out.String(), tc.want)
			}

			if got := vmReference(path); got.exit != 0 || got.stdout != tc.want || got.stderr != "" {
				t.Fatalf("vm command: %s; want %q", got, tc.want)
			}
		})
	}
}

// Without the binding the crossing reports that this machine binds none,
// which is the reading an in-process host that did not link the project's Go
// package gets. The call itself links.
func TestIRHostFn_AnUnboundCrossingIsReportedNotLinked(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("..", "..", "tests", "18-ffi-and-dynamic", "inline_go_block_app", "main.nomi"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Analyze(path)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := GenerateIR(p)
	if err != nil {
		t.Fatal(err)
	}
	var entry *ir.Module
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() == "main" {
				entry = m
			}
		}
	}
	if entry == nil {
		t.Fatal("main was not retained")
	}
	_, err = vm.NewProgram(entry, res.IRModules(), &bytes.Buffer{}).Run("main")
	if err == nil || !strings.Contains(err.Error(), "ffi.echo_upper crosses into Go and this machine binds no implementation") {
		t.Fatalf("an unbound user binding answered %v", err)
	}
}
