//go:build !nomi_vsix

package editors

// vsix is empty in a build without the nomi_vsix tag, which carries no VS
// Code extension.
var vsix []byte
