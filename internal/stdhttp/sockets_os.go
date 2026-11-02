//go:build !js && !wasip1

package stdhttp

// hasSockets is true where a program can listen and dial.
const hasSockets = true

// noSocketsMessage is never read here.
const noSocketsMessage = ""
