package irbuild

import "testing"

// Std types reached only through a whole-module import's qualifier, in a file
// that declares its own `Task`, `Channel`, `Supervisor` and `Error`:
// `tasks.Task.spawn` and `tasks.Task<Int>`, `channels.Channel.buffered<T>`,
// `supervisors.Supervisor.new` with named and defaulted arguments in boot, an
// app field of type `supervisors.Supervisor`, `random.Error.OsEntropy{...}`
// built and matched, and `json.Json.Int(3)` and `json.Json.Null`. The
// intrinsic arms recognise std's type by the declaration the owner resolves
// to (stdIntrinsicOwner), so the local types keep their own calls.
func TestStdModuleQualified_TypesResolveByDeclaration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	const want = "" +
		"task mine\n" +
		"3\n" +
		"local\n" +
		"my error\n" +
		"42\n" +
		"Some(hi)\n" +
		"supervised\n" +
		"entropy: x\n" +
		"weight: 0.5\n" +
		"other\n" +
		"OS entropy unavailable: a\n" +
		"[3,null]\n"
	got := vmReference(fixture("std_module_qualified.nomi"))
	if got.stdout != want || got.exit != 0 || got.stderr != "" {
		t.Fatalf("std types named through their module's qualifier:\nwant stdout=%q\ngot  %s", want, got)
	}
}
