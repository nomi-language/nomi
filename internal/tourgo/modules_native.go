//go:build !js

package tourgo

// linkedModules is empty outside the browser: only the tour's js/wasm
// build links a tour module's Go side (linked_modules_gen.go, built in the
// GoWork workspace), so a block that imports one runs only in the tour's
// wasm (Unlinked).
var linkedModules []linkedModule
