package irbuild

import "testing"

// TestDebugHostHandle_RendersThroughTheTypesImpl pins Debug of a value the VM
// holds as an rt.HostHandle. A Regex renders through std's `impl Debug for
// Regex` (its typed literal) at the top, in a struct field, in a payload, a
// list and a tuple. Every `Debug.inspect` row here was BLOCKED
// with "Debug on an unrepresented receiver (rt.HostHandle)" before the std
// host type's symbol was named by its declaring module (`regex.Regex`, the
// handle's TypeName) and the VM's Debug hook dispatched on a host handle.
func TestDebugHostHandle_RendersThroughTheTypesImpl(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	want := "dbg line 14: re = Regex`\\d+`\n" +
		"Regex`\\d+`\n" +
		"Holder{name: \"digits\", re: Regex`\\d+`}\n" +
		"dbg line 17: (Holder{name: \"digits\", re}) = Holder{name: \"digits\", re: Regex`\\d+`}\n" +
		"Some(Regex`\\d+`)\n" +
		"[Regex`\\d+`]\n" +
		"(Regex`\\d+`, 1)\n" +
		"Ok(Regex\"a`b\")\n"
	got := vmReference(fixture("debug_host_handle.nomi"))
	if got.stdout != want || got.exit != 0 {
		t.Errorf("VM transcript drifted (exit %d, stderr %q):\n--- got ---\n%s\n--- want ---\n%s",
			got.exit, got.stderr, got.stdout, want)
	}
}
