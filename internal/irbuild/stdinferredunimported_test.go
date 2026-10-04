package irbuild

import "testing"

// A lambda parameter the checker infers as a std enum or std opaque type runs
// on the VM in a file that never imports the type's name
// (testdata/std_inferred_unimported).
//
// projectStdEnum and projectStdOpaque identified the type through this file's
// anchors, which exist only for names the file's scope binds, so `|e|` over
// std/random's `Error` and `|d|` over a `Duration` refused as `an unsupported
// type` while the annotated `|e: Error|` lowered. They now identify it by the
// solved (Origin, Name), as stdStructOfType does for std records.
func TestProjectStd_InferredWithoutTheNameInScope(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "Err(OS entropy unavailable: refused)\n" +
		"Ok(1)\n" +
		"Err(invalid value: month out of range: 13)\n" +
		"[2s, 5ms]\n"
	got := vmReference(fixture("std_inferred_unimported/main.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}

// A monomorphic std type named through its module's qualifier in a signature
// runs on the VM (testdata/std_qualified_type.nomi): `random.Error` and
// `duration.Duration`. typeOf's QualifiedType arm asked only the program's own
// declarations, so a signature naming one refused its function.
func TestQualifiedStdType_InASignature(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "Err(OS entropy unavailable: refused)\n" +
		"Ok(1)\n" +
		"6s\n"
	got := vmReference(fixture("std_qualified_type.nomi"))
	if got.stdout != want || got.stderr != "" || got.exit != 0 {
		t.Fatalf("VM run (exit %d):\n--- stdout ---\n%s--- stderr ---\n%s--- want ---\n%s",
			got.exit, got.stdout, got.stderr, want)
	}
}
