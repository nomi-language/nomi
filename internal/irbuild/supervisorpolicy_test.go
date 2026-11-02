package irbuild

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	rt "github.com/nomi-language/nomi/rt"
)

// TestBackoffDefaultsMatchTheStdDeclaration is the guard stdenum.go's
// `payloadStructFields` promises, and it is the ONLY thing standing between two
// encodings of one fact.
//
// `Backoff.Exponential`'s field defaults are written TWICE: in Nomi, in
// std/supervisors.nomi, where a caller meets them and where `pub fn new`'s
// declaration carries them; and in Go, in rt/supervisorpolicy.go, where the
// builder reads them to fill an omitted field of a literal. The second encoding
// exists because a std enum's *typeDef is process-wide and has no gen to lower
// Nomi source in — the reason payloadStructScalar rejects a defaulted std field
// outright, which this form does not weaken but does route around.
//
// WHAT WOULD HAPPEN WITHOUT THIS GUARD, which is why it is worth its weight:
// `Backoff.Exponential{}` is `Supervisor.new`'s own declared default, so EVERY
// call that omits `backoff:` goes through it — and a program would retry on the
// Go numbers while std's documentation states the Nomi ones. Nothing in the
// corpus observes a backoff delay (the three supervisor files never fail a
// supervised task on purpose), so every golden comparison would pass with the
// schedule wrong. That is the blind-to-a-DIMENSION hazard, and a fixture cannot
// close it.
//
// It reads the STD SOURCE rather than restating the numbers, so a std edit fails
// here rather than drifting. And it asserts the reading is NON-VACUOUS first: a
// regexp that matched nothing would make this pass against any rt constant at
// all, which is the "what would this have shown if the thing were false" failure
// in its purest form.
func TestBackoffDefaultsMatchTheStdDeclaration(t *testing.T) {
	src := readStdSupervisorsSource(t)

	// `max_restarts: Int = 10`
	restarts := regexp.MustCompile(`max_restarts:\s*Int\s*=\s*(\d+)`).FindStringSubmatch(src)
	if restarts == nil {
		t.Fatal("std/supervisors.nomi declares no `max_restarts: Int = <n>` default; either the " +
			"declaration moved and this reading is vacuous, or the default was dropped and " +
			"stdenum.go's row now fills a field the front end requires")
	}
	if want := "10"; restarts[1] != want {
		t.Errorf("std declares max_restarts = %s and rt.BackoffDefaultMaxRestarts is %d",
			restarts[1], rt.BackoffDefaultMaxRestarts)
	}
	if rt.BackoffDefaultMaxRestarts != 10 {
		t.Errorf("rt.BackoffDefaultMaxRestarts is %d, and std declares %s",
			rt.BackoffDefaultMaxRestarts, restarts[1])
	}

	// `max_elapsed: Duration = Duration.minutes(15)`
	elapsed := regexp.MustCompile(`max_elapsed:\s*Duration\s*=\s*Duration\.minutes\((\d+)\)`).FindStringSubmatch(src)
	if elapsed == nil {
		t.Fatal("std/supervisors.nomi declares no `max_elapsed: Duration = Duration.minutes(<n>)` " +
			"default; the reading is vacuous, so this test would pass against any rt constant")
	}
	if elapsed[1] != "15" {
		t.Errorf("std declares max_elapsed = Duration.minutes(%s)", elapsed[1])
	}
	if want := rt.Duration(15 * time.Minute); rt.BackoffDefaultMaxElapsed != want {
		t.Errorf("rt.BackoffDefaultMaxElapsed is %d ns and std declares Duration.minutes(%s) = %d ns",
			rt.BackoffDefaultMaxElapsed, elapsed[1], want)
	}

	// `shutdown_timeout: Duration = Duration.seconds(5)` is the THIRD default of
	// the same kind, and it is checked here rather than beside the other two
	// because it belongs to `Supervisor.new` rather than to `Backoff`. Same
	// hazard exactly: the declaration owns it (the new/new_exact split), but rt
	// still carries the constant for a Go embedder, and a drift would make an
	// embedder drain on a different budget from a Nomi program.
	shutdown := regexp.MustCompile(`shutdown_timeout:\s*Duration\s*=\s*Duration\.seconds\((\d+)\)`).FindStringSubmatch(src)
	if shutdown == nil {
		t.Fatal("std/supervisors.nomi declares no `shutdown_timeout: Duration = Duration.seconds(<n>)`")
	}
	if want := rt.Duration(5 * time.Second); rt.DefaultShutdownTimeout != want || shutdown[1] != "5" {
		t.Errorf("std declares Duration.seconds(%s) and rt.DefaultShutdownTimeout is %d ns",
			shutdown[1], rt.DefaultShutdownTimeout)
	}
}

// readStdSupervisorsSource locates std/supervisors.nomi from this package's own
// directory, so the reading does not depend on the test's working directory.
func readStdSupervisorsSource(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot locate the std source: %v", err)
	}
	// internal/irbuild -> repository root -> std/supervisors.nomi
	path := filepath.Join(wd, "..", "..", "std", "supervisors.nomi")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	return string(src)
}
