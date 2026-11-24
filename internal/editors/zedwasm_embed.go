//go:build nomi_zedwasm

package editors

import _ "embed"

// zedWasm is the Zed extension's WebAssembly, editors/zed built with
// `cargo build --release --target wasm32-wasip2`, which scripts/release.sh
// builds and internal/runners/hostrunnersgen -zed stages just before the
// build (and `make install-zed` stages for its own). No extension.wasm is
// in the source tree here: the build supplies it with `go build -overlay`,
// so a build with the tag and no extension fails to compile rather than
// embedding a stale one.
//
//go:embed extension.wasm
var zedWasm []byte
