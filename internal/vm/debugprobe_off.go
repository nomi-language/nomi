//go:build !nomi_debugprobe

package vm

// debugProbeOn says the Debug probe is built in (debugprobe_on.go): only
// a build with the nomi_debugprobe tag has it.
const debugProbeOn = false

func (m *Machine) execProbed(*frame) error { panic("vm: the Debug probe is not built in") }
