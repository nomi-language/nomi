package irbuild

import (
	"testing"
)

// TestStdDynamicIsLowerable is the SIGNATURE-side claim, reached without
// building anything.
//
// If the `stdHostSpecs` row for `pub host type Dynamic` is removed or its
// identity check stops matching, every one of the thirteen refuses `stdlib
// function outside the scalar subset` and this test names the declaration,
// where a program-level failure would say only "refused".
//
// Asserted as a SET of names rather than as a total, for the reason
// TestStdlibIndexKeysCarryTheirReceiver gives about totals: a number needs
// editing every time std grows, so it becomes churn and churn gets bumped
// reflexively, and a total cannot say WHICH declaration was lost.
func TestStdDynamicIsLowerable(t *testing.T) {
	idx := stdlibLowering()
	// The eleven inherent `host fn`s, the Debug requirement, and json's bridge.
	//
	// `dynamic.Dynamic.inspect` carries NO interface segment, and that is read
	// off the index rather than derived: `Debug` is not generic and `stdKey` only
	// spells an instantiation, so `dynamic.Dynamic.Debug.inspect` would be a
	// plausible-looking key that resolves to nothing. Spelling a key from a
	// reading of stdKey is a guess.
	bound := []string{
		"dynamic.Dynamic.field",
		"dynamic.Dynamic.index",
		"dynamic.Dynamic.path",
		"dynamic.Dynamic.as_string",
		"dynamic.Dynamic.as_int",
		"dynamic.Dynamic.as_float",
		"dynamic.Dynamic.as_bool",
		"dynamic.Dynamic.as_list",
		"dynamic.Dynamic.as_dict",
		"dynamic.Dynamic.null?",
		"dynamic.Dynamic.has?",
		"dynamic.Dynamic.inspect",
		"json.Json.to_dynamic",
	}
	for _, key := range bound {
		f, declared := idx.byKey[key]
		if !declared {
			t.Errorf("std declares no %q; the key is wrong, not the binding", key)
			continue
		}
		if f.why != "" {
			t.Errorf("%s refuses %q; the stdHostSpecs row for `Dynamic` is what admits "+
				"its signature", key, f.why)
			continue
		}
		if f.rtCall == "" {
			t.Errorf("%s is admitted but bound to nothing", key)
		}
	}

	// AND THE THREE THAT MUST STILL REFUSE, with the reason, because a row that
	// admitted them would be over-reaching rather than progress. `as_maybe`,
	// `at_field` and `at_index` are generic Nomi bodies over a CALLBACK, so their
	// obstacle is the function-value gap and not this type.
	//
	// Checked as a NEGATIVE half rather than assumed: this is the only place that
	// states the module's extern/non-extern split is exactly the lowerable split,
	// and if a future callback mechanism admits them these rows should go red so
	// somebody moves them up rather than leaving a stale claim here.
	for _, key := range []string{
		"dynamic.Dynamic.as_maybe",
		"dynamic.Dynamic.at_field",
		"dynamic.Dynamic.at_index",
	} {
		f, declared := idx.byKey[key]
		if !declared {
			t.Errorf("std declares no %q", key)
			continue
		}
		if f.why != "stdlib generic function" {
			t.Errorf("%s refuses %q, want \"stdlib generic function\" — it is a generic "+
				"Nomi body over a callback, so the function-value gap is its obstacle "+
				"and a different reason means something moved", key, f.why)
		}
	}
}
