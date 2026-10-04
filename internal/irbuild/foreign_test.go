package irbuild

// Cross-package type identity: a user type declared in one Nomi file, used from
// another.
//
// A bug at a package boundary is usually a wrong answer that still runs, and a
// comparison harness does not see it. So each claim is pinned absolutely:
// spelled-out stdout, spelled-out generated text, or a compile error. The
// compile-error form is the strongest: a mix-up between two same-named Nomi
// types is not a comparison that has to come out right, it is a program that
// does not typecheck.
