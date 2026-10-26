package rt

import "strconv"

// ExitCodeError is the fault of a `main` or a boot that answers
// `Err(#Exit{code})` with a code an exit status cannot hold: outside 0 to
// 255.
func ExitCodeError(code int64) *Error {
	return &Error{Msg: "exit code " + strconv.FormatInt(code, 10) + " is outside 0 to 255"}
}
