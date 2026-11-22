package hostgen

import "github.com/nomi-language/nomi/std"

// stdSource reads the standard library's sources as the binary embeds them.
var stdSource = std.ReadFile
