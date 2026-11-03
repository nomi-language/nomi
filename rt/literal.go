package rt

// LiteralError is Nomi's `std/literals.LiteralError`: why a checked literal's
// check refused its text. Fragment is the index of the static the problem is
// in and Offset counts code points into it.
//
// Field order and Go names are a contract with internal/irbuild/stdstruct.go,
// which writes field selectors from its own spec.
type LiteralError struct {
	Fragment int64
	Offset   int64
	Message  string
}
