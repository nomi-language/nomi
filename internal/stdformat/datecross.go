package stdformat

import (
	"github.com/nomi-language/nomi/internal/stdcalendar"
	"github.com/nomi-language/nomi/internal/stdlocale"
	"github.com/nomi-language/nomi/rt"
)

// The read functions' answers as std/calendar's attempts: the value parsed
// from its ISO 8601 form, or the Error variant's ordinal (1 InvalidFormat,
// 2 InvalidValue) and its text.

// answerKind is an answer's error ordinal, 0 for a value, and its text.
func answerKind(answer string) (int64, string) {
	if answer == "" {
		return 1, "nothing to read"
	}
	switch answer[0] {
	case '=':
		return 0, answer[1:]
	case '?':
		return 2, answer[1:]
	}
	return 1, answer[1:]
}

func dateAttempt(answer string) stdcalendar.DateAttempt {
	if kind, text := answerKind(answer); kind != 0 {
		return stdcalendar.DateAttempt{Kind: kind, Reason: text}
	}
	return stdcalendar.FFIDateParse(answer[1:])
}

func timeAttempt(answer string) stdcalendar.TimeAttempt {
	if kind, text := answerKind(answer); kind != 0 {
		return stdcalendar.TimeAttempt{Kind: kind, Reason: text}
	}
	return stdcalendar.FFITimeParse(answer[1:])
}

func naiveAttempt(answer string) stdcalendar.NaiveAttempt {
	if kind, text := answerKind(answer); kind != 0 {
		return stdcalendar.NaiveAttempt{Kind: kind, Reason: text}
	}
	return stdcalendar.FFINaiveParse(answer[1:])
}

// FFIParseDateStyle is `calendar.date_parse_style`.
func FFIParseDateStyle(fr *rt.Frame, text, locale, spec string, lenient bool) stdcalendar.DateAttempt {
	return dateAttempt(ParseStyle(stdlocale.Data(fr), text, locale, spec, lenient))
}

// FFIParseTimeStyle is `calendar.time_parse_style`.
func FFIParseTimeStyle(fr *rt.Frame, text, locale, spec string, lenient bool) stdcalendar.TimeAttempt {
	return timeAttempt(ParseStyle(stdlocale.Data(fr), text, locale, spec, lenient))
}

// FFIParseDatePattern is `calendar.date_parse_pattern`.
func FFIParseDatePattern(text, pattern string) stdcalendar.DateAttempt {
	return dateAttempt(ParsePattern(text, pattern, true, false))
}

// FFIParseTimePattern is `calendar.time_parse_pattern`.
func FFIParseTimePattern(text, pattern string) stdcalendar.TimeAttempt {
	return timeAttempt(ParsePattern(text, pattern, false, true))
}

// FFIParseNaivePattern is `calendar.naive_parse_pattern`.
func FFIParseNaivePattern(text, pattern string) stdcalendar.NaiveAttempt {
	return naiveAttempt(ParsePattern(text, pattern, true, true))
}
