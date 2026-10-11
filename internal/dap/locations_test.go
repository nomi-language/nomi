package dap

import "testing"

// Go to a variable's binding: each local carries a declaration location,
// which the `locations` request resolves to the name where it is bound: a
// parameter, a destructured name, a binding and the rebinding that shadows
// it, and a loop's state. A pipeline stage's is where the stage is written.
func TestLocations_GoToAVariablesBinding(t *testing.T) {
	h := newHarness(t)
	h.start(false, runConfig("bindings.nomi"))
	h.breakAt("bindings.nomi", 9)
	h.configure()
	thread := h.stopped()
	frames := h.stack(thread)
	for _, ref := range h.declarations(frames[0]) {
		h.request("locations", map[string]any{"locationReference": ref})
	}
	h.request("locations", map[string]any{"locationReference": 999})
	h.breakAt("bindings.nomi")
	h.do("continue", thread)
	h.wait("terminated")
	h.disconnect()

	h2 := newHarness(t)
	h2.start(false, runConfig("pipeline.nomi"))
	h2.breakAt("pipeline.nomi", 17)
	h2.configure()
	thread = h2.stopped()
	frames = h2.stack(thread)
	body := h2.request("scopes", map[string]any{"frameId": frames[0]})
	pipeline := body["scopes"].([]any)[1].(map[string]any)
	vars := h2.request("variables", map[string]any{"variablesReference": pipeline["variablesReference"]})
	for _, v := range vars["variables"].([]any) {
		if ref, has := v.(map[string]any)["declarationLocationReference"]; has {
			h2.request("locations", map[string]any{"locationReference": ref})
		}
	}
	h2.disconnect()
	h.log.WriteString("-------------------- a pipeline's stages\n" + h2.log.String())
	h.check("locations")
}

// declarations requests a frame's locals and answers each one's
// declaration location reference, in order.
func (h *harness) declarations(frame int) []int {
	h.t.Helper()
	body := h.request("scopes", map[string]any{"frameId": frame})
	ref := body["scopes"].([]any)[0].(map[string]any)["variablesReference"]
	vars := h.request("variables", map[string]any{"variablesReference": ref})
	var out []int
	for _, v := range vars["variables"].([]any) {
		if r, has := v.(map[string]any)["declarationLocationReference"]; has {
			out = append(out, int(r.(float64)))
		} else {
			h.t.Errorf("local %v has no declaration location", v)
		}
	}
	return out
}
