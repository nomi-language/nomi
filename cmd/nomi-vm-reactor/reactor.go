//go:build wasip1

package main

// The call ABI's exports (nomi/vmrunner/reactor).
import _ "github.com/nomi-language/nomi/vmrunner/reactor"

// main is never called in a c-shared module; a main package needs one.
func main() {}
