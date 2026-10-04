// Package compilerhosts is std/compiler's host functions for the VM.
//
// internal/irbuild retains a call to `compiler.check`, `check_project`,
// `hover`, `run` or `run_file` as a crossing named by its stdlib key
// (irCompilerHost). nomi/vmhost adds this package's table to every machine it
// opens (vm.Machine.WithHosts). They are not internal/stdlibbindings rows
// because their answer depends on the program being run: the project root and
// the virtual sibling files a checked or run source resolves imports against.
// So they are hand-written adapters over rt values, closed over those two,
// rather than generated ones.
//
// The analysis is nomi/stdcompiler's and the run is nomi/stdcompilerrun's,
// whose engine nomi/vmhost installs. What is here is the conversion between
// those and the VM's rt values. A separate package from vmhost so that a test
// of internal/irbuild, which vmhost imports, can bind them too.
package compilerhosts

import (
	"fmt"

	"github.com/nomi-language/nomi/rt"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/stdcompiler"
	"github.com/nomi-language/nomi/internal/stdcompilerrun"
)

var (
	diagnosticSpec = &hostadapt.DescSpec{Name: "compiler.Diagnostic", Kind: rt.KindStruct, Fields: []rt.FieldSpec{
		{Name: "line", Type: rt.SlotInt}, {Name: "col", Type: rt.SlotInt}, {Name: "message", Type: rt.SlotString}}}
	hoverSpec = &hostadapt.DescSpec{Name: "compiler.Hover", Kind: rt.KindStruct, Fields: []rt.FieldSpec{
		{Name: "signature", Type: rt.SlotString}, {Name: "markdown", Type: rt.SlotString}}}
	resultSpec = &hostadapt.DescSpec{Name: "results.Result", Kind: rt.KindEnum, Variants: []rt.VariantSpec{
		{Name: "Ok", Shape: rt.VariantPositional, Fields: []rt.FieldSpec{{Type: rt.SlotRef}}},
		{Name: "Err", Shape: rt.VariantPositional, Fields: []rt.FieldSpec{{Type: rt.SlotRef}}}}}
)

// Names are the keys Table answers, which internal/irbuild's IR builder admits
// as std/compiler crossings.
func Names() []string {
	return []string{"compiler.check", "compiler.check_project", "compiler.hover", "compiler.run", "compiler.run_file"}
}

// descs are the descriptors one binding builds with.
type descs struct {
	diagnostic, hover, result *rt.TypeDesc
}

// Table binds the five std/compiler hosts for a program whose imports resolve
// against root and virtualFiles. Its type is vm.HostTable's.
func Table(root string, virtualFiles map[string]string) func(*hostadapt.Env) (map[string]hostadapt.Func, error) {
	return func(env *hostadapt.Env) (map[string]hostadapt.Func, error) {
		var d descs
		var err error
		if d.diagnostic, err = env.Desc(diagnosticSpec); err != nil {
			return nil, err
		}
		if d.hover, err = env.Desc(hoverSpec); err != nil {
			return nil, err
		}
		if d.result, err = env.Desc(resultSpec); err != nil {
			return nil, err
		}
		return map[string]hostadapt.Func{
			"compiler.check": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
				source, err := stringArg("compiler.check", args)
				if err != nil {
					return nil, err
				}
				return d.diagnostics(stdcompiler.Diagnostics(source, root, virtualFiles)), nil
			},
			"compiler.check_project": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
				if len(args) != 1 {
					return nil, fmt.Errorf("compiler.check_project expects 1 argument, got %d", len(args))
				}
				spec, err := projectSpec(args[0])
				if err != nil {
					return nil, err
				}
				diags, err := stdcompiler.ProjectSpecDiagnostics(spec, root)
				if err != nil {
					// A host-argument fault, reported as the text alone, with
					// no position.
					rt.Trap(err.Error())
				}
				return d.diagnostics(diags), nil
			},
			"compiler.hover": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
				source, err := stringArg("compiler.hover", args)
				if err != nil {
					return nil, err
				}
				h, err := stdcompiler.HoverContent(source, root, virtualFiles)
				if err != nil {
					return d.result.MakeVariant(1, err.Error()), nil
				}
				return d.result.MakeVariant(0, d.hover.Make(h.Signature, h.Markdown)), nil
			},
			"compiler.run": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
				source, err := stringArg("compiler.run", args)
				if err != nil {
					return nil, err
				}
				return d.runResult(stdcompilerrun.RunSource(source, root, virtualFiles, nil))
			},
			"compiler.run_file": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
				if len(args) != 1 {
					return nil, fmt.Errorf("compiler.run_file expects 1 argument, got %d", len(args))
				}
				entry, hostEnv, err := runFileSpec(args[0])
				if err != nil {
					return nil, err
				}
				return d.runResult(stdcompilerrun.RunFileSource(entry, root, virtualFiles, hostEnv))
			},
		}, nil
	}
}

// runResult converts a run's answer. A nested program the engine could not
// run is the machine's limit, not the program's `Err`, so the calling case
// reports blocked rather than failing.
func (d descs) runResult(out string, err error) (rt.Value, error) {
	if u, unrunnable := err.(*stdcompilerrun.Unrunnable); unrunnable {
		return nil, u
	}
	if err != nil {
		return d.result.MakeVariant(1, err.Error()), nil
	}
	return d.result.MakeVariant(0, out), nil
}

func (d descs) diagnostics(diags []rt.Diagnostic) *rt.List[any] {
	var out *rt.List[any]
	for i := len(diags) - 1; i >= 0; i-- {
		out = rt.Cons[any](d.diagnostic.Make(diags[i].Line, diags[i].Col, diags[i].Message), out)
	}
	return out
}

func stringArg(caller string, args []rt.Value) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("%s expects 1 argument, got %d", caller, len(args))
	}
	s, ok := args[0].(string)
	if !ok {
		return "", fmt.Errorf("%s expects String, got %T", caller, args[0])
	}
	return s, nil
}

// field is a struct record's field by name.
func field(v rt.Value, typeName, name string) (rt.Value, error) {
	r, ok := v.(*rt.Record)
	if !ok || r == nil || r.Desc.Kind != rt.KindStruct || rt.ShortTypeName(r.Desc.Name) != typeName {
		return nil, fmt.Errorf("expects compiler.%s, got %s", typeName, describe(v))
	}
	f, ok := r.FieldNamed(name)
	if !ok {
		return nil, fmt.Errorf("compiler.%s has no field %s", typeName, name)
	}
	return f, nil
}

func describe(v rt.Value) string {
	if r, ok := v.(*rt.Record); ok && r != nil {
		return r.Desc.Name
	}
	return fmt.Sprintf("%T", v)
}

// projectSpec reads a `compiler.Project` into the shared spec.
func projectSpec(v rt.Value) (stdcompiler.ProjectSpec, error) {
	raw, err := field(v, "Project", "entry_point")
	if err != nil {
		return stdcompiler.ProjectSpec{}, fmt.Errorf("compiler.check_project %w", err)
	}
	entry, ok := raw.(string)
	if !ok {
		return stdcompiler.ProjectSpec{}, fmt.Errorf("compiler.Project.entry_point must be String")
	}
	rawFiles, _ := field(v, "Project", "files")
	files, err := stringMap("compiler.Project.files", rawFiles)
	if err != nil {
		return stdcompiler.ProjectSpec{}, err
	}
	spec := stdcompiler.ProjectSpec{EntryPoint: entry, Files: files}
	if m, err := field(v, "Project", "manifest"); err == nil && m != nil {
		maybe, ok := m.(*rt.Record)
		if !ok || maybe.Desc.Kind != rt.KindEnum || rt.ShortTypeName(maybe.Desc.Name) != "Maybe" {
			return stdcompiler.ProjectSpec{}, fmt.Errorf("compiler.Project.manifest must be Maybe<Toml>")
		}
		if maybe.Variant().Name == "Some" {
			toml, ok := maybe.Field(0).(*rt.Record)
			if !ok || toml.Desc.Kind != rt.KindDistinct || rt.ShortTypeName(toml.Desc.Name) != "Toml" || toml.NumFields() != 1 {
				return stdcompiler.ProjectSpec{}, fmt.Errorf("compiler.Project.manifest must be Maybe<Toml>, got Some(%s)", describe(maybe.Field(0)))
			}
			text, ok := toml.Field(0).(string)
			if !ok {
				return stdcompiler.ProjectSpec{}, fmt.Errorf("compiler.Project.manifest expected Toml to wrap String, got %T", toml.Field(0))
			}
			spec.Manifest = &text
		}
	}
	return spec, nil
}

// runFileSpec reads a `compiler.RunFile` into its entry point and environment
// overrides.
func runFileSpec(v rt.Value) (string, map[string]string, error) {
	raw, err := field(v, "RunFile", "entry_point")
	if err != nil {
		return "", nil, fmt.Errorf("compiler.run_file %w", err)
	}
	entry, ok := raw.(string)
	if !ok {
		return "", nil, fmt.Errorf("compiler.RunFile.entry_point must be String")
	}
	rawEnv, _ := field(v, "RunFile", "env")
	env, err := stringMap("compiler.RunFile.env", rawEnv)
	if err != nil {
		return "", nil, err
	}
	return entry, env, nil
}

func stringMap(name string, v rt.Value) (map[string]string, error) {
	if v == nil {
		return map[string]string{}, nil
	}
	m, ok := v.(rt.Map[any, any])
	if !ok {
		return nil, fmt.Errorf("%s must be Map<String, String>, got %T", name, v)
	}
	out := make(map[string]string, rt.MapSize(m))
	for _, e := range rt.MapEntries(m) {
		key, ok := e.Key.(string)
		if !ok {
			return nil, fmt.Errorf("%s expects String keys, got %T", name, e.Key)
		}
		val, ok := e.Val.(string)
		if !ok {
			return nil, fmt.Errorf("%s expects String values, got %T", name, e.Val)
		}
		out[key] = val
	}
	return out, nil
}
