package stdlibadapters

import (
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/rt"
)

// partsTable binds the adapters' parts forms (hostadapt.PartsFunc) by
// binding name.
func partsTable(t *testing.T) map[string]hostadapt.PartsFunc {
	t.Helper()
	parts := map[string]hostadapt.PartsFunc{}
	env := &hostadapt.Env{AddParts: func(name string, fn hostadapt.PartsFunc) { parts[name] = fn }}
	if _, err := Bind(env); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	return parts
}

// sameParts holds a parts form's answer to the adapter's whole answer for
// the same operands: a failure for a failure, the payload of a Some or an Ok
// with true, and the None or the Err record with false. shapeOnly compares
// which of the three it was and not the values, for an answer that differs
// from call to call. It answers whether the binding has a parts form.
func sameParts(t *testing.T, key string, parts hostadapt.PartsFunc, whole hostOutcome, call func(hostadapt.PartsFunc) (rt.Value, bool, error), shapeOnly bool) bool {
	t.Helper()
	if parts == nil {
		return false
	}
	var v rt.Value
	var ok bool
	got := classify(func() (rt.Value, error) {
		var err error
		v, ok, err = call(parts)
		return v, err
	})
	if (whole.err != nil) != (got.err != nil) || whole.fault != got.fault || whole.panic != got.panic {
		t.Errorf("%s: the parts form fails with %v / %q / %q, the adapter with %v / %q / %q",
			key, got.err, got.fault, got.panic, whole.err, whole.fault, whole.panic)
		return true
	}
	if whole.err != nil {
		if whole.err.Error() != got.err.Error() {
			t.Errorf("%s: the parts form fails with %v, the adapter with %v", key, got.err, whole.err)
		}
		return true
	}
	if whole.fault != "" || whole.panic != "" {
		return true
	}
	rec, isRec := whole.val.(*rt.Record)
	if !isRec || rec.Desc.Kind != rt.KindEnum {
		t.Errorf("%s: the adapter answers %T, not a Maybe or a Result", key, whole.val)
		return true
	}
	variant := rec.Desc.Variants[rec.Tag].Name
	carries := variant == "Some" || variant == "Ok"
	if ok != carries {
		t.Errorf("%s: the adapter answers %s and the parts form says carried = %v", key, variant, ok)
		return true
	}
	if shapeOnly {
		return true
	}
	want := whole.val
	if carries {
		want = rec.Field(0)
	}
	if !rt.Equal(v, want) {
		t.Errorf("%s: the parts form answers %#v, the adapter %#v", key, v, want)
	}
	return true
}
