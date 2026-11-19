// Package capability is the one rule that names an outside call and the one
// check that holds outside calls to lists, shared by everything that holds
// a program to them: nomi.toml's [capabilities] lists and an embedding
// host's lists in vmhost, and a host's list in vmrunner, which links no
// front end. It imports nothing of the compiler's, so the checker (which
// validates nomi.toml's lists), vmhost and vmrunner all call it.
//
// An outside call is what internal/vm's reach walk reports
// (vm.ReachedOutsideCalls): a call that acts on or reads from anything
// outside the machine. Its name is the std module the code outside std
// called, as an import writes it (`std::fs`); for a crossing no std module
// declares, `go:` and the Go import path of the `gopkg` its declaration
// names, when the caller can read that from source, and otherwise `host:`
// and the crossing's key. A compiled image records no Go import path, so a
// runner names a Go binding by its key.
//
// Two lists hold a call (Allows): a list of std names holds every call
// but a Go binding, by its name, and a list of modules holds a Go binding
// by the module whose files hold it. Go is allowed by module, never by
// package, so a `go:` name is never an entry of a list (NameProblem); a
// module list's entries are module paths (ModuleProblem).
//
// How a refusal reads is shared too: Message and GoMessage are its
// headline, HostAddHint and RemoveHint its help, and vm.ReachText says how
// the walk reached the call. vmhost renders them as diagnostics at the
// call's source; vmrunner, which has no source, renders them at the file
// and line the image records.
package capability
