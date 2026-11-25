package vm

import (
	"testing"

	"github.com/nomi-language/nomi/hostadapt"
)

// Each kind of unretained declaration has one line a blocked run reports,
// which vmhost and vmrunner both read from Reason. A project host function
// is named by its key, as its host table lists it.
func TestUnretained_Reason(t *testing.T) {
	cases := []struct {
		u    Unretained
		want string
	}{
		{Unretained{Kind: NotRetained, Name: "main"}, "[main] not retained"},
		{Unretained{Kind: OnceNotRetained, Name: "config.table"}, "[config.table] once initializer not retained"},
		{Unretained{Kind: NoBinding, Name: "strings.trim"},
			"[strings.trim] is a host function, and the VM has no implementation for it"},
		{Unretained{Kind: NoBinding, Name: hostadapt.ProjectName("lookup")},
			"[lookup] is a host function, and the VM has no implementation for it"},
		{Unretained{Kind: NoImplementation, Name: "Shape.area"},
			"[Shape.area] dispatched, and no implementation was retained"},
	}
	for _, c := range cases {
		if got := c.u.Reason(); got != c.want {
			t.Errorf("%v: %q, want %q", c.u.Kind, got, c.want)
		}
	}
}
