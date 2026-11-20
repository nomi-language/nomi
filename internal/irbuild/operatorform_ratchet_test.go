package irbuild

// maxOperatorFormSites is how many distinct source sites of each operator
// with no record its switch reads (unreadAnswers), by family, the programs
// TestIRGolden_RecordsMatchTheVM runs reach. Each count only goes down.
// An operator the builder writes carries a record (carryOperator: an
// `==` for a call, tupleHashOf's Int arithmetic), and an operator over a
// type parameter resolves at its instance's checker types
// (checkSubstCensus).
var maxOperatorFormSites = map[string]int{
	"equality, no record (written by the builder)":    0,
	"equality, no record (unanswered)":                0,
	"equality, per instance, unresolved":              0,
	"ordering, no record (written by the builder)":    0,
	"ordering, no record (unanswered)":                0,
	"ordering, per instance, unresolved":              0,
	"ordering, dispatched":                            0,
	"arithmetic, no record (written by the builder)":  0,
	"arithmetic, no record (unanswered)":              0,
	"arithmetic, per instance, unresolved":            0,
	"arithmetic, structural":                          0,
	"arithmetic, dispatched":                          0,
	"bitwise not, no record (written by the builder)": 0,
	"bitwise not, no record (unanswered)":             0,
	"bitwise not, per instance, unresolved":           0,
	"bitwise not, structural":                         0,
	"bitwise not, dispatched":                         0,
}

// heldOperatorClasses are the classes maxOperatorFormSites holds: every
// class of an operator a switch has no arm for.
func heldOperatorClasses() map[string]bool {
	held := map[string]bool{}
	for f, answers := range unreadAnswers {
		for _, a := range answers {
			held[unreadClass(f, a)] = true
		}
	}
	return held
}

// operatorRatchet holds the operator census to maxOperatorFormSites.
func operatorRatchet() censusRatchet {
	return censusRatchet{what: "operator sites", held: heldOperatorClasses(), max: maxOperatorFormSites,
		fix: "lower the operator as its record says"}
}
