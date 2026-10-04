package irbuild

import "testing"

func TestIRDates_ParseAndDebug(t *testing.T) {
	verifyLambdaProgram(t, "import std/calendar.{Date, Error}\nfn main(): Result<Unit, Error> {\n  d = try Date.parse(\"2026-06-15\")\n  dbg d\n  Ok(Unit)\n}\n", "dbg line 4: d = 2026-06-15\n")
}

func TestIRDates_TourLiterals(t *testing.T) {
	verifyLambdaProgram(t, "import {\n  std/calendar.{Error}\n  std/calendar.Date\n  std/calendar.DateTime\n}\n\nfn main(): Result<Unit, Error> {\n  d = try Date\"2026-06-15\"\n  dbg d\n\n  dt = try DateTime\"2026-06-15T14:30:00-04:00[America/New_York]\"\n  dbg dt\n\n  dbg DateTime.year(dt)\n  dbg DateTime.month(dt)\n  dbg DateTime.hour(dt)\n  Ok(Unit)\n}\n", "dbg line 9: d = 2026-06-15\ndbg line 12: dt = 2026-06-15T14:30:00-04:00[America/New_York]\ndbg line 14: DateTime.year(dt) = 2026\ndbg line 15: DateTime.month(dt) = 6\ndbg line 16: DateTime.hour(dt) = 14\n")
}

func TestIRDates_TourArithmetic(t *testing.T) {
	verifyLambdaProgram(t, "import {\n  std/calendar.{Days, Error, Months, Weeks, Years}\n  std/calendar.Date\n}\n\nfn main(): Result<Unit, Error> {\n  start = try Date\"2026-05-04\"\n  jan31 = try Date\"2026-01-31\"\n  leap = try Date\"2024-02-29\"\n\n  dbg start + Days(10)\n  dbg start + Weeks(2)\n  dbg start - Weeks(1)\n  dbg jan31 + Months(1)\n  dbg jan31 - Months(1)\n  dbg leap + Years(1)\n  Ok(Unit)\n}\n", "dbg line 11: start + Days(10) = 2026-05-14\ndbg line 12: start + Weeks(2) = 2026-05-18\ndbg line 13: start - Weeks(1) = 2026-04-27\ndbg line 14: jan31 + Months(1) = 2026-02-28\ndbg line 15: jan31 - Months(1) = 2025-12-31\ndbg line 16: leap + Years(1) = 2025-02-28\n")
}

func TestIRDates_TourL170(t *testing.T) {
	verifyLambdaProgram(t, "import {\n  std/calendar.{Days, Error}\n  std/calendar.DateTime\n  std/duration.Duration\n}\n\nfn main(): Result<Unit, Error> {\n  start = try DateTime\"2026-03-07T12:00:00-05:00[America/New_York]\"\n  civil = start + Days(1)\n  physical = start + Duration.hours(24)\n\n  dbg start\n  dbg civil\n  dbg physical\n  Ok(Unit)\n}\n", "dbg line 12: start = 2026-03-07T12:00:00-05:00[America/New_York]\ndbg line 13: civil = 2026-03-08T12:00:00-04:00[America/New_York]\ndbg line 14: physical = 2026-03-08T13:00:00-04:00[America/New_York]\n")
}

func TestIRDates_TourL140(t *testing.T) {
	verifyLambdaProgram(t, "import {\n  std/calendar.{Error}\n  std/calendar.DateTime\n}\n\nfn main(): Result<Unit, Error> {\n  ny = try DateTime\"2026-06-15T12:00:00-04:00[America/New_York]\"\n  paris = try DateTime\"2026-06-15T18:00:00+02:00[Europe/Paris]\"\n\n  dbg ny == paris\n  dbg ny\n  dbg paris\n  Ok(Unit)\n}\n", "dbg line 10: ny == paris = True\ndbg line 11: ny = 2026-06-15T12:00:00-04:00[America/New_York]\ndbg line 12: paris = 2026-06-15T18:00:00+02:00[Europe/Paris]\n")
}

func TestIRDates_TourL224(t *testing.T) {
	verifyLambdaProgram(t, "import {\n  std/calendar.{Error}\n  std/calendar.{DateTime}\n}\n\nfn main(): Result<Unit, Error> {\n  ny = try DateTime\"2026-06-15T12:00:00-04:00[America/New_York]\"\n  tokyo = try DateTime.with_zone(ny, \"Asia/Tokyo\")\n  dbg tokyo\n  dbg ny == tokyo\n  Ok(Unit)\n}\n", "dbg line 9: tokyo = 2026-06-16T01:00:00+09:00[Asia/Tokyo]\ndbg line 10: ny == tokyo = True\n")
}

func TestIRDates_TourL251(t *testing.T) {
	verifyLambdaProgram(t, "import {\n  std/calendar.{Error}\n  std/calendar.{DateTime, Disambiguation}\n  std/calendar.NaiveDateTime\n  std/instant.Instant\n}\n\nfn main(): Result<Unit, Error> {\n  // 2026-11-01 01:30 NY happens twice (2am EDT rolls back to 1am EST).\n  naive = try NaiveDateTime\"2026-11-01T01:30:00\"\n\n  earlier = try DateTime.in_zone(\n    naive,\n    \"America/New_York\",\n    Disambiguation.Earlier,\n  )\n  later = try DateTime.in_zone(\n    naive,\n    \"America/New_York\",\n    Disambiguation.Later,\n  )\n\n  // Same wall reading, different offsets, different instants.\n  dbg earlier\n  dbg later\n\n  // One hour apart in the underlying instant.\n  earlier_sec = DateTime.to_instant(earlier) |> Instant.to_seconds()\n\n  later_sec = DateTime.to_instant(later) |> Instant.to_seconds()\n\n  dbg later_sec - earlier_sec\n\n  // Instant-only equality: they are NOT equal.\n  dbg earlier == later\n  Ok(Unit)\n}\n", "dbg line 24: earlier = 2026-11-01T01:30:00-04:00[America/New_York]\ndbg line 25: later = 2026-11-01T01:30:00-05:00[America/New_York]\ndbg line 32: later_sec - earlier_sec = 3600\ndbg line 35: earlier == later = False\n")
}

// TestIRDates_TourModulesL21 calls a selectively imported std method by its
// bare name and inspects a Result holding a std struct, whose Debug impl is
// keyed by the struct's std-qualified name.
func TestIRDates_TourModulesL21(t *testing.T) {
	verifyLambdaProgram(t, "import {\n  std/calendar.Date.parse\n  std/io as console\n}\n\nfn main() {\n  parsed = parse(\"2026-05-27\")\n\n  console.print(String.length(\"hello\"))\n  console.print(Iter.count([1, 2, 3]))\n  console.print(String.join([\"a\", \"b\", \"c\", \"d\"]))\n  console.inspect(parsed)\n}\n", "5\n3\nabcd\nOk(2026-05-27)\n")
}
