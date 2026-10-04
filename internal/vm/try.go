package vm

import (
	"fmt"

	"github.com/nomi-language/nomi/internal/ir"
)

// tryReturn is consumed by the current activation before a caller can see it,
// except in a test body, which propagates a report rather than a value: there
// it leaves the activation and the runner turns it into an
// rt.EarlyReturnFailure carrying at's line and text. See testFn.
type tryReturn struct {
	value any
	at    *ir.Try
}

func (*tryReturn) Error() string { return "vm: try propagation" }

func tryValue(fr *frame, n *ir.Try) error {
	v, err := fr.read(n.Src())
	if err != nil {
		return err
	}
	e, ok := enumRecord(v)
	if !ok {
		return fmt.Errorf("vm: try requires a Maybe or Result, got %T", v)
	}
	switch e.Desc.Name {
	case "maybe.Maybe":
		switch variantName(e) {
		case "Some":
			return nil
		case "None":
			return &tryReturn{value: e, at: n}
		}
	case "results.Result":
		switch variantName(e) {
		case "Ok":
			return nil
		case "Err":
			return &tryReturn{value: e, at: n}
		}
	}
	return fmt.Errorf("vm: invalid try operand %s.%s", e.Desc.Name, variantName(e))
}
