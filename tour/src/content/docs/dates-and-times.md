---
title: "Dates & Times"
description: "civil types, machine instants, and exact durations — the three layers that cover date-and-time arithmetic in Nomi."
---

Three stdlib areas cover dates and times, each handling a different layer:

- **[`std/calendar`](/reference/calendar/)** — `Date`, `Time`,
  `NaiveDateTime`, `OffsetDateTime`, `DateTime`, and shared civil-period
  vocabulary. What humans read off a calendar or clock face.
- **[`std/instant.Instant`](/reference/instant/)** — `Instant`, the machine timestamp. What hardware
  records.
- **[`std/duration.Duration`](/reference/duration/)** — `Duration`, an exact span of time (`5 seconds`,
  `30 minutes`).

The everyday zoned civil type is `DateTime`: when you say "10:30 AM on June 15
in New York," you mean a real moment in history, and a `DateTime` is what
models it (IANA-zoned, DST-aware). `NaiveDateTime` carries the "Naive" prefix as
a footgun marker — it's a wall-clock reading without a zone, useful for
templates and unzoned data, not for "the meeting happens at."

## Civil Construction

Each civil type has a typed-literal form. `DateTime` uses RFC 9557's bracket
suffix to name the IANA zone:

```nomi-run
import {
    std/calendar.{Error}
    std/calendar.Date
    std/calendar.DateTime
}

fn main(): Result<Unit, Error> {
    d = try Date"2026-06-15"
    dbg d

    dt = try DateTime"2026-06-15T14:30:00-04:00[America/New_York]"
    dbg dt

    dbg DateTime.year(dt)
    dbg DateTime.month(dt)
    dbg DateTime.hour(dt)
    Ok(Unit)
}

```
<!-- expect
dbg line 9: d = 2026-06-15
dbg line 12: dt = 2026-06-15T14:30:00-04:00[America/New_York]
dbg line 14: DateTime.year(dt) = 2026
dbg line 15: DateTime.month(dt) = 6
dbg line 16: DateTime.hour(dt) = 14
-->

## Civil Periods

`Date` shifts by civil calendar periods with `+` and `-`. Days and weeks move
by whole dates; months and years clamp when the target month has fewer days:

```nomi-run
import {
    std/calendar.{Days, Error, Months, Weeks, Years}
    std/calendar.Date
}

fn main(): Result<Unit, Error> {
    start = try Date"2026-05-04"
    jan31 = try Date"2026-01-31"
    leap = try Date"2024-02-29"

    dbg start + Days(10)
    dbg start + Weeks(2)
    dbg start - Weeks(1)
    dbg jan31 + Months(1)
    dbg jan31 - Months(1)
    dbg leap + Years(1)
    Ok(Unit)
}

```
<!-- expect
dbg line 11: start + Days(10) = 2026-05-14
dbg line 12: start + Weeks(2) = 2026-05-18
dbg line 13: start - Weeks(1) = 2026-04-27
dbg line 14: jan31 + Months(1) = 2026-02-28
dbg line 15: jan31 - Months(1) = 2025-12-31
dbg line 16: leap + Years(1) = 2025-02-28
-->

## Machine clock and durations

An [`Instant`](/reference/instant/) is a machine timestamp — an absolute point in time backed by
Unix epoch nanoseconds. It's what [`Instant.now()`](/reference/instant/#instantnow) returns, and what you store
when you need a moment independent of calendar names or time zones. A
[`Duration`](/reference/duration/) is an exact span of nanoseconds — the value you add to or subtract from an instant,
get back from `later - earlier`, or pass to [`timer.sleep`](/reference/timer/#timersleep). Construct durations with
unit functions ([`Duration.seconds(5)`](/reference/duration/#durationseconds), [`Duration.minutes(30)`](/reference/duration/#durationminutes)) and combine
them with `+`, `-`, `*`, and `/`:

```nomi-run
import {
    std/duration.Duration
    std/instant.Instant
}

fn main() {
    start = Instant.from_seconds(1_700_000_000)
    five_minutes = Duration.minutes(5)

    dbg Duration.as_minutes(five_minutes)

    later = start + five_minutes
    dbg Instant.to_seconds(later)

    // Instant.between returns the Duration between two Instants
    gap = Instant.between(start, later)
    dbg Duration.as_seconds(gap)
    dbg later - start == gap

    Unit
}

```
<!-- expect
dbg line 10: Duration.as_minutes(five_minutes) = 5
dbg line 13: Instant.to_seconds(later) = 1700000300
dbg line 17: Duration.as_seconds(gap) = 300
dbg line 18: later - start == gap = True
-->

`Instant.now()` and `timer.sleep` aren't shown here because their output isn't
deterministic — they read the real wall clock and pause for real time. See
the [Concurrency](/concurrency/) chapter for `timer.sleep` in practice.

## Instant-only equality

A `DateTime` holds a moment plus a display zone. Equality is **instant-only**
— same moment in two different zones compares equal, even though their
local readings differ:

```nomi-run
import {
    std/calendar.{Error}
    std/calendar.DateTime
}

fn main(): Result<Unit, Error> {
    ny = try DateTime"2026-06-15T12:00:00-04:00[America/New_York]"
    paris = try DateTime"2026-06-15T18:00:00+02:00[Europe/Paris]"

    dbg ny == paris
    dbg ny
    dbg paris
    Ok(Unit)
}

```
<!-- expect
dbg line 10: ny == paris = True
dbg line 11: ny = 2026-06-15T12:00:00-04:00[America/New_York]
dbg line 12: paris = 2026-06-15T18:00:00+02:00[Europe/Paris]
-->

## DST is real

The 2026 spring-forward in America/New_York lands the small hours of
March 8th. Adding one calendar day to noon March 7 lands at noon March 8 —
the *civil reading* you'd want. Adding an exact 24-hour `Duration` lands an
hour later, because one wall-clock hour disappeared overnight:

```nomi-run
import {
    std/calendar.{Days, Error}
    std/calendar.DateTime
    std/duration.Duration
}

fn main(): Result<Unit, Error> {
    start = try DateTime"2026-03-07T12:00:00-05:00[America/New_York]"
    civil = start + Days(1)
    physical = start + Duration.hours(24)

    dbg start
    dbg civil
    dbg physical
    Ok(Unit)
}

```
<!-- expect
dbg line 12: start = 2026-03-07T12:00:00-05:00[America/New_York]
dbg line 13: civil = 2026-03-08T12:00:00-04:00[America/New_York]
dbg line 14: physical = 2026-03-08T13:00:00-04:00[America/New_York]
-->

Both answers are correct. `+ Days(1)` preserves the local calendar
reading and re-resolves the UTC offset. `+ Duration.hours(24)` advances the
underlying instant by exactly 24 elapsed hours. The split lets the caller
spell which intent applies.

:::note[Continue or dig deeper]
The tour moves on to [**Typed Literals**](/typed-literals/), which
covers how `Date"…"` and `DateTime"…"` actually work: a
`Literal` conformance in `Date`'s body makes the bracket syntax
dispatch to user code. Or stay here for the deep-dive section below.
:::

## Going deeper

### Five civil types

The calendar family defines a ladder from less specific to more specific:

- `Date` — calendar day
- `Time` — time of day
- `NaiveDateTime` — wall reading, no zone (**not** a real moment)
- `OffsetDateTime` — anchored at a fixed UTC offset
- `DateTime` — anchored in an IANA zone (DST-aware)

### Re-projecting a moment into another zone

[`DateTime.with_zone(dt, zone)`](/reference/calendar/#datetimewith_zone) keeps the underlying instant and re-renders the wall
reading through a different IANA zone:

```nomi-run
import {
    std/calendar.{Error}
    std/calendar.{DateTime}
}

fn main(): Result<Unit, Error> {
    ny = try DateTime"2026-06-15T12:00:00-04:00[America/New_York]"
    tokyo = try DateTime.with_zone(ny, "Asia/Tokyo")
    dbg tokyo
    dbg ny == tokyo
    Ok(Unit)
}

```
<!-- expect
dbg line 9: tokyo = 2026-06-16T01:00:00+09:00[Asia/Tokyo]
dbg line 10: ny == tokyo = True
-->

### DST: gaps and folds

Projecting a wall reading into an IANA zone can hit a **gap** (the wall
reading never exists — clocks jumped over it) or a **fold** (the wall
reading exists twice — clocks rolled back). The `Disambiguation` enum
controls how [`DateTime.in_zone`](/reference/calendar/#datetimein_zone) resolves them:

```nomi-run
import {
    std/calendar.{Error}
    std/calendar.{DateTime, Disambiguation}
    std/calendar.NaiveDateTime
    std/instant.Instant
}

fn main(): Result<Unit, Error> {
    // 2026-11-01 01:30 NY happens twice (2am EDT rolls back to 1am EST).
    naive = try NaiveDateTime"2026-11-01T01:30:00"

    earlier = try DateTime.in_zone(
        naive,
        "America/New_York",
        Disambiguation.Earlier,
    )
    later = try DateTime.in_zone(
        naive,
        "America/New_York",
        Disambiguation.Later,
    )

    // Same wall reading, different offsets, different instants.
    dbg earlier
    dbg later

    // One hour apart in the underlying instant.
    earlier_sec = DateTime.to_instant(earlier) |> Instant.to_seconds()

    later_sec = DateTime.to_instant(later) |> Instant.to_seconds()

    dbg later_sec - earlier_sec

    // Instant-only equality: they are NOT equal.
    dbg earlier == later
    Ok(Unit)
}

```
<!-- expect
dbg line 24: earlier = 2026-11-01T01:30:00-04:00[America/New_York]
dbg line 25: later = 2026-11-01T01:30:00-05:00[America/New_York]
dbg line 32: later_sec - earlier_sec = 3600
dbg line 35: earlier == later = False
-->

The default mode is `Disambiguation.Compatible` (total resolution matching
naive intuition — earlier in folds, advances forward through gaps).
`Disambiguation.Reject` is the strict mode for when the wall reading came
from user input and the right answer is to bounce the ambiguity back via
`Err`.

### Reading the current time

[`DateTime.now_in(zone)`](/reference/calendar/#datetimenow_in) reads the system clock and projects it into the
given zone in one call (sugar for `Instant.now() |> DateTime.from_instant_in(zone)`):

```nomi-run ignore
import {
    std/calendar.{Error}
    std/calendar.{DateTime}
}

fn main(): Result<DateTime, Error> {
    now = try DateTime.now_in("America/New_York")
    dbg now // example: dbg line 6: now = 2026-05-26T17:42:00-04:00[America/New_York]
    Ok(now)
}

```

The output changes each time because this reads the real wall clock.
