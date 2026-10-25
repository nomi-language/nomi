//go:build js && wasm

package vmrunner

import (
	"errors"
	"syscall/js"
)

// localeFetcher fetches a browser build's locale file through the page:
// nomi.js sets globalThis.nomiLocale to a function that answers a promise
// of the file's text, fetched from the locales/ folder beside app.wasm (or
// the folder its `locales` option names). The calling goroutine waits on
// the promise, which lets the page's event loop run: Locale.load blocks
// its task and nothing else.
func localeFetcher() func(tag string) ([]byte, error) {
	return func(tag string) ([]byte, error) {
		fetch := js.Global().Get("nomiLocale")
		if fetch.Type() != js.TypeFunction {
			return nil, errors.New("the page did not start the program with nomi.js, which fetches locale files")
		}
		type answer struct {
			text string
			err  error
		}
		done := make(chan answer, 1)
		resolve := js.FuncOf(func(_ js.Value, args []js.Value) any {
			done <- answer{text: args[0].String()}
			return nil
		})
		reject := js.FuncOf(func(_ js.Value, args []js.Value) any {
			msg := "the fetch failed"
			if len(args) > 0 {
				if m := args[0].Get("message"); m.Type() == js.TypeString {
					msg = m.String()
				} else {
					msg = args[0].Call("toString").String()
				}
			}
			done <- answer{err: errors.New(msg)}
			return nil
		})
		defer resolve.Release()
		defer reject.Release()
		fetch.Invoke(tag).Call("then", resolve, reject)
		a := <-done
		return []byte(a.text), a.err
	}
}
