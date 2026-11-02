package stdlibadapters

import (
	"context"
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/rt"
)

// TestRtFuncs_ProcessesAndTheFake holds std/process_host's rows to what
// they do through the generated adapters: on a frame that carries a fake,
// `start` answers from the fake's first matching answer and records the
// call, its streams read the answer and record the input, `wait` answers
// the code, `captured` the captured output, and a refused or unanswered
// start fails; on the operating system's frame a program nothing names is
// NotFound. internal/stdprocess's tests run real programs.
func TestRtFuncs_ProcessesAndTheFake(t *testing.T) {
	table, err := Bind(&hostadapt.Env{})
	if err != nil {
		t.Fatal(err)
	}
	call := func(fr *rt.Frame, key string, args ...any) any {
		t.Helper()
		v, err := table[key](fr, args)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		return v
	}
	ok := func(fr *rt.Frame, key string, args ...any) any {
		t.Helper()
		v := call(fr, key, args...)
		r, isRec := v.(*rt.Record)
		if !isRec || r.Variant().Name != "Ok" {
			t.Fatalf("%s answered %s, want an Ok", key, canonText(v, false))
		}
		return r.Field(0)
	}
	text := func(v any) string { return canonText(v, false) }
	noEnv := rt.MapOf(rt.Hash, rt.Equal, []rt.MapEntry[any, any]{})
	empty := rt.ReaderFromText("")

	host := rt.NewFrame(context.Background())
	rb := newRecordBuilder()
	fake := call(host, "process_host.new_fake").(rt.ProcessFake)
	call(host, "process_host.fake_answer", fake, "sort", rtList(), true, int64(0), rt.Bytes("a\nb\n"), rt.Bytes("note\n"))
	call(host, "process_host.fake_answer", fake, "make", rtList("all"), false, int64(2), rt.Bytes(""), rt.Bytes("no rule\n"))
	call(host, "process_host.fake_refuse", fake, "deploy", rtList(), true,
		rb.FieldVariant("io.Error", "PermissionDenied", "path", "deploy"))
	faked := rt.EnterProcesses(host, fake)

	env := rt.MapOf(rt.Hash, rt.Equal, []rt.MapEntry[any, any]{{Key: "LC_ALL", Val: "C"}})
	sorter := ok(faked, "process_host.start", "sort", rtList("-r"), env, false, "logs", int64(3), empty, int64(3), int64(3))
	ok(faked, "io.Writer.write_text", call(faked, "process_host.child_stdin", sorter), "b\na\n")
	if got := text(ok(faked, "process_host.wait", sorter)); got != `[Int(0), Int(0)]` {
		t.Errorf("wait answered %s", got)
	}
	if got := text(ok(faked, "io.Reader.read_all_text", call(faked, "process_host.child_stdout", sorter))); got != `String("a\nb\n")` {
		t.Errorf("the piped stdout read %s", got)
	}
	if got := text(ok(faked, "io.Reader.read_all_text", call(faked, "process_host.child_stderr", sorter))); got != `String("note\n")` {
		t.Errorf("the piped stderr read %s", got)
	}
	call(faked, "process_host.kill", sorter)

	maker := ok(faked, "process_host.start", "make", rtList("all"), noEnv, false, "", int64(0), empty, int64(2), int64(2))
	if got := text(ok(faked, "process_host.wait", maker)); got != `[Int(0), Int(2)]` {
		t.Errorf("wait answered %s", got)
	}
	if got := text(call(faked, "process_host.captured", maker, int64(2))); got != `Bytes("no rule\n")` {
		t.Errorf("captured stderr is %s", got)
	}
	if got := text(call(faked, "process_host.start", "deploy", rtList(), noEnv, false, "", int64(0), empty, int64(2), int64(2))); got != `results.Result/4.Err{0=io.Error/4.PermissionDenied{path=String("deploy")}}` {
		t.Errorf("a refused start answered %s", got)
	}
	if got := text(call(faked, "process_host.start", "make", rtList("clean"), noEnv, false, "", int64(0), empty, int64(2), int64(2))); got != `results.Result/4.Err{0=io.Error/4.NotFound{path=String("make")}}` {
		t.Errorf("an unanswered start answered %s", got)
	}

	if got := call(host, "process_host.fake_calls", fake).(int64); got != 4 {
		t.Errorf("fake_calls is %d, want 4", got)
	}
	if got := text(call(host, "process_host.fake_command", fake, int64(0))); got != `[String("sort"), String("-r")]` {
		t.Errorf("fake_command is %s", got)
	}
	if got := call(host, "process_host.fake_dir", fake, int64(0)).(string); got != "logs" {
		t.Errorf("fake_dir is %q", got)
	}
	if got := text(call(host, "process_host.fake_env", fake, int64(0))); got != `{String("LC_ALL")=>String("C")}` {
		t.Errorf("fake_env is %s", got)
	}
	if got := text(call(host, "process_host.fake_input", fake, int64(0))); got != `Bytes("b\na\n")` {
		t.Errorf("fake_input is %s", got)
	}

	if got := text(call(host, "process_host.start", "nomi-no-such-program", rtList(), noEnv, false, "", int64(0), empty, int64(2), int64(2))); got != `results.Result/4.Err{0=io.Error/4.NotFound{path=String("nomi-no-such-program")}}` {
		t.Errorf("a missing program answered %s", got)
	}
}
