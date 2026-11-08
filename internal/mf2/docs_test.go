package mf2

import (
	"sort"
	"testing"
)

// The hover docs describe exactly the functions Validate formats or
// refuses as not supported yet, and each function's options exactly as
// Validate allows them.
func TestFunctionDocsMatchValidate(t *testing.T) {
	documented := map[string]bool{}
	for _, f := range FunctionDocs() {
		documented[f.Name] = true
		if f.Later {
			if _, ok := laterFunctions[f.Name]; !ok {
				t.Errorf("`:%s` is documented as not supported yet, and Validate does not say so", f.Name)
			}
			continue
		}
		if !formatFunctions[f.Name] {
			t.Errorf("`:%s` is documented, and Validate does not know it", f.Name)
			continue
		}
		var got, want []string
		for _, o := range f.Options {
			got = append(got, o.Name)
		}
		for o := range allowedOptions(f.Name) {
			want = append(want, o)
		}
		sort.Strings(got)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Errorf("`:%s` documents the options %v, and Validate allows %v", f.Name, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("`:%s` documents the options %v, and Validate allows %v", f.Name, got, want)
				break
			}
		}
	}
	for name := range formatFunctions {
		if !documented[name] {
			t.Errorf("`:%s` has no doc", name)
		}
	}
	for name := range laterFunctions {
		if !documented[name] {
			t.Errorf("`:%s` has no doc", name)
		}
	}
}
