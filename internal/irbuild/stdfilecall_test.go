package irbuild

import "testing"

// A program calls a shared std module's top-level functions as it calls a
// sibling file's: one leaving out a defaulted parameter (std/base64's
// `encode`, a Nomi body), one whose body calls an earlier shared module's
// top-level function (std/hash's `sha256_hex` over std/hex's `encode`), and
// a public `host fn` a stdlibbindings Funcs row answers (std/random's
// `secure_bytes`). The builder declined each: the short-call route took only
// a type owner, an earlier module's top-level functions were missing from a
// later module's index, and a file-qualified call took only rt rows.
func TestStdFileCall_DefaultsCrossModuleAndGoRows(t *testing.T) {
	if testing.Short() {
		t.Skip("integration; -short")
	}
	verifyLambdaProgram(t, `import
    std::base64
    std::hash
    std::io
    std::random
end

fn main()
    data = String.to_bytes("f")
    io.print(base64.encode(data))
    io.print(base64.encode(data, padded: False))
    io.print(base64.encode_url(Bytes.from_list([251, 255, 191, 102])))
    io.print(hash.sha256_hex(String.to_bytes("abc")))
    io.print(Bytes.length(random.secure_bytes(16)))
end
`, "Zg==\nZg\n-_-_Zg==\nba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad\n16\n")
}
