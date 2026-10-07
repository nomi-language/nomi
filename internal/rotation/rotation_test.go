package rotation

import (
	"testing"
	"time"
)

// TestTodayStart_OneStartPerUTCDay holds the start seed to the UTC day: the
// same all day, whatever the local zone, and dayStride further the next day.
func TestTodayStart_OneStartPerUTCDay(t *testing.T) {
	morning := time.Date(2026, 10, 6, 0, 0, 1, 0, time.UTC)
	night := time.Date(2026, 10, 6, 23, 59, 59, 0, time.UTC)
	elsewhere := night.In(time.FixedZone("UTC+10", 10*60*60))
	next := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	if TodayStart(morning) != TodayStart(night) || TodayStart(night) != TodayStart(elsewhere) {
		t.Fatalf("one UTC day has several starts: %d, %d, %d", TodayStart(morning), TodayStart(night), TodayStart(elsewhere))
	}
	if got := TodayStart(next) - TodayStart(night); got != dayStride {
		t.Fatalf("the next day starts %d seeds later, want %d", got, dayStride)
	}
}

// TestFor_PinnedStartAndCount reads NOMI_GEN_SEED and NOMI_GEN_COUNT.
func TestFor_PinnedStartAndCount(t *testing.T) {
	t.Setenv("NOMI_GEN_SEED", "42")
	t.Setenv("NOMI_GEN_COUNT", "3")
	s := For(t, "./internal/rotation", 150)
	if !s.Pinned || s.Start != 42 || s.Count != 3 {
		t.Fatalf("got %+v, want start 42, count 3, pinned", s)
	}
	if got := s.Seeds(); len(got) != 3 || got[0] != 42 || got[2] != 44 {
		t.Fatalf("seeds %v, want [42 43 44]", got)
	}
}
