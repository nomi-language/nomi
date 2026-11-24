//go:build !nomi_zedwasm

package editors

// zedWasm is empty in a build without the nomi_zedwasm tag, which carries
// no Zed extension.
var zedWasm []byte
