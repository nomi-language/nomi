package ffirun

// InlineGoHelper describes one generated helper available inside inline Go
// bindings. The catalog feeds both wrapper codegen and editor help.
type InlineGoHelper struct {
	Name        string
	Signature   string
	Description string
	Body        string
}

// InlineGoHelpers is the user-facing generated-helper surface for inline Go
// bindings. Keep this list small; every name becomes visible in generated
// wrapper code. The integer conversions live in nomi/hostadapt, which every
// wrapper imports for its adapters.
var InlineGoHelpers = []InlineGoHelper{
	{
		Name:        "toGoInt",
		Signature:   "func toGoInt[T hostadapt.GoInteger](n int64) T",
		Description: "Convert Nomi Int (int64) to a Go integer type with an overflow check.",
		Body:        "return hostadapt.MustGoInt[T](n)",
	},
	{
		Name:        "toNomiInt",
		Signature:   "func toNomiInt[T hostadapt.GoInteger](n T) int64",
		Description: "Convert a Go integer value back to Nomi Int with an overflow check.",
		Body:        "return hostadapt.MustNomiInt(n)",
	},
	{
		Name:        "toNomiMaybe",
		Signature:   "func toNomiMaybe[T any](value T, ok bool) *T",
		Description: "Convert Go's (value, ok) lookup shape to Nomi Maybe<T>.",
		Body:        "if !ok {\n\t\treturn nil\n\t}\n\treturn &value",
	},
	{
		Name:        "toNomiResult",
		Signature:   "func toNomiResult[T any](value T, err error) (T, error)",
		Description: "Pass through a Go (T, error) pair as Result<T, String>.",
		Body:        "return value, err",
	},
	{
		Name:        "toNomiOk",
		Signature:   "func toNomiOk[T any](value T) (T, error)",
		Description: "Return an Ok(value) branch manually.",
		Body:        "return value, nil",
	},
	{
		Name:        "toNomiErr",
		Signature:   "func toNomiErr[T any](err error) (T, error)",
		Description: "Return an Err from a Go error.",
		Body:        "var zero T\n\treturn zero, err",
	},
	{
		Name:        "toNomiErrString",
		Signature:   "func toNomiErrString[T any](message string) (T, error)",
		Description: "Return an Err from a string.",
		Body:        "var zero T\n\treturn zero, stdfmt.Errorf(\"%s\", message)",
	},
	{
		Name:        "toNomiUnitOk",
		Signature:   "func toNomiUnitOk() error",
		Description: "Return Ok(()).",
		Body:        "return nil",
	},
	{
		Name:        "toNomiUnitErr",
		Signature:   "func toNomiUnitErr(err error) error",
		Description: "Return Err from a Go error for Result<Unit, String>.",
		Body:        "return err",
	},
	{
		Name:        "toNomiUnitErrString",
		Signature:   "func toNomiUnitErrString(message string) error",
		Description: "Return Err from a string for Result<Unit, String>.",
		Body:        "return stdfmt.Errorf(\"%s\", message)",
	},
}
