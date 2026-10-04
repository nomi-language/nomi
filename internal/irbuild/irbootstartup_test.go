package irbuild

import (
	"strings"
	"testing"
)

// An entry boot takes no parameter or one Startup, and the Startup parameter
// may have a default. A program's boot with no parameter is called with none;
// a group's `boot` line calls it as an ordinary call, so `boot server.boot()`
// either calls a boot with no parameter or fills the parameter from its
// default.

// A program whose boot takes no parameter boots and publishes its app.
func TestIRBootStartup_AZeroParameterBootRunsMain(t *testing.T) {
	verifyDeferProgram(t, `import std/io

struct App {
  port: Int
}

fn boot(): App {
  App{port: 8080}
}

fn main() {
  io.print("listening on :${App.port}")
}
`, "listening on :8080\n", "boot", "main")
}

// The entry file a group's `boot` line calls: a boot with no parameter.
const irBootStartupZeroServer = `import std/io

pub struct App {
  label: String
}

pub fn boot(): App {
  io.print("boot")
  App{label: "zero"}
}

fn main() {}
`

// A group calls a boot that takes no parameter with no argument; the case
// records no startup function and the runner calls the boot with none.
func TestIRBootStartup_AGroupBootsAZeroParameterBoot(t *testing.T) {
	run := irTestGroupVMFiles(t, `import {
  server.{self, App}
}

tests "group" {
  boot server.boot()

  test "reads the app" {
    assert App.label == "zero"
  }
}
`, map[string]string{"server.nomi": irBootStartupZeroServer}, 1)
	group := run.module.Tests()[0].Group()
	if group.Boot == nil || group.Startup != nil {
		t.Fatalf("the case names boot %v and startup %v; want the boot and no startup", group.Boot, group.Startup)
	}
	if run.vm.exit != 0 || !strings.Contains(run.vm.stdout, "boot\n") || !strings.Contains(run.vm.stdout, "1 passed") {
		t.Errorf("want the boot to run and the case to pass:\n%s", run.vm.stdout)
	}
}

// The entry file a group's `boot` line calls: a boot whose Startup parameter
// has a default, which prints the arguments it received. The default calls a
// function private to the entry file, which the test file cannot name, so
// the entry's unit must lower it.
const irBootStartupDefaultServer = `import std/io

pub struct App {
  first: String
}

fn default_startup(): Startup {
  Startup{args: ["default"]}
}

pub fn boot(startup: Startup = default_startup()): App {
  io.print("boot ${startup.args}")
  App{first: Iter.first(startup.args) |> Maybe.with_default("none")}
}

fn main() {}
`

// A group that writes `boot server.boot()` boots with the parameter's
// default; one that passes a Startup boots with it.
func TestIRBootStartup_AGroupReliesOnTheBootsDefault(t *testing.T) {
	run := irTestGroupVMFiles(t, `import {
  server.{self, App}
}

tests "defaulted" {
  boot server.boot()

  test "reads the default" {
    assert App.first == "default"
  }
}

tests "given" {
  boot server.boot(Startup{args: ["given"]})

  test "reads the argument" {
    assert App.first == "given"
  }
}
`, map[string]string{"server.nomi": irBootStartupDefaultServer}, 2)
	for _, c := range run.module.Tests() {
		if c.Group().Boot == nil || c.Group().Startup == nil {
			t.Errorf("%s names boot %v and startup %v; want both", c.Name(), c.Group().Boot, c.Group().Startup)
		}
	}
	for _, line := range []string{"boot [default]\n", "boot [given]\n", "2 passed"} {
		if !strings.Contains(run.vm.stdout, line) {
			t.Errorf("the VM report lacks %q:\n%s", line, run.vm.stdout)
		}
	}
}
