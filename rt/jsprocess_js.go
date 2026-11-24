//go:build js && wasm

package rt

// The arguments and environment a js/wasm host passes through
// globalThis.nomiArgs and globalThis.nomiEnv are in place before this
// package's variables initialize (hashKey reads NOMI_HASH_SEED).
import _ "github.com/nomi-language/nomi/rt/jsprocess"
