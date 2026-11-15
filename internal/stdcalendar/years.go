package stdcalendar

// Year arithmetic for a year of any size: the leap rule, month lengths, day
// numbers and the ISO 8601 spelling of a year. Every function here is exact
// for every Int, with an int64 path for the years and day counts a program
// ordinarily holds and math/big past them.

import (
	"math/big"
	"strings"

	"github.com/nomi-language/nomi/rt"
)

// addI, subI and mulI are exact Int arithmetic, int64 when the answer fits.
func addI(a, b rt.Int) rt.Int { return rt.IntOf(rt.IntAdd(a.Value(), b.Value())) }

func subI(a, b rt.Int) rt.Int { return rt.IntOf(rt.IntSub(a.Value(), b.Value())) }

func mulI(a, b rt.Int) rt.Int { return rt.IntOf(rt.IntMul(a.Value(), b.Value())) }

// small is n as an Int.
func small(n int64) rt.Int { return rt.SmallInt(n) }

// floorDivModI is a divided by b > 0, rounded toward negative infinity, and
// the remainder, which is never negative and is below b.
func floorDivModI(a rt.Int, b int64) (rt.Int, int64) {
	if n, ok := a.Int64(); ok {
		q, r := floorDivMod(n, b)
		return small(q), r
	}
	q, r := new(big.Int).DivMod(a.Big(), big.NewInt(b), new(big.Int))
	return rt.BigIntOf(q), r.Int64()
}

// yearOfCycle is year modulo 400, from 0 to 399. The Gregorian calendar
// repeats every 400 years, which are 146097 days, a whole number of weeks,
// so the leap rule, a month's length and a date's weekday depend on this
// alone.
func yearOfCycle(year rt.Int) int64 {
	_, r := floorDivModI(year, 400)
	return r
}

// IsLeapYear reports whether year is a leap year in the proleptic Gregorian
// calendar: divisible by 4, except a century year not divisible by 400.
func IsLeapYear(year rt.Int) bool {
	y := yearOfCycle(year)
	return y%4 == 0 && (y%100 != 0 || y == 0)
}

// DaysInMonth is the length of month 1 to 12 of year, leap years included.
func DaysInMonth(year rt.Int, month int64) int64 {
	switch month {
	case 2:
		if IsLeapYear(year) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

// smallYear bounds the years whose day numbers DaysFromCivil and
// CivilFromDays work out in int64: a trillion years is under 2^49 days.
const smallYear = int64(1) << 40

// DaysFromCivil is the day number of a proleptic Gregorian date, 0 for
// 1970-01-01 (Howard Hinnant's days_from_civil), exact for every year.
func DaysFromCivil(year rt.Int, month, day int64) rt.Int {
	if y, ok := year.Int64(); ok && y > -smallYear && y < smallYear {
		return small(daysFromCivil64(y, month, day))
	}
	// Whole 400-year cycles first, each 146097 days; what is left is a
	// year within one cycle, which int64 holds.
	era, yoe := floorDivModI(year, 400)
	days := daysFromCivil64(2000+yoe, month, day)
	return addI(mulI(era, small(146097)), small(days-5*146097))
}

// daysFromCivil64 is DaysFromCivil for a year whose day number fits int64.
func daysFromCivil64(year, month, day int64) int64 {
	if month <= 2 {
		year--
	}
	era, yoe := floorDivMod(year, 400)
	mp := (month + 9) % 12
	doy := (153*mp+2)/5 + day - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

// CivilFromDays is DaysFromCivil's inverse, exact for every day number.
func CivilFromDays(z rt.Int) (year rt.Int, month, day int64) {
	if n, ok := z.Int64(); ok && n > -(1<<60) && n < 1<<60 {
		y, m, d := civilFromDays64(n)
		return small(y), m, d
	}
	// Whole 400-year cycles first, as DaysFromCivil counts them.
	cycles, rest := floorDivModI(z, 146097)
	y, m, d := civilFromDays64(rest)
	return addI(mulI(cycles, small(400)), small(y)), m, d
}

// civilFromDays64 is CivilFromDays for a day number under 2^60 either way.
func civilFromDays64(z int64) (year, month, day int64) {
	era, doe := floorDivMod(z+719468, 146097)
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	day = doy - (153*mp+2)/5 + 1
	month = mp + 3
	if mp >= 10 {
		month = mp - 9
	}
	year = yoe + era*400
	if month <= 2 {
		year++
	}
	return year, month, day
}

// WeekdayOf is the day of the week of a date, 0 for Sunday to 6 for
// Saturday, for every year: 1970-01-01 was a Thursday.
func WeekdayOf(year rt.Int, month, day int64) int64 {
	// The weekday repeats with the 400-year cycle, so the cycle's year in
	// 2000 to 2399 has the same one.
	_, r := floorDivMod(daysFromCivil64(2000+yearOfCycle(year), month, day)+4, 7)
	return r
}

// DayOfYear is the date's day within its year, 1 for January 1.
func DayOfYear(year rt.Int, month, day int64) int64 {
	y := 2000 + yearOfCycle(year)
	return daysFromCivil64(y, month, day) - daysFromCivil64(y, 1, 1) + 1
}

// ISOYearText is a year as ISO 8601 writes it, and RFC 3339 and RFC 9557
// with it: four digits for 0000 to 9999, and otherwise the expanded form,
// a sign and at least six digits (`+010000`, `-000001`,
// `+123456789012345678901`), as Temporal writes a year past four digits.
func ISOYearText(year rt.Int) string {
	if y, ok := year.Int64(); ok && y >= 0 && y <= 9999 {
		return fourDigits(y)
	}
	digits := year.String()
	sign := "+"
	if year.Sign() < 0 {
		sign, digits = "-", digits[1:]
	}
	if len(digits) < 6 {
		digits = strings.Repeat("0", 6-len(digits)) + digits
	}
	return sign + digits
}

func fourDigits(y int64) string {
	return string([]byte{byte('0' + y/1000), byte('0' + y/100%10), byte('0' + y/10%10), byte('0' + y%10)})
}

// ParseISOYear reads a year as ISO 8601 writes it: four or more digits, or
// a sign and four or more digits, so it reads ISOYearText's every answer
// and the unpadded five-digit years other tools write (`10000`). A year
// of fewer digits, and minus zero, are not years. ok is false for text
// that is not one; numeric is false when a digit is not one.
func ParseISOYear(s string) (year rt.Int, numeric, ok bool) {
	digits := s
	neg := false
	if digits != "" && (digits[0] == '+' || digits[0] == '-') {
		neg = digits[0] == '-'
		digits = digits[1:]
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return rt.Int{}, false, false
		}
	}
	if len(digits) < 4 {
		return rt.Int{}, true, false
	}
	v, parsed := rt.ParseIntValue(digits)
	if !parsed {
		return rt.Int{}, false, false
	}
	year = rt.IntOf(v)
	if neg {
		if year.Sign() == 0 {
			return rt.Int{}, true, false
		}
		year = subI(small(0), year)
	}
	return year, true, true
}
