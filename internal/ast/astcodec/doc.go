// Package astcodec writes syntax trees as bytes and reads them back: the
// node half of a file's interface (analysis.FileInterface). The encoder and
// decoder of each node type are generated from internal/ast's types
// (codec_gen.go, by codecgen; `go generate ./internal/ast/astcodec`), so
// every exported field is written; TestGeneratedCodecIsCurrent fails until
// the generator is rerun after an AST change, and
// TestInterfaceCodecCoversEveryNode fills every field of every node form
// and requires it to survive.
//
// A pointer the tree holds twice is written once and then by its number,
// so a decoded tree shares what the encoded one shares. Decoding bytes the
// encoder did not write, or bytes cut short, is an error, never a panic.
// The two unexported fields the analysis sets on a checked tree,
// TaggedString's expansion and template (an `Html` literal's expansion,
// codecgen's analysisSets), are not written; an interface never holds
// either. *ast.Expansion and *big.Int have hand-written codecs (codec.go).
//
// # File interfaces
//
// A file's interface is what another file's check reads of it: its nodes as
// a build gets them, less what no other file reads. analysis's
// interface.go lists what is taken out and what stays, and
// interface_check.go how a file is checked against the others' interfaces.
// analysis.ExtractInterface records the interface hash, a SHA-256 of
// interfaceFingerprint, and FileInterface.Encode adds a magic, a version
// (interfaceVersion), the file's key, path and hash to this package's
// bytes. Two more bodies stay than interface.go's list names: those of an
// `impl Release` and an `impl Commit`, since another file's release check
// reads the call each makes (internal/analysis package doc, "Releasing
// resources").
//
// Interfaces are registered through the sweeps like source, so a check
// against them costs a registration of the closure; decoding std's takes
// about 4 ms against 60 ms to parse it. A derive's synthesized impl records
// no reference, so its impl header's names are looked up in the file's
// scope; counting derives as visible everywhere would make a leaf's check
// at 50k lines read 215 files' impls instead of none.
// TestImplHeadersFromInterfacesMatchTheProgram holds the headers to the
// program's check from source.
//
// The users of file interfaces:
//
//   - the stdlib, built from its interfaces (std/shared.go, "From interfaces");
//   - a kept program whose files changed, which checks the changed files
//     against the rest's stored interfaces and lowers them alone (vmhost
//     package doc, "File records"; internal/irbuild package doc, "Lowering
//     one file"); a check's analysis.FileCheck carries the other files its
//     build read, which such a lowering declares;
//   - a REPL session, whose program grows by one file per input checked
//     against the earlier files' interfaces held in memory
//     (analysis.ProgramInterfaces.AddFile).
//
// FuzzCheckFromInterfacesMatchesFull (internal/analysis) holds a file
// checked against interfaces to a check of the whole program from source.
package astcodec
