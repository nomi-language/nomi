//go:build !nomi_hostrunners

package runners

// embeddedRunners is empty in a binary built without the nomi_hostrunners
// tag, which finds, builds or downloads every runner (Runner).
var embeddedRunners map[string][]byte
