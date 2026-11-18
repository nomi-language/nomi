package ast

import (
	"fmt"
	"reflect"
	"testing"
)

// FieldChildren reports, for every node type with every field filled, the
// children Children reports, in the same order, each under a field
// NodeChildFields lists for that type.
func TestFieldChildrenAreChildren(t *testing.T) {
	listed := map[string]bool{}
	NodeChildFields(func(node, field string) { listed[node+"."+field] = true })
	if len(listed) == 0 {
		t.Fatal("NodeChildFields lists nothing")
	}
	f := &filler{}
	for _, proto := range generatedTypes {
		typ := reflect.TypeOf(proto)
		if typ.Kind() != reflect.Pointer || !typ.Implements(nodeType) {
			continue
		}
		root := reflect.New(typ.Elem())
		f.fill(root.Elem(), 0)
		n := root.Interface().(Node)
		var plain, byField []Node
		Children(n, func(c Node) { plain = append(plain, c) })
		FieldChildren(n, func(field string, c Node) {
			byField = append(byField, c)
			if key := typ.Elem().Name() + "." + field; !listed[key] {
				t.Errorf("FieldChildren reports a child under %s, which NodeChildFields does not list", key)
			}
		})
		if !sameChildren(plain, byField) {
			t.Errorf("the children of a %T differ:\nChildren      %v\nFieldChildren %v", n, names(plain), names(byField))
		}
	}
	if testing.Verbose() {
		NodeChildFields(func(node, field string) { fmt.Println(node + "." + field) })
	}
}
