package analysis

import (
	"reflect"
	"testing"
)

// TestFileBodiesCloneCopiesEveryTable holds clone to FileBodies' fields: a
// table it leaves shared is one CheckAddedTest's walk writes into the
// shared stdlib's analysis of the module.
func TestFileBodiesCloneCopiesEveryTable(t *testing.T) {
	var fb FileBodies
	v := reflect.ValueOf(&fb).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Map:
			reflect.NewAt(f.Type(), f.Addr().UnsafePointer()).Elem().Set(reflect.MakeMap(f.Type()))
		case reflect.Slice:
			reflect.NewAt(f.Type(), f.Addr().UnsafePointer()).Elem().Set(reflect.MakeSlice(f.Type(), 1, 1))
		}
	}
	out := fb.clone()
	o := reflect.ValueOf(&out).Elem()
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		switch v.Field(i).Kind() {
		case reflect.Map, reflect.Slice:
			if v.Field(i).UnsafePointer() == o.Field(i).UnsafePointer() {
				t.Errorf("FileBodies.clone shares %s with the original", name)
			}
		}
	}
}
