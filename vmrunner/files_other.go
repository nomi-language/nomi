//go:build !(js && wasm)

package vmrunner

// Outside the browser a program's files are the disk's: there is no page to
// seed them or hand them back to.
func seedPageFiles()   {}
func returnPageFiles() {}
