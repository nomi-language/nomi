package rt

import (
	"mime/quotedprintable"
	"strings"
	"time"
)

// The Go side of std/mail_host: the encodings std::mail's renderer takes
// from Go's mime/quotedprintable and time.

// MailQuotedPrintable is `mail_host.quoted_printable`: text written by Go's
// quotedprintable.Writer as text, so each line break (CRLF, LF or a lone
// CR) is written as CRLF, whitespace before a break or at the end is
// encoded, and no line is longer than 76 characters.
func MailQuotedPrintable(text string) string {
	var b strings.Builder
	w := quotedprintable.NewWriter(&b)
	_, _ = w.Write([]byte(text))
	_ = w.Close()
	return b.String()
}

// MailDate is `mail_host.date`: the instant nanos nanoseconds after the
// Unix epoch as an RFC 5322 date in UTC, Go's RFC1123Z layout.
func MailDate(nanos int64) string {
	return time.Unix(0, nanos).UTC().Format(time.RFC1123Z)
}
