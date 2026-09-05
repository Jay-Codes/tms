// Package tz holds the one wall clock the platform reads calendar boundaries
// against.
//
// Two subsystems need it and must agree: the notification scheduler decides an
// org's send hour on it (SPEC §6), and reports cut their month and day buckets
// on it (DECISIONS.md, 2026-09-05). Two copies of the same constant are two
// places for the boundary to drift, so it lives here and both import it.
package tz

import "time"

// LocalZone is the wall clock every org is read against. Tanzania keeps one
// zone year-round, so a single location serves the whole platform; it is named
// here rather than assumed so a second country is one constant.
const LocalZone = "Africa/Dar_es_Salaam"

// eatOffsetHours is the fallback when the host image has no tzdata: East Africa
// Time is UTC+3 with no daylight saving, so a fixed zone is exact rather than
// approximate.
const eatOffsetHours = 3

// Zone resolves the platform wall clock, falling back to a fixed UTC+3 when the
// host image ships without tzdata.
func Zone() *time.Location {
	if loc, err := time.LoadLocation(LocalZone); err == nil {
		return loc
	}
	return time.FixedZone("EAT", eatOffsetHours*60*60)
}

// LocalDate is the calendar day t names, as a UTC midnight.
//
// t must already be in the local zone: the day a renter is living in is the one
// on their own wall clock, so 02:30 EAT belongs to the day that has just started
// and not to the UTC day still running behind it. Truncating the instant would
// answer the second question, which at 23:30 UTC is the wrong day by one.
func LocalDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// StartOfDay is midnight of d on the local wall clock, as an instant.
func StartOfDay(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, Zone())
}
