//go:build !nomi_stdbundle

package irbuild

// embeddedStdBundle is empty in a binary built without the nomi_stdbundle tag,
// which lowers the stdlib on first use and caches the bundle (stdbundle.go).
var embeddedStdBundle []byte
