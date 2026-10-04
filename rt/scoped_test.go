package rt

import "testing"

func TestScopedFieldSnapshotsAreIndependent(t *testing.T) {
	root := &Frame{}
	PublishScoped(root, map[string]any{"label": "root", "port": int64(3000)})
	child := EnterScopedField(root, "label", "child")
	sibling := EnterScopedField(root, "port", int64(4000))
	if ScopedField[string](root, "label") != "root" || ScopedField[string](child, "label") != "child" || ScopedField[string](sibling, "label") != "root" {
		t.Fatal("field override leaked between lineages")
	}
	PublishScoped(root, map[string]any{"label": "new"})
	if ScopedField[string](child, "label") != "child" || ScopedField[int64](child, "port") != 3000 {
		t.Fatal("publication changed a captured snapshot")
	}
}
