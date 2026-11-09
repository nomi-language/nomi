//go:build !js && !nomi_tourmodules

package tourgo

// linkedModules is empty outside the browser: only the tour's js/wasm
// build and a native build with the nomi_tourmodules tag link a tour
// module's Go side (linked_modules_gen.go, built in the GoWork workspace),
// so elsewhere a block that imports one cannot run (Unlinked).
var linkedModules []linkedModule
