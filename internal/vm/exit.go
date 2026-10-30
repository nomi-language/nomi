package vm

import (
	"errors"

	"github.com/nomi-language/nomi/rt"
)

// Exit is a `main` or an entry boot that returned `Err(#Exit{code, message})`
// with std/process's Exit (ir.Module.SetMainExit, BootFailure.Exit): the
// program ends with status Code, after the same shutdown as any other end
// of main, and prints Message and a newline to stderr when it is not empty,
// with no `error: ` before it. Code is 0 to 255; an Exit with code 0 ends the
// program successfully.
type Exit struct {
	Code    int
	Message string
}

// Error is the message, the line a runner prints.
func (e *Exit) Error() string { return e.Message }

// ExitCode is the status a run that answered err exits with: 0 for nil, an
// Exit's code, and 1 for any other failure.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *Exit
	if errors.As(err, &exit) {
		return exit.Code
	}
	return 1
}

// exitOf is the Exit an `Err` payload of std/process's Exit asks for, or
// the fault of a code outside 0 to 255.
func exitOf(payload any) error {
	rec, isRecord := payload.(*rt.Record)
	if !isRecord || rec == nil {
		return &Fault{err: errors.New("vm: an Exit payload is not a struct")}
	}
	var code int64
	var message string
	for i := range rec.Desc.Fields {
		switch rec.Desc.Fields[i].Name {
		case "code":
			if v := rec.Field(i); rt.IsInt(v) {
				code = rt.IntSaturated(v)
			}
		case "message":
			message, _ = rec.Field(i).(string)
		}
	}
	if code < 0 || code > 255 {
		return &Fault{err: rt.ExitCodeError(code)}
	}
	return &Exit{Code: int(code), Message: message}
}
