//go:build js && wasm

package vmrunner

import (
	"encoding/json"
	"os"
	"sync"
	"syscall/js"
)

// serveCalls starts the program for a page that calls its exported
// functions, when the page asked for that: nomi.js's load sets
// globalThis.nomiServe to a function before it runs the instance. It
// answers false when the page did not, and the runner runs main as usual.
//
// The runner calls nomiServe once, after main has run:
//
//	nomiServe({ exports: "<JSON list>", call: (name, argsJSON) => Promise<string> })
//	nomiServe({ error: "<why the program could not start>" })
//
// The exports list holds one { name, params: [{ name, type }], result,
// signature } per exported function. call resolves with the result's JSON
// text, or rejects with an Error whose message is the Nomi diagnostic. On
// an error the runner exits with status 1; otherwise it serves calls until
// the page terminates the worker.
func serveCalls(image []byte, opts []Option) bool {
	serve := js.Global().Get("nomiServe")
	if serve.Type() != js.TypeFunction {
		return false
	}
	in, err := Start(image, os.Stdout, os.Stderr, ProgramArgs(), opts...)
	if err != nil {
		serve.Invoke(map[string]any{"error": err.Error()})
		os.Exit(1)
	}
	listed, err := json.Marshal(exportsJSON(in.Exports()))
	if err != nil {
		serve.Invoke(map[string]any{"error": err.Error()})
		os.Exit(1)
	}
	var mu sync.Mutex
	call := js.FuncOf(func(_ js.Value, args []js.Value) any {
		name, argsJSON := args[0].String(), args[1].String()
		return js.Global().Get("Promise").New(js.FuncOf(func(_ js.Value, settle []js.Value) any {
			resolve, reject := settle[0], settle[1]
			// A call may block (a timer, a task), which a JS callback must
			// not: it runs on its own goroutine.
			go func() {
				mu.Lock()
				result, err := in.Call(name, []byte(argsJSON))
				mu.Unlock()
				if err != nil {
					reject.Invoke(js.Global().Get("Error").New(err.Error()))
					return
				}
				resolve.Invoke(string(result))
			}()
			return nil
		}))
	})
	serve.Invoke(map[string]any{"exports": string(listed), "call": call})
	select {}
}

type exportJSON struct {
	Name      string            `json:"name"`
	Params    []exportParamJSON `json:"params"`
	Result    string            `json:"result"`
	Signature string            `json:"signature"`
}

type exportParamJSON struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func exportsJSON(exports []Export) []exportJSON {
	out := make([]exportJSON, len(exports))
	for i, e := range exports {
		x := exportJSON{Name: e.Name, Params: []exportParamJSON{}, Result: e.Result, Signature: e.Signature}
		for _, p := range e.Params {
			x.Params = append(x.Params, exportParamJSON{Name: p.Name, Type: p.Type})
		}
		out[i] = x
	}
	return out
}
