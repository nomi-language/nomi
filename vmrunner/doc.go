// Package vmrunner is the runtime half of `nomi build`: a runner binary reads
// the linked IR image `nomi build` appended to its own executable, decodes it
// and runs `main` on the VM exactly as `nomi run` does. A built binary runs
// at `nomi run` speed: it skips the front end and the lowering at startup
// and runs the same VM over the same IR. It is not native code.
//
// It links the VM, rt, the stdlib host adapters and the IR decoder, and no
// front end: no parser, analyzer, IR builder or LSP. vmrunner's deps test
// pins that. A program that crosses into std/compiler's hosts is the one
// exception, and its runner links nomi/vmrunner/compiler, which brings the
// front end with it.
//
// A native executable is the runner's bytes, then the image (its code, then
// its embedded files' region; files.go), then a fixed 24-byte trailer: the
// image's offset and length as little-endian uint64s and the magic
// "NOMIEXE\x01". The runner reads the code and maps the region. A
// WebAssembly runner carries the image inside the module instead (wasm.go).
//
// A runner nothing was appended to (nomi-vm) runs a .nomic file `nomi build
// --no-vm` wrote, handed to it by path or framed on stdin (imagefile.go,
// bare.go).
//
// It is a public package because an FFI project's runner is generated into a
// separate Go module, which can import only this module's public packages.
//
// # Pruning
//
// The image holds only the functions the VM's own reachability walk
// (vm.PruneImage) reaches from `main`, the boot and each `export fn`. Since
// it is the VM's walk, a call it did not foresee fails naming the callee
// rather than running something else.
// TestIRImage_PrunedProgramsMatchTheCommittedRecords (internal/vm) runs
// every corpus and tour program pruned against its record.
//
// The image names each source file as `go build -trimpath` would
// (vmhost's imageName): a stdlib module as `std/<file>.nomi`, a project file
// by its path from the project root, and an embedded file's source the same
// way. It records the project root only for a program that reaches
// std/compiler. A Go-FFI runner recognizes an entry that declares its own
// bindings by that relative name.
//
// # Runner variants
//
// Every runner comes in two builds. The untagged one links every std
// module's Go side. The nomi_nonet build (nomi-vm-nonet,
// nomi-vm-reactor-nonet) fails the network crossings with an error, so it
// links no net/http, crypto/tls, crypto/x509 or net. Leaving the packages
// out is the only way to drop them, since importing net/http keeps its
// client transport alive through DefaultTransport; the js/wasm and wasip1
// runners go from 23.5 to 14.6 MB stripped, darwin's from 12.6 to 7.9 MB.
//
// `go generate ./internal/stdlibadapters` writes the split
// (internal/hostgen/stdtable/netsplit.go): adapters_gen.go holds every
// adapter that does not call internal/stdhttp and NetworkNames,
// adapters_net_gen.go the ones that do (untagged), and adapters_nonet_gen.go
// an error stub for each (tagged), so Bind names the same crossings in
// both. `nomi build` appends to the smaller runner when the pruned image
// reaches none of NetworkNames (vmhost.Program.UsesNetwork);
// ffirun.Variant.NoNetwork names, caches and finds it, and a prebuilt
// runner's -tags must match its name. A runner without the network refuses
// an image that reaches one before running anything (network.go).
//
// A Go-FFI project follows the same rule: its wrapper reports whether the
// image reaches a network crossing (ffirun.BuiltImage.NoNetwork), and
// ffirun.ProjectRunner builds the runner with nomi_nonet when it does not.
// The tag drops only std's network adapters, never the project's own Go.
// TestNoNetworkVMImportsNoNetworkCode (cmd/nomi-vm) holds the packages out
// of the tagged build on every target.
//
// # Image layout and embedded files
//
// An image is its code (ir.EncodeImage, ir.DecodeImage) followed by its
// files region: the contents of each file the code's ConstEmbeddeds name,
// once per SHA-256, back to back. The code's header gives its length
// (ir.ImageCodeLen) and the code ends with the region's table,
// ir.Image.Files (sum, size, offset). EncodeImage writes only the code, so
// ffirun.BuildImage, the build cache (cmd/nomi's keptBuild) and an FFI
// wrapper's image hold no file bytes. Whoever writes an output appends the
// region from disk (vmhost.WriteFilesRegion), streaming each file and
// refusing one whose size or sum changed. The paths come from the
// program's own lowering (Program.EmbeddedSources, which a wrapper writes
// beside its image as `sources`). A native executable's trailer, a .nomic's
// header and a js/wasm module's nomi.image section count the region in the
// image's length and are written head first (ImageFileHead,
// WasmImageSectionHead) so the region streams. A wasip1 module's image goes
// into its image area whole.
//
// files.go says how a runner reads the region in each place it can be.
// TestOpenImageAt_StartingReadsTheCodeAndAFileOnlyWhenRead counts the bytes
// read at start (25 KB for an image embedding 64 MiB) and checks that
// reading one small file reads none of a big one.
//
// # WebAssembly
//
// Nothing may follow a WebAssembly module's last section, so a wasm target
// carries the image inside the module (wasm.go). A wasip1 runner (nomi-vm
// or nomi-vm-reactor) holds it in a bss array of WasmImageAreaSize (4 MiB)
// that an added data segment fills (image_wasip1.go, EmbedWasmImage), so the
// module needs no file access and no symbols to be found, and a stripped
// release runner works. A js/wasm module carries it in a nomi.image custom
// section that nomi.js hands it as the nomiImage Uint8Array (image_js.go).
//
// # The wasip1 reactor
//
// `--target wasip1/wasm` of a program with an `export fn` appends it to
// cmd/nomi-vm-reactor: the VM linked -buildmode=c-shared as a WASI reactor
// exporting `_initialize` and the generic call ABI of vmrunner/reactor. A
// program without one stays a command module. The ABI is generic because Go
// fixes export names when the runner is compiled and one runner serves
// every program, so function names travel as data. A Go-binding project
// with an `export fn` gets the reactor variant of its own runner, whose
// generated main imports the //go:wasmexport functions from
// vmrunner/reactor and passes its host table to them from init.
//
// Each `export fn` is recorded in the image with std/json's converters
// instantiated at its parameters' and result's types, so a call crosses as
// JSON text. A runner boots an image once, runs its `main`, then runs one
// exported function at a time on that booted program (Instance, exports.go).
//
// `nomi build-vm --reactor` copies out nomi-vm-reactor with nothing embedded
// (image area length zero), so a host compiles it once, instantiates it per
// script and hands each instance one .nomic through nomi_load. A reactor
// with an embedded program refuses nomi_load of another. Releases ship the
// reactor in the wasip1 archive beside nomi-vm. It also writes nomi-wasi.js
// and nomi-wasi.d.ts (cmd/nomi/wasi): a dependency-free ES module
// implementing the WASI preview1 imports the reactor needs (stdio, clocks,
// random, a blocking poll_oneoff sleep, no preopens, ENOSYS for any other
// import), offering loadRuntime, load and call, and run, in browsers and
// Node (TestBuildCommand_ReactorWritesTheWasiLoader drives it in Node).
// `nomi build --target wasip1/wasm` writes the same two files beside a
// reactor with an embedded program; there load() and run() with no
// argument call nomi_load with length 0, which boots the embedded program
// and returns its exports (a reactor with none refuses it).
//
// # Compiled programs
//
// `--no-vm` writes the image alone as a compiled program, <entry>.nomic,
// whose header imagefile.go lays out. It needs no Go toolchain; Go-FFI
// projects are refused. It records the exported functions, so a reactor can
// call them. A nomi-vm with nothing appended runs one by path or framed on
// stdin, and `nomi run` runs one in process.
//
// A field added to an IR node fails TestImage_EveryNodeFieldIsEncoded
// (internal/ir) until the encoder, decoder and ir.FormatVersion carry it.
// The bump makes every nomi-vm refuse old .nomic files by name.
//
// # Runners from a release
//
// Release archives ship nomi-vm, and `nomi build` uses the one beside
// `nomi` only after checking it comes from the same commit. `--target`
// downloads, checksum-verifies and caches another platform's runner from
// the same release (internal/ffirun/prebuilt.go). A wasm runner's commit is
// read from its data section (internal/gobuildinfo), since debug/buildinfo
// cannot read a WebAssembly module. A project of lib/ modules at the
// release's commit is appended to nomi-vm-lib from the target's lib
// archive, found and checked the same way (internal/ffirun package doc,
// "Prebuilt lib runners").
package vmrunner
