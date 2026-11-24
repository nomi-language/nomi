package vmhost

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// std::cipher's opening and sealing with a given nonce, std::hash's HKDF
// and std::key_exchange's agreement compute from their operands alone, so a
// refinement rule may call them and the program's literals are checked as
// it compiles. A rule that makes a key from the system's entropy has effects
// and is refused with the error every rule with effects gets.
func TestCipherRules_APureCallIsCheckedAndAKeyFromEntropyIsRefused(t *testing.T) {
	const pure = `import
    std::cipher.{self, Algorithm, Key}
    std::hash
    std::key_exchange.{self, Curve, PrivateKey}
end

refine Opens String when fn s ->
    key = Key.from_bytes(Algorithm.ChaCha20Poly1305, String.to_bytes("kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk"))!
    nonce = String.to_bytes("nnnnnnnnnnnn")
    sealed = cipher.seal_with_nonce(key, nonce, String.to_bytes(s))!
    cipher.open_with_nonce(key, nonce, sealed) == Ok(String.to_bytes(s))
end

refine Derived String when fn s ->
    Bytes.length(hash.hkdf_sha256(String.to_bytes(s), String.to_bytes(""), String.to_bytes("label"), 32)!) == 32
end

refine Agreed String when fn s ->
    ours = PrivateKey.from_bytes(Curve.X25519, String.to_bytes(s))!
    theirs = PrivateKey.from_bytes(Curve.X25519, String.to_bytes("tttttttttttttttttttttttttttttttt"))!
    key_exchange.shared_secret(ours, PrivateKey.public_key(theirs))
        == key_exchange.shared_secret(theirs, PrivateKey.public_key(ours))
end

fn main()
    a: Opens = "hello"
    b: Derived = "secret"
    c: Agreed = "kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk"
    dbg (a, b, c)
end
`
	p, err := LoadSource("main", pure)
	if err != nil {
		t.Fatalf("a rule that seals, opens, derives and agrees keys is refused: %v", err)
	}
	var out bytes.Buffer
	if err := p.Run(context.Background(), &out, nil, false); err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}
	if want := `dbg line 29: (a, b, c) = ("hello", "secret", "kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk")` + "\n"; out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}

	const fresh = `import std::cipher.{Algorithm, Key}

refine Fresh String when fn s -> String.length(s) > 0 and Key.generate(Algorithm.Aes256Gcm) != Key.generate(Algorithm.Aes256Gcm)

fn main()
    f: Fresh = "x"
    dbg f
end
`
	ds := literalDiagnostics(t, fresh)
	const want = "the rule of Fresh has effects ("
	for _, d := range ds {
		if strings.HasPrefix(d.Message, want) && strings.HasSuffix(d.Message, "): a rule is a pure function of the value it checks") &&
			d.Code == InvalidRefinementCode {
			return
		}
	}
	t.Errorf("diagnostics = %v, want one reading %q...", ds, want)
}
