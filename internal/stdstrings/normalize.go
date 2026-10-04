// Package stdstrings is std/strings' host implementation for the operations
// that need a dependency `rt` does not carry.
//
// # The reason this is not in rt, and why it is a DIFFERENT reason from stdio's
//
// One function today: `String.normalize`, which needs
// golang.org/x/text/unicode/norm. rt does not import x/text, and rt is linked
// by everything that runs Nomi, so an x/text import in rt would be a decision
// about every binary. Placed here, it is a decision about the compiler only.
//
// The precedent went the other way once, deliberately:
// `github.com/rivo/uniseg` was allowed in rt for `String.length`'s grapheme
// clusters, on the stated ground that "`String.length` is an ordinary
// language primitive rather than a toolchain feature". Normalization is not that:
// it is a Unicode-table operation on a form the caller names.
//
// rt's import allowlist (rt/imports_test.go, TestRuntimeImportsOnlyItsAllowlist)
// does not include x/text, so an x/text import in rt fails that test.
//
// # ONE implementation of the form mapping
//
// The switch from rt.NormalForm onto a `norm.Form` lives here once.
// internal/stdlibbindings binds Normalize as an `RtFuncs` row.
package stdstrings

import (
	"golang.org/x/text/unicode/norm"

	"github.com/nomi-language/nomi/rt"
)

// Normalize is std/strings' `normalize`: s in the given Unicode normalization
// form.
//
// An UNKNOWN tag returns s unchanged rather than panicking, and that choice is
// not defensive padding — it is the only answer available here. The tag comes
// from a Go value, so there is no Nomi position to blame, and rt.NormalForm's
// zero value is the reserved-invalid tag 0, which a never-constructed struct
// carries. A Nomi call cannot produce an unknown tag at all, since the only
// NormalForm a program holds is one built from an anchored variant reference.
// TestNormalizeTagsCoverEveryVariant is what keeps
// that from becoming a silent no-op when std adds a fifth form: it derives the
// tag list from rt and fails on any tag this switch does not name.
func Normalize(s string, form rt.NormalForm) string {
	f, known := goForm(form)
	if !known {
		return s
	}
	return f.String(s)
}

// goForm maps a Nomi NormalForm onto x/text's, reporting whether the tag is one
// this package knows.
//
// Split out of Normalize so a test can ask about the mapping without going
// through a string, which is what makes the coverage assertion above possible.
func goForm(form rt.NormalForm) (norm.Form, bool) {
	switch form.Tag {
	case rt.TagNFC:
		return norm.NFC, true
	case rt.TagNFD:
		return norm.NFD, true
	case rt.TagNFKC:
		return norm.NFKC, true
	case rt.TagNFKD:
		return norm.NFKD, true
	}
	return norm.NFC, false
}
