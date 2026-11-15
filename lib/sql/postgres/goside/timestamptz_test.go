//go:build !js

package goside

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// A timestamptz of any year crosses as its exact nanoseconds, never
// wrapped by UnixNano into a moment in another century, and goes back as
// the same moment. A parameter past what a timestamptz holds is refused
// rather than sent wrapped.
func TestTimestamptzOfAnyYearCrosses(t *testing.T) {
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), "253402300799000000000"},
		{time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC), "-11676096000000000000"},
		{time.Date(2026, 10, 19, 12, 30, 5, 250000000, time.UTC), "1792413005250000000"},
	} {
		c, err := scalar(tc.at, 0, "expires_at")
		if err != nil || c.Kind != KindTimestamptz || c.Text != tc.want {
			t.Errorf("%v: %+v, %v", tc.at, c, err)
			continue
		}
		back, err := param(KindTimestamptz, 0, 0, c.Text, nil)
		if err != nil || !back.(pgtype.Timestamptz).Time.Equal(tc.at) {
			t.Errorf("%v back: %+v, %v", tc.at, back, err)
		}
	}
	_, err := param(KindTimestamptz, 0, 0, "1"+strings.Repeat("0", 30), nil)
	if err == nil || !strings.Contains(err.Error(), "past what a timestamptz holds") {
		t.Errorf("an instant past a timestamptz: %v", err)
	}
}

// A Date and a NaiveDateTime cross as their ISO 8601 text, whose year
// outside 0000 to 9999 has a sign and six or more digits, and a date or
// timestamp column read back writes a year as Go does, which Date.parse
// reads.
func TestCivilTextOfAnyYearCrosses(t *testing.T) {
	for _, tc := range []struct {
		kind int64
		text string
		want time.Time
	}{
		{KindDate, "2026-10-19", time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)},
		{KindDate, "+010000-01-01", time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{KindDate, "-000044-03-15", time.Date(-44, 3, 15, 0, 0, 0, 0, time.UTC)},
		{KindDate, "+5874897-12-31", time.Date(5874897, 12, 31, 0, 0, 0, 0, time.UTC)},
		{KindTimestamp, "+294276-02-29T23:59:59.5", time.Date(294276, 2, 29, 23, 59, 59, 500000000, time.UTC)},
	} {
		got, err := param(tc.kind, 0, 0, tc.text, nil)
		if err != nil {
			t.Errorf("%s: %v", tc.text, err)
			continue
		}
		var at time.Time
		switch v := got.(type) {
		case pgtype.Date:
			at = v.Time
		case pgtype.Timestamp:
			at = v.Time
		}
		if !at.Equal(tc.want) {
			t.Errorf("%s crosses as %v, want %v", tc.text, at, tc.want)
		}
	}
	if _, err := param(KindDate, 0, 0, "+"+strings.Repeat("9", 30)+"-01-01", nil); err == nil {
		t.Error("a year past an int crossed")
	}
}
