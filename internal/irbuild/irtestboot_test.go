package irbuild

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
)

// The entry boot a `tests` group's `boot` line names, built for the VM and
// started with BootTest.
//
// The VM leg runs the file function the case calls, in the app the group's
// boot publishes; the case's printed line is the comparison.
const irTestBootSource = `import std/io

pub struct App {
  context: Context
  label: String
}

pub fn boot(): App {
  App{context: Context.root(), label: "booted"}
}

fn main() {}

fn show(suffix: String): String {
  App.label + suffix
}

tests "group" {
  boot boot()

  test "reads the group's app" {
    io.print(show("!"))
    assert show("") == "booted"
  }
}
`

func TestIRTestBoot_AGroupBootPublishesTheAppItsCasesRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boot_test.nomi")
	if err := os.WriteFile(path, []byte(irTestBootSource), 0600); err != nil {
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
		if len(m.TestBoots()) > 0 {
			entry = m
		}
	}
	if entry == nil {
		t.Fatal("no module records the group's boot")
	}
	if len(entry.TestBoots()) != 1 || entry.FuncFor(entry.TestBoots()[0]) == nil {
		t.Fatalf("the group's boot is recorded %d time(s) and must be retained once", len(entry.TestBoots()))
	}
	if len(entry.Tests()) != 1 {
		t.Fatalf("%d case(s) retained; want the group's one", len(entry.Tests()))
	}
	group := entry.Tests()[0].Group()
	if group.Boot != entry.TestBoots()[0] || group.Startup != nil {
		t.Fatalf("the case's group names boot %v and startup %v; want the recorded boot and no startup, since the boot takes no parameter", group.Boot, group.Startup)
	}
	var out bytes.Buffer
	m := vm.NewProgram(entry, res.IRModules(), &out)
	// Unbooted, the read has nothing to find.
	if _, err := vmRunV(m, "show", "!"); err == nil || !strings.Contains(err.Error(), "is not published") {
		t.Fatalf("an unbooted read answered %v", err)
	}
	booted, err := m.BootTest(group)
	if err != nil {
		t.Fatal(err)
	}
	got, err := vmRunV(booted, "show", "!")
	if err != nil {
		t.Fatalf("VM: %v", err)
	}
	const line = "booted!\n"
	if s, isString := got.(string); !isString || s+"\n" != line {
		t.Fatalf("VM answered %v; want %q", got, line)
	}

	ref := goldenReference(t, path)
	if ref.exit != 0 || !strings.HasPrefix(ref.stdout, line) {
		t.Fatalf("golden output: %s; want its output to begin %q", ref, line)
	}
}

// BootTest starts only a boot the program records as a test-group boot.
func TestIRTestBoot_BootTestRefusesAnUnrecordedSymbol(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boot_test.nomi")
	if err := os.WriteFile(path, []byte(irTestBootSource), 0600); err != nil {
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
	for _, m := range res.IR {
		for _, f := range m.Funcs() {
			if f.Name() != "show" {
				continue
			}
			_, err := vm.NewProgram(m, res.IRModules(), &bytes.Buffer{}).BootTest(ir.TestGroup{Boot: f.Sym(), Startup: f.Sym()})
			if err == nil || !strings.Contains(err.Error(), "is not a test-group boot") {
				t.Fatalf("booting an ordinary function answered %v", err)
			}
			return
		}
	}
	t.Fatal("show was not retained")
}
