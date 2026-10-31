---
title: "Dates & Times"
description: "Calendar dates and clock times, machine timestamps, and exact durations, and how Nomi handles time zones and daylight saving."
---

Three standard library modules cover dates and times:

- [`std/calendar`](/reference/calendar/) has the types a person reads off a
  calendar or a clock, plus periods such as `Days` and `Months`:
  - `Date`: a day on the calendar.
  - `Time`: a time of day.
  - `NaiveDateTime`: a date and a time with no zone.
  - `OffsetDateTime`: a date and time at a fixed offset from UTC, such as
    `-04:00`.
  - `DateTime`: a date and time in a named zone such as
    `America/New_York`.
- [`std/instant.Instant`](/reference/instant/) is a machine timestamp.
- [`std/duration.Duration`](/reference/duration/) is an exact length of
  time, such as 5 seconds or 30 minutes.

For an event that happens at a real moment, use `DateTime`. "10:30 AM on
June 15 in New York" names one moment, and a `DateTime` holds exactly that:
a date, a time, and a time zone such as `America/New_York`. It knows the
zone's daylight saving rules.

`NaiveDateTime` is a date and time with no zone. It is useful for templates
and for data that came without a zone. It does not name a real moment, so
don't use it for "when the meeting happens". The word "Naive" in its name is
there as a warning.

## Writing dates and times

Each calendar type has a typed literal. Written with backticks, it is
checked when the program compiles, so it needs no `?`. `DateTime` puts the
zone name in square brackets at the end, a format from RFC 9557:

```nomi-run
import std/calendar.{Date, DateTime}

fn main() {
    d = Date`2026-06-15`
    dbg d

    dt = DateTime`2026-06-15T14:30:00-04:00[America/New_York]`
    dbg dt

    dbg DateTime.year(dt)
    dbg DateTime.month(dt)
    dbg DateTime.hour(dt)
}

```
<!-- expect
dbg line 5: d = 2026-06-15
dbg line 8: dt = 2026-06-15T14:30:00-04:00[America/New_York]
dbg line 10: DateTime.year(dt) = 2026
dbg line 11: DateTime.month(dt) = 6
dbg line 12: DateTime.hour(dt) = 14
-->

## Adding days, months and years

Add or subtract days, weeks, months and years from a `Date` with `+` and
`-`. When you add months or years and the target month is too short for the
day, you get the month's last day. January 31 plus one month is February 28:

```nomi-run
import std/calendar.{Date, Days, Months, Weeks, Years}

fn main() {
    start = Date`2026-05-04`
    jan31 = Date`2026-01-31`
    leap = Date`2024-02-29`

    dbg start + Days(10)
    dbg start + Weeks(2)
    dbg start - Weeks(1)
    dbg jan31 + Months(1)
    dbg jan31 - Months(1)
    dbg leap + Years(1)
}

```
<!-- expect
dbg line 8: start + Days(10) = 2026-05-14
dbg line 9: start + Weeks(2) = 2026-05-18
dbg line 10: start - Weeks(1) = 2026-04-27
dbg line 11: jan31 + Months(1) = 2026-02-28
dbg line 12: jan31 - Months(1) = 2025-12-31
dbg line 13: leap + Years(1) = 2025-02-28
-->

## Machine clock and durations

An [`Instant`](/reference/instant/) is a point in time, stored as
nanoseconds since the Unix epoch. It has no calendar and no time zone.
[`Instant.now()`](/reference/instant/#instantnow) returns one. Store an
`Instant` when you need a moment that doesn't depend on any zone.

A [`Duration`](/reference/duration/) is an exact number of nanoseconds. You
add it to an instant, subtract it from one, get it back from
`later - earlier`, or pass it to [`timer.sleep`](/reference/timer/#timersleep).
Make one with a unit function such as
[`Duration.seconds(5)`](/reference/duration/#durationseconds) or
[`Duration.minutes(30)`](/reference/duration/#durationminutes). Combine
durations with `+`, `-`, `*` and `/`:

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
    dbg later - start
}

```
<!-- expect
dbg line 10: Duration.as_minutes(five_minutes) = 5
dbg line 13: Instant.to_seconds(later) = 1700000300
dbg line 14: later - start = 5m
-->

This example doesn't use `Instant.now()` or `timer.sleep`, because they read
the real clock and wait in real time, so their output changes from run to
run.

## Equality compares moments

A `DateTime` holds a moment and the zone to show it in. Two `DateTime`s are
equal when they are the same moment, even if their zones differ. Noon in
New York and 6 PM in Paris on the same day are equal:

```nomi-run
import std/calendar.DateTime

fn main() {
    ny = DateTime`2026-06-15T12:00:00-04:00[America/New_York]`
    paris = DateTime`2026-06-15T18:00:00+02:00[Europe/Paris]`

    dbg ny == paris
    dbg ny
    dbg paris
}

```
<!-- expect
dbg line 7: ny == paris = True
dbg line 8: ny = 2026-06-15T12:00:00-04:00[America/New_York]
dbg line 9: paris = 2026-06-15T18:00:00+02:00[Europe/Paris]
-->

## Daylight saving time

In 2026, New York's clocks jump from 2 AM to 3 AM on March 8. Start at noon
on March 7. Adding one calendar day gives noon on March 8. Adding
exactly 24 hours gives 1 PM, because that night was one hour short:

```nomi-run
import {
    std/calendar.{DateTime, Days}
    std/duration.Duration
}

fn main() {
    start = DateTime`2026-03-07T12:00:00-05:00[America/New_York]`
    civil = start + Days(1)
    physical = start + Duration.hours(24)

    dbg start
    dbg civil
    dbg physical
}

```
<!-- expect
dbg line 11: start = 2026-03-07T12:00:00-05:00[America/New_York]
dbg line 12: civil = 2026-03-08T12:00:00-04:00[America/New_York]
dbg line 13: physical = 2026-03-08T13:00:00-04:00[America/New_York]
-->

Both answers are correct; they answer different questions. `+ Days(1)`
keeps the clock reading, noon, and works out the new UTC offset.
`+ Duration.hours(24)` moves the moment forward by exactly 24 hours. You
pick the one you mean.

## Showing a date to a person

`dbg` and `"${date}"` write ISO 8601, `2026-10-20`, the same everywhere. A
person reads a date the way their language writes it, so `Date.format`
takes a locale:

```nomi-run
import {
    std/calendar.Date
    std/locale.Locale
}

fn main() {
    ships = Date`2026-10-20`
    dbg Date.format(ships, Locale`en-US`, style: .Long)
    dbg Date.format(ships, Locale`de`, style: .Long)
}

```
<!-- expect
dbg line 8: Date.format(ships, Locale`en-US`, style: .Long) = "October 20, 2026"
dbg line 9: Date.format(ships, Locale`de`, style: .Long) = "20. Oktober 2026"
-->

[Formatting for a Reader](/locales-and-formatting/#dates-and-times) covers
the styles, choosing the fields, times, and reading dates people type.

:::note[Continue or dig deeper]
The [Typed Literals](/typed-literals/) chapter, after Money, shows how
``Date`…` `` and ``DateTime`…` `` work, and how to write literals for your own types. The
rest of this page goes into more detail on dates and times.
:::

## Going deeper

### Showing a moment in another zone

[`DateTime.with_zone(dt, zone)`](/reference/calendar/#datetimewith_zone)
keeps the same moment and shows it in another zone. Noon in New York is
1 AM the next day in Tokyo:

```nomi-run
import std/calendar.{DateTime, Error}

fn main() -> Result<Unit, Error> {
    ny = DateTime`2026-06-15T12:00:00-04:00[America/New_York]`
    tokyo = DateTime.with_zone(ny, "Asia/Tokyo")?
    dbg tokyo
    dbg ny == tokyo
    Ok(Unit)
}

```
<!-- expect
dbg line 6: tokyo = 2026-06-16T01:00:00+09:00[Asia/Tokyo]
dbg line 7: ny == tokyo = True
-->

### DST: gaps and folds

[`DateTime.in_zone`](/reference/calendar/#datetimein_zone) puts a
`NaiveDateTime` into a zone. Daylight saving makes two cases hard:

- A gap: the clock time never happens, because the clocks jumped over it.
- A fold: the clock time happens twice, because the clocks went back.

The `Disambiguation` argument says which moment to pick:

```nomi-run
import std/calendar.{DateTime, Disambiguation, Error, NaiveDateTime}

fn main() -> Result<Unit, Error> {
    // 2026-11-01 01:30 NY happens twice (2am EDT rolls back to 1am EST).
    naive = NaiveDateTime`2026-11-01T01:30:00`

    earlier = DateTime.in_zone(naive, "America/New_York", Disambiguation.Earlier)?
    later = DateTime.in_zone(naive, "America/New_York", Disambiguation.Later)?

    // Same wall reading, different offsets, different instants.
    dbg earlier
    dbg later

    // One hour apart as moments.
    dbg DateTime.to_instant(later) - DateTime.to_instant(earlier)

    // Different moments, so not equal.
    dbg earlier == later
    Ok(Unit)
}

```
<!-- expect
dbg line 11: earlier = 2026-11-01T01:30:00-04:00[America/New_York]
dbg line 12: later = 2026-11-01T01:30:00-05:00[America/New_York]
dbg line 15: DateTime.to_instant(later) - DateTime.to_instant(earlier) = 1h
dbg line 18: earlier == later = False
-->

If you pass no mode, you get `Disambiguation.Compatible`. In a fold it picks
the earlier moment. In a gap it moves forward past the jump, so 2:30 becomes
3:30.

`Disambiguation.Reject` returns an `Err` for a gap or a fold instead of
picking. Use it when a person typed the time, so you can ask them which one
they meant.

### Reading the current time

[`DateTime.now_in(zone)`](/reference/calendar/#datetimenow_in) reads the
system clock and gives the time in that zone. It is short for
`Instant.now() |> DateTime.from_instant_in(zone)`:

```nomi-run no-run
import std/calendar.{DateTime, Error}

fn main() -> Result<Unit, Error> {
    now = DateTime.now_in("America/New_York")?
    dbg now // example: dbg line 5: now = 2026-05-26T17:42:00-04:00[America/New_York]
    Ok(Unit)
}

```

The output changes each time, because it reads the real clock.
