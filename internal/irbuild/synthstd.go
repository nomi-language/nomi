package irbuild

import (
	"github.com/nomi-language/nomi/internal/analysis"
)

// stdSynthEnumAnchors adds to a module's std enum name anchors the
// names DERIVE-SYNTHESIZED code in it may write without the file binding them:
// `Json` and `Json.ShapeError` in a derived ToJson or FromJson, in a file that
// imports only the interface. The analyzer resolves exactly these names, at
// synthesized positions only, to std's declarations
// (analysis.SynthSupportSymbol); this is the same answer for the builder.
//
// Keyed by name with no position check, and sound for the reason
// stdEnumSynthAnchors gives for std's own modules: SynthSupportSymbol answers
// only for a name the file's scope binds to NOTHING, so the ordinary anchors
// have no entry for it, and the analyzer rejects every mention of it the
// program writes. Only synthesized code reaches the name.
//
// Each symbol is matched to its spec by the declaration's (Origin, Name), the
// rule projectStdEnum and stdStructOfType apply where the program never
// writes the name.
func stdSynthEnumAnchors(fa *analysis.FileAnalysis, enums map[string]int) {
	enumDeclared := stdEnumDeclaredInStd()
	for i := range stdEnumSpecs {
		s := &stdEnumSpecs[i]
		if _, anchored := enums[s.nomi]; anchored || !enumDeclared[i] {
			continue
		}
		sym := analysis.SynthSupportSymbol(fa, s.nomi)
		if sym == nil {
			continue
		}
		if et, isEnum := sym.Type.(*analysis.EnumType); isEnum && et.Origin == s.origin && et.Name == s.nomi {
			enums[s.nomi] = i
		}
	}
}

// stdSynthStructAnchors is stdSynthEnumAnchors for the std struct specs.
func stdSynthStructAnchors(fa *analysis.FileAnalysis, structs map[string]int) {
	structValidated := stdStructValidated()
	for i := range stdStructSpecs {
		s := &stdStructSpecs[i]
		if _, anchored := structs[s.nomi]; anchored || !structValidated[i] {
			continue
		}
		sym := analysis.SynthSupportSymbol(fa, s.nomi)
		if sym == nil {
			continue
		}
		if st, isStruct := sym.Type.(*analysis.StructType); isStruct && st.Origin == s.origin && st.Name == s.nomi {
			structs[s.nomi] = i
		}
	}
}
