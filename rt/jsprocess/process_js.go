//go:build js && wasm

package jsprocess

import (
	"os"
	"syscall/js"
)

func init() {
	global := js.Global()
	if env := global.Get("nomiEnv"); env.Type() == js.TypeObject {
		entries := global.Get("Object").Call("entries", env)
		for i := range entries.Length() {
			entry := entries.Index(i)
			_ = os.Setenv(entry.Index(0).String(), entry.Index(1).String())
		}
	}
	if args := global.Get("nomiArgs"); args.Type() == js.TypeObject {
		argv := make([]string, 1, 1+args.Length())
		if len(os.Args) > 0 {
			argv[0] = os.Args[0]
		}
		for i := range args.Length() {
			argv = append(argv, args.Index(i).String())
		}
		os.Args = argv
	}
}
