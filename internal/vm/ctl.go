package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
)

// A call to an iter-sensitive function that is an iteration callback's
// whole body writes the callee's answer whole into a temporary of type Any:
// the value, or the *ctlValue its `break` or `continue` left with.
// ir.CtlRead reads the outcome and the value out of it. See
// internal/ir/ctl.go.

// ctlReadInstr reads one part of a signalling call's answer.
func ctlReadInstr(fr *frame, n *ir.CtlRead) error {
	v, err := fr.read(n.Src())
	if err != nil {
		return err
	}
	c, signalled := v.(*ctlValue)
	switch n.Part() {
	case ir.CtlPartCode:
		code := int64(ir.CtlNone)
		if signalled {
			code = int64(c.ctl)
		}
		fr.write(n.Dst(), code)
		return nil
	case ir.CtlPartValue:
		if !signalled {
			fr.write(n.Dst(), v)
			return nil
		}
		if c.ctl != ir.CtlEmitStop {
			return fmt.Errorf("vm: %s: a `%s` answer carries no value", fr.fn.Name(), c.ctl)
		}
		fr.write(n.Dst(), c.v)
		return nil
	}
	return fmt.Errorf("vm: %s: %s reads no part", fr.fn.Name(), n)
}
