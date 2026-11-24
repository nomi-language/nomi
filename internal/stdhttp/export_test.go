package stdhttp

// SignatureTables is the variant names of the signature tables in code
// order, and each scheme's kind, for the test that holds them to
// std/signatures.nomi's `KeyKind` and `Scheme`.
func SignatureTables() (kinds, schemes []string, schemeKind []int64) {
	for _, k := range sigKinds {
		kinds = append(kinds, k.variant)
	}
	for _, s := range sigSchemes {
		schemes = append(schemes, s.variant)
		schemeKind = append(schemeKind, s.kind)
	}
	return kinds, schemes, schemeKind
}

// EncryptionTable is the Encryption table's variant names in code order.
func EncryptionTable() (names []string) {
	for _, e := range sigEncryptions {
		names = append(names, e.variant)
	}
	return names
}
