package rt

// std/calendar's types for text a reader reads, here for Locale's reason
// (rt/locale.go): nothing in rt operates on them, and a Go type in rt is
// what lets a std signature name one across generated packages. The
// formatting is internal/stdformat's.

// DateStyle is `std/calendar.DateStyle` (Full, Long, Medium, Short), a bare
// enum: std/calendar.nomi passes its CLDR name across, so the type only
// lets `Date.format`'s signature lower.
type DateStyle struct {
	Tag uint8
}

// TimeStyle is `std/calendar.TimeStyle`, DateStyle's twin for times.
type TimeStyle struct {
	Tag uint8
}

// Weekday is `std/calendar.Weekday`, Monday (1) to Sunday (7).
type Weekday struct {
	Tag uint8
}

// TimeUnit is `std/calendar.TimeUnit`, Second (1) to Year (8).
type TimeUnit struct {
	Tag uint8
}

// DateFields, TimeFields and DateTimeFields are std/calendar's skeletons,
// and DatePattern, TimePattern and DateTimePattern its fixed patterns: each
// the text it was checked as.
type (
	DateFields      string
	TimeFields      string
	DateTimeFields  string
	DatePattern     string
	TimePattern     string
	DateTimePattern string
)
