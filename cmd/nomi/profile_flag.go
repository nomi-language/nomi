package main

import (
	"os"
	"strings"
)

// takeProfileFlag takes `--profile` or `--profile=<file>` from the flags
// before `nomi run`'s file, and sets NOMI_PROFILE so the run is sampled
// (internal/vm's profile.go): `--profile` prints the profile's table to
// stderr when the program ends, and `--profile=<file>` also writes it in
// pprof's format. The rest of args comes back in order.
func takeProfileFlag(args []string) []string {
	out := args[:0:0]
	i := 0
	for ; i < len(args) && isFlag(args[i]); i++ {
		switch arg := args[i]; {
		case arg == "--profile":
			os.Setenv("NOMI_PROFILE", "1")
		case strings.HasPrefix(arg, "--profile="):
			file := strings.TrimPrefix(arg, "--profile=")
			if file == "" {
				file = "1"
			}
			os.Setenv("NOMI_PROFILE", file)
		default:
			out = append(out, arg)
		}
	}
	return append(out, args[i:]...)
}
