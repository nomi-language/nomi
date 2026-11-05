//go:build !nomi_stdbundle

package std

// embeddedStdInterfaces is empty without the nomi_stdbundle tag: the
// process reads the stdlib's interfaces from the artifact cache, or makes
// them (shared.go).
var embeddedStdInterfaces []byte
