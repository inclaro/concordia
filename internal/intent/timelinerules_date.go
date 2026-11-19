package intent

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	hoursPerDay       = 24
	secondsPerHour    = 3600
	secondsPerMinute  = 60
	nanosPerMilli     = 1_000_000
	maxTimeClipMillis = 8.64e15 // the largest time value of an ECMAScript Date, in milliseconds.
)

// timelineISODate is the date time string format of ECMAScript (the form that
// JavaScript Date.parse accepts first), plus the space separator of V8.
var timelineISODate = regexp.MustCompile(
	`^([+-]\d{6}|\d{4})(?:-(\d\d)(?:-(\d\d))?)?(?:[T ](\d\d):(\d\d)(?::(\d\d)(?:\.(\d+))?)?(Z|[+-]\d\d:\d\d)?)?$`)

// timelineParseDate approximates JavaScript Date.parse: it returns the time in
// milliseconds and false for NaN. It is exact for the ECMAScript date time
// string format (a date without a time is UTC; a date time without an offset is
// local time, as in V8; a day up to 31 overflows into the next month as in V8). V8 also accepts many legacy formats ("Jan 1, 2024",
// "1/2/2024"); of these, only the common slash and month-name forms are ported
// (see timelineLegacyLayouts). Not ported: other legacy forms (for example the
// bare number "12", which V8 reads as a month), a "-000000" year (V8 accepts
// it), and surrounding whitespace (V8 then uses its legacy parser, which reads a
// date without a time as local time).
func timelineParseDate(text string) (float64, bool) {
	text = strings.Trim(text, jsSpaces)

	if parts := timelineISODate.FindStringSubmatch(text); parts != nil {
		return timelineParseISO(parts)
	}

	for _, layout := range timelineLegacyLayouts {
		loc := time.Local
		if strings.HasSuffix(layout, "GMT") {
			loc = time.UTC
		}

		if parsed, err := time.ParseInLocation(layout, text, loc); err == nil {
			return float64(parsed.UnixMilli()), true
		}
	}

	return 0, false
}

var timelineLegacyLayouts = []string{
	"2006/01/02", "2006/1/2", "1/2/2006", "01/02/2006",
	"Jan 2 2006", "Jan 2, 2006", "January 2 2006", "January 2, 2006",
	"2 Jan 2006", "2 January 2006",
	"Mon, 02 Jan 2006 15:04:05 GMT", "Mon Jan 02 2006 15:04:05",
	"2006-1-2", "2006-01-02T15:04:05-0700",
}

// timelineISOFields are the numeric fields of a date time string.
type timelineISOFields struct {
	year, month, day         int
	hour, minute, sec, milli int
}

// timelineISOFieldsOf reads the fields of the submatches of timelineISODate.
func timelineISOFieldsOf(parts []string) timelineISOFields {
	year, _ := strconv.Atoi(parts[1])

	return timelineISOFields{
		year: year, month: timelineISOInt(parts[2], 1), day: timelineISOInt(parts[3], 1),
		hour: timelineISOInt(parts[4], 0), minute: timelineISOInt(parts[5], 0), sec: timelineISOInt(parts[6], 0),
		milli: timelineISOMillis(parts[7]),
	}
}

// timelineISOInt is the number of digits, or fallback when there are none.
func timelineISOInt(digits string, fallback int) int {
	if digits == "" {
		return fallback
	}

	n, _ := strconv.Atoi(digits)

	return n
}

// timelineISOMillis is the milliseconds of the fraction digits of a second.
func timelineISOMillis(fraction string) int {
	if fraction == "" {
		return 0
	}

	millis, _ := strconv.Atoi((fraction + "00")[:3])

	return millis
}

// valid tells whether the fields are in the range of the format.
func (f timelineISOFields) valid() bool {
	return f.validDate() && f.validClock()
}

func (f timelineISOFields) validDate() bool {
	return f.month >= 1 && f.month <= 12 && f.day >= 1 && f.day <= 31
}

// validClock tells whether the time is in range; 24:00:00.000 is the end of a day.
func (f timelineISOFields) validClock() bool {
	if f.hour > hoursPerDay {
		return false
	}

	if f.hour == hoursPerDay {
		return f.minute+f.sec+f.milli == 0
	}

	return f.minute <= 59 && f.sec <= 59
}

// timelineISOLocation gives the location of a date time string: UTC for "Z" and
// for a date without a time, local time without an offset, else the offset.
func timelineISOLocation(zone string, hasTime bool) (*time.Location, bool) {
	switch {
	case zone == "Z" || !hasTime:
		return time.UTC, true
	case zone == "":
		return time.Local, true
	}

	sign := 1
	if zone[0] == '-' {
		sign = -1
	}

	hours, _ := strconv.Atoi(zone[1:3])
	minutes, _ := strconv.Atoi(zone[4:6])

	if hours > 23 || minutes > 59 {
		return nil, false
	}

	return time.FixedZone("", sign*(hours*secondsPerHour+minutes*secondsPerMinute)), true
}

func timelineParseISO(parts []string) (float64, bool) {
	if parts[1] == "-000000" {
		return 0, false
	}

	fields := timelineISOFieldsOf(parts)
	if !fields.valid() {
		return 0, false
	}

	loc, ok := timelineISOLocation(parts[8], parts[4] != "")
	if !ok {
		return 0, false
	}

	moment := time.Date(fields.year, time.Month(fields.month), fields.day,
		fields.hour, fields.minute, fields.sec, fields.milli*nanosPerMilli, loc)

	value := float64(moment.UnixMilli())
	if math.Abs(value) > maxTimeClipMillis {
		return 0, false
	}

	return value, true
}
