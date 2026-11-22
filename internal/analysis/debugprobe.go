//go:build nomi_debugprobe

package analysis

// DebugProbeClass names the synthesized Debug a line belongs to: "decl"
// for a module-level type's name-only Debug (SynthesizeUniversalDebug),
// "block" for a type declared in a body (synthesizeBlockDebug), "derived"
// for a `derive Debug`'s body, "" for any other line. Only the Debug probe
// reads it (vmhost's debugprobe.go).
func DebugProbeClass(line int) string {
	if line < synthLineBase {
		return ""
	}
	last := synthLineBase + (synthSlotCount-1)*synthLineStride
	if line >= last {
		return "block"
	}
	slot := (line - synthLineBase) / synthLineStride
	origin := synthOrigin(slot / synthRegionSize)
	switch kind := synthKind((slot - int(origin)*synthRegionSize) % synthKindCount); {
	case kind == synthKindAutoDebug && origin == synthOriginDecl:
		return "decl"
	case kind == synthKindDebug && (origin == synthOriginDecl || origin == synthOriginConformance):
		return "derived"
	}
	return ""
}
