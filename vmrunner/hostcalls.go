package vmrunner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/nomi-language/nomi/hostadapt"
	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/vm"
	"github.com/nomi-language/nomi/rt"
)

// HostCaller answers one call of a program's `host fn` for a host that
// answers in JSON: key is the function's key (frontend.HostKey: its name in
// the entry file, `helpers/util.greet` in another), args its arguments as
// a JSON array, each encoded by its parameter type's ToJson, and the answer
// is `{"ok": <result>}`, the result as its type's FromJson reads it, or
// `{"error": "<message>"}`. An error is a failure to reach the host at all.
type HostCaller func(key string, args []byte) (answer []byte, err error)

// WithHostCalls answers the program's own `host fn`s whose keys keys lists
// through call, as the wasip1 reactor answers them through its
// nomi.host_call import. A JSON host's table carries no Go: each argument
// is encoded and the answer decoded by the conversions the image records
// for the function (ir.HostFn), std/json's, which an exported function's
// arguments and result cross by too. A key the program declares no `host
// fn` for is not an error, so one host can serve many programs.
//
// The load refuses a program, naming the key, that declares a `host fn`
// no table answers (via names where the host lists its keys), and one
// whose listed `host fn` cannot cross as JSON: a parameter or a result
// with no ToJson or FromJson (a function value, an opaque host type, a
// stream). An answer `{"error": ...}` fails the call that crossed, as a
// trap with the host's message. Each crossing is `host:<key>` in a host's
// lists (WithEffectsFrom), as a Go table's is.
func WithHostCalls(via string, keys []string, call HostCaller) Option {
	return func(c *config) {
		c.hostCalls = &hostCalls{via: via, keys: append([]string{}, keys...), call: call}
	}
}

// hostCalls is WithHostCalls' option.
type hostCalls struct {
	via  string
	keys []string
	call HostCaller
}

// table is the host table that answers, for the program in im, each key
// h lists that the program declares a `host fn` for, or why the program
// cannot be answered so. linked are the runner's Go tables (WithHosts),
// which answer their keys themselves.
func (h *hostCalls) table(im ir.Image, linked []HostTable) (HostTable, error) {
	byKey := map[string]*ir.HostFn{}
	for i := range im.HostFns {
		byKey[im.HostFns[i].Key] = &im.HostFns[i]
	}
	goKeys, err := hostadapt.TableKeys(linked...)
	if err != nil {
		return nil, err
	}
	var answered []*ir.HostFn
	for _, key := range h.keys {
		if !slices.Contains(im.HostKeys, key) {
			continue
		}
		fn := byKey[key]
		switch {
		case goKeys[key]:
			return nil, fmt.Errorf("%s lists %q, which this runner's linked host tables answer", h.via, key)
		case fn == nil:
			return nil, fmt.Errorf("%s lists %q, and the program's `host fn` of that key is declared where no host answers it in JSON", h.via, key)
		case fn.NoJSON != "":
			return nil, fmt.Errorf("%s lists %q, and %s (`host fn %s`); "+
				"a host that answers in JSON receives the arguments and answers the result as JSON values, "+
				"so a function value, an opaque host type or a stream crosses only to a Go host (vmhost.WithHosts)",
				h.via, key, fn.NoJSON, fn.Signature())
		}
		answered = append(answered, fn)
	}
	return func(env *hostadapt.Env) (map[string]hostadapt.Func, error) {
		funcs := make(map[string]hostadapt.Func, len(answered))
		for _, fn := range answered {
			funcs[fn.Key] = h.crossing(env, fn)
		}
		return funcs, nil
	}, nil
}

// crossing is fn's host function: its operands encoded on the calling
// frame, the host's answer read, and its `ok` decoded there.
func (h *hostCalls) crossing(env *hostadapt.Env, fn *ir.HostFn) hostadapt.Func {
	encode := make([]rt.Value, len(fn.Params))
	for i, p := range fn.Params {
		encode[i] = vm.FunctionOf(p.Encode)
	}
	var decode rt.Value
	if fn.Decode != nil {
		decode = vm.FunctionOf(fn.Decode)
	}
	return func(fr *rt.Frame, args []rt.Value) (rt.Value, error) {
		if len(args) != len(encode) {
			return nil, hostadapt.Arity(fn.Key, len(encode), len(args))
		}
		var b bytes.Buffer
		b.WriteByte('[')
		for i, enc := range encode {
			text, err := env.Call(fr, enc, []rt.Value{args[i]})
			if err != nil {
				return nil, err
			}
			s, ok := text.(string)
			if !ok {
				return nil, fmt.Errorf("vm: argument `%s` of host function %s was not encoded as JSON text", fn.Params[i].Name, fn.Key)
			}
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(s)
		}
		b.WriteByte(']')
		answer, err := h.call(fn.Key, b.Bytes())
		if err != nil {
			return nil, &rt.Error{Msg: fmt.Sprintf("host function `%s` could not be called: %v", fn.Key, err)}
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(answer, &got); err != nil || got == nil {
			return nil, answeredBadly(fn, answer)
		}
		if raw, failed := got["error"]; failed {
			var text string
			if err := json.Unmarshal(raw, &text); err != nil {
				return nil, answeredBadly(fn, answer)
			}
			return nil, &rt.Error{Msg: text}
		}
		value, ok := got["ok"]
		if !ok || len(got) != 1 {
			return nil, answeredBadly(fn, answer)
		}
		if decode == nil {
			return rt.Unit{}, nil
		}
		decoded, err := env.Call(fr, decode, []rt.Value{string(value)})
		if err != nil {
			return nil, err
		}
		v, why, isOk := resultOf(decoded)
		if !isOk {
			return nil, &rt.Error{Msg: fmt.Sprintf("host function `%s` answered %s, which is not a %s: %s", fn.Key, value, fn.Result, why)}
		}
		return v, nil
	}
}

// answeredBadly is the trap of a host whose answer to fn is neither
// `{"ok": ...}` nor `{"error": "..."}`.
func answeredBadly(fn *ir.HostFn, answer []byte) error {
	const most = 200
	text := string(answer)
	if len(text) > most {
		text = text[:most] + "..."
	}
	return &rt.Error{Msg: fmt.Sprintf("host function `%s` answered %q, which is neither {\"ok\": <result>} nor {\"error\": \"<message>\"}", fn.Key, text)}
}
