package hostgen

// stdSource reads the standard library from the source tree, since this
// package's tests may not import nomi/std (see DirSource).
var stdSource = DirSource("../../std")
