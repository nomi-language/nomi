package vm

import (
	"github.com/nomi-language/nomi/internal/ir"
	"strings"
	"testing"
)

func TestIterTerminal_RejectsWrongSource(t *testing.T) {
	pos := ir.At("count.nomi", 1, 1)
	for _, tc := range []struct {
		op   ir.IterOp
		over ir.IterDomain
		want string
	}{
		{ir.IterView, ir.IterOverString, "not a string"},
		{ir.IterCount, ir.IterOverList, "not a list"},
		{ir.IterCount, ir.IterOverSeq, "not a sequence"},
		{ir.IterEmpty, ir.IterOverSeq, "not a sequence"},
		{ir.IterNotEmpty, ir.IterOverSeq, "not a sequence"},
	} {
		f := ir.NewFunc(pos, "terminal")
		src, dst := f.NewTemp(), f.NewTemp()
		n := ir.NewIter(pos, dst, tc.op, tc.over, src)
		fr := testFrame(f, nil)
		fr.write(src, int64(1))
		if err := new(Machine).iterInstr(fr, n); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v", tc.op, err)
		}
	}
}
