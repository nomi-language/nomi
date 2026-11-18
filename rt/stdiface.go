package rt

// The stdlib interface functions internal/irbuild's specs name (Method).
// A row is here when the builder can give the interface a complete method
// shape: no type parameters, no defaults, no host-backed methods, and every
// parameter and result representable.
var (
	// Display.to_string(value: self): String
	MDisplayToString = NewMethod[func(*Frame, any) string]("Display.to_string")

	// Comparable.compare(a: self, b: self): Ordering
	MComparableCompare = NewMethod[func(*Frame, any, any) Ordering]("Comparable.compare")

	// Debug.inspect(value: self): String
	MDebugInspect = NewMethod[func(*Frame, any) string]("Debug.inspect")
)
