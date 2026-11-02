package rt

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// Base64Encode is std/base64's `standard`, behind `encode`: data in RFC 4648's standard
// alphabet, padded with `=` when padded is true.
func Base64Encode(data Bytes, padded bool) string {
	if padded {
		return base64.StdEncoding.EncodeToString([]byte(data))
	}
	return base64.RawStdEncoding.EncodeToString([]byte(data))
}

// Base64Decode is std/base64's `decode`: the standard alphabet, padded or
// not.
func Base64Decode(text string) Result[Bytes, string] {
	return base64Decode(base64.StdEncoding, text)
}

// Base64URLEncode is std/base64's `url_safe`, behind `encode_url`: data in RFC 4648's URL and
// file name safe alphabet (`-` and `_` for `+` and `/`), padded with `=`
// when padded is true.
func Base64URLEncode(data Bytes, padded bool) string {
	if padded {
		return base64.URLEncoding.EncodeToString([]byte(data))
	}
	return base64.RawURLEncoding.EncodeToString([]byte(data))
}

// Base64URLDecode is std/base64's `decode_url`: the URL-safe alphabet,
// padded or not.
func Base64URLDecode(text string) Result[Bytes, string] {
	return base64Decode(base64.URLEncoding, text)
}

// base64Decode decodes text with enc, padded when text ends in `=` and
// unpadded otherwise, so both forms of one alphabet decode.
func base64Decode(enc *base64.Encoding, text string) Result[Bytes, string] {
	trimmed := strings.TrimRight(text, "\r\n")
	if !strings.HasSuffix(trimmed, "=") {
		enc = enc.WithPadding(base64.NoPadding)
	}
	data, err := enc.DecodeString(text)
	if err != nil {
		var at base64.CorruptInputError
		if errors.As(err, &at) {
			return Err[Bytes, string](fmt.Sprintf("invalid base64 at byte %d", int64(at)))
		}
		return Err[Bytes, string]("invalid base64: " + err.Error())
	}
	return Ok[Bytes, string](Bytes(data))
}

// HexEncode is std/hex's `encode`: two lowercase hexadecimal digits per
// byte.
func HexEncode(data Bytes) string {
	return hex.EncodeToString([]byte(data))
}

// HexDecode is std/hex's `decode`: pairs of hexadecimal digits, either case.
func HexDecode(text string) Result[Bytes, string] {
	data, err := hex.DecodeString(text)
	if err != nil {
		var bad hex.InvalidByteError
		switch {
		case errors.As(err, &bad):
			at := strings.IndexByte(text, byte(bad))
			if bad >= utf8.RuneSelf {
				// Part of a multi-byte character, which no hex digit is.
				return Err[Bytes, string](fmt.Sprintf("invalid hex digit at byte %d", at))
			}
			return Err[Bytes, string](fmt.Sprintf("invalid hex digit %q at byte %d", rune(bad), at))
		case errors.Is(err, hex.ErrLength):
			return Err[Bytes, string](fmt.Sprintf("hex text has an odd number of digits (%d)", len(text)))
		}
		return Err[Bytes, string]("invalid hex: " + err.Error())
	}
	return Ok[Bytes, string](Bytes(data))
}

// URLEscapeQuery is std/url's `escape_query`: text percent-encoded for a
// query string's key or value, a space as `+`.
func URLEscapeQuery(text string) string { return url.QueryEscape(text) }

// URLUnescapeQuery is std/url's `unescape_query`: escape_query's inverse,
// `+` read as a space.
func URLUnescapeQuery(text string) Result[string, string] {
	return urlUnescape(text, url.QueryUnescape)
}

// URLEscapePathSegment is std/url's `escape_path_segment`: text
// percent-encoded for one segment of a URL path, `/` included, a space as
// `%20`.
func URLEscapePathSegment(text string) string { return url.PathEscape(text) }

// URLUnescapePathSegment is std/url's `unescape_path_segment`:
// escape_path_segment's inverse, `+` kept as itself.
func URLUnescapePathSegment(text string) Result[string, string] {
	return urlUnescape(text, url.PathUnescape)
}

func urlUnescape(text string, unescape func(string) (string, error)) Result[string, string] {
	out, err := unescape(text)
	if err != nil {
		var bad url.EscapeError
		if errors.As(err, &bad) {
			return Err[string, string](fmt.Sprintf("invalid percent escape %q", string(bad)))
		}
		return Err[string, string](err.Error())
	}
	if !utf8.ValidString(out) {
		return Err[string, string]("the unescaped text is not valid UTF-8")
	}
	return Ok[string, string](out)
}
