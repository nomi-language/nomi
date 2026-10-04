package vmhost_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/vmhost"

	"github.com/nomi-language/nomi/rt"
)

const embedProgram = `import std/io

host fn shout(s: String): String

pub struct Order {
  weight: Float
  distance: Int
}

pub fn cost(o: Order, rate: Float): Float {
  o.weight * rate + Int.to_float(o.distance)
}

pub fn greet(name: String): String {
  io.print("greeting " + name)
  shout(name)
}

pub fn check(n: Int): Result<Int, String> {
  if n > 0 { Ok(n) } else { Err("not positive") }
}
`

func shoutTable(*hostadapt.Env) (map[string]hostadapt.Func, error) {
	return map[string]hostadapt.Func{
		"shout": func(_ *rt.Frame, args []rt.Value) (rt.Value, error) {
			return strings.ToUpper(args[0].(string)) + "!", nil
		},
	}, nil
}

func TestCall_ConvertsArgumentsAndAnswersVMValues(t *testing.T) {
	var out bytes.Buffer
	p, err := vmhost.LoadSource("main", embedProgram, vmhost.WithHosts(shoutTable), vmhost.WithOutput(&out))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	got, err := p.Call(ctx, "cost", vmhost.Fields{"weight": 2.5, "distance": 10}, float32(4))
	if err != nil {
		t.Fatal(err)
	}
	if got != 20.0 {
		t.Errorf("cost answered %v, want 20", got)
	}

	got, err = p.Call(ctx, "greet", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if got != "ADA!" {
		t.Errorf("greet answered %v; the host table's shout did not run", got)
	}
	if out.String() != "greeting ada\n" {
		t.Errorf("WithOutput received %q", out.String())
	}

	got, err = p.Call(ctx, "check", uint8(0))
	if err != nil {
		t.Fatal(err)
	}
	r, ok := got.(*rt.Record)
	if !ok || r.Variant().Name != "Err" || r.Field(0) != "not positive" {
		t.Errorf("check answered %#v, want Err(\"not positive\") as a value", got)
	}
}

func TestCall_RefusesArgumentsTheParameterCannotHold(t *testing.T) {
	p, err := vmhost.LoadSource("main", embedProgram, vmhost.WithHosts(shoutTable))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		args []any
		want string
	}{
		{"check", []any{"one"}, "want an Int"},
		{"check", []any{uint64(1) << 63}, "does not fit an Int"},
		{"cost", []any{vmhost.Fields{"weight": 1.0}, 1.0}, "missing field distance"},
		{"cost", []any{vmhost.Fields{"weight": 1.0, "distance": 1, "colour": "red"}, 1.0}, "colour"},
		{"check", []any{1, 2}, "takes 1 argument"},
		{"nowhere", nil, "declares no function nowhere"},
	} {
		if _, err := p.Call(ctx, tc.name, tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Call(%s, %v): error %v, want one containing %q", tc.name, tc.args, err, tc.want)
		}
	}
}

func TestLoad_AHostFnNoTableAnswersIsAFrontEndError(t *testing.T) {
	_, err := vmhost.LoadSource("main", embedProgram)
	if err == nil {
		t.Fatal("a program whose `host fn shout` nothing answers loaded")
	}
	for _, want := range []string{"1 extern declared but not registered", "shout\t(entry:3:9)", "vmhost.WithHosts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("load error %q lacks %q", err, want)
		}
	}
}

func TestCall_AnUnretainedFunctionIsBlocked(t *testing.T) {
	p, err := vmhost.Load(writeProgram(t, blockedProgram))
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Call(context.Background(), "count", 3, 0)
	var b *vmhost.Blocked
	if !errors.As(err, &b) || !strings.Contains(b.Error(), "count") {
		t.Fatalf("Call of an unretained function answered %v, want *Blocked naming count", err)
	}
}

func TestCheck_RunsTheFrontEndOnly(t *testing.T) {
	dir := t.TempDir()
	clean := filepath.Join(dir, "clean.nomi")
	if err := os.WriteFile(clean, []byte("import std/io\n\nfn main() {\n  io.print(\"ran\")\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := vmhost.Check(clean); err != nil {
		t.Errorf("Check(clean) = %v", err)
	}
	bad := filepath.Join(dir, "bad.nomi")
	if err := os.WriteFile(bad, []byte("fn main(): Int {\n  \"no\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var ds vmhost.Diagnostics
	if err := vmhost.Check(bad); !errors.As(err, &ds) || !strings.Contains(err.Error(), "bad.nomi:2:3: return type mismatch") {
		t.Errorf("Check(bad) = %v, want an analysis error", err)
	}
}
