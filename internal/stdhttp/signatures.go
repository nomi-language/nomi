package stdhttp

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"

	"github.com/nomi-language/nomi/rt"
)

// std/signatures_host: Ed25519, ECDSA P-256 and RSA keys and signatures
// over Go's crypto packages. The key encodings are crypto/x509's, which
// links net, so these sit with the network adapters (the
// internal/hostgen/stdtable split): a program that never touches a key
// runs on the runner without crypto/x509.
//
// A key crosses as DER: a private key as PKCS #8, a public key as PKIX
// (SubjectPublicKeyInfo). Every function that takes one takes DER that
// SigPrivateFromDER or SigPublicFromDER accepted, which std/signatures'
// opaque key types guarantee, so a key that does not parse is a trap.

// Key kinds, as std/signatures' KeyKind numbers its variants.
const (
	sigKindEd25519 = 0
	sigKindP256    = 1
	sigKindRSA     = 2
)

// Signature schemes, as std/signatures' Scheme numbers its variants.
const (
	sigEd25519  = 0
	sigES256    = 1
	sigRS256    = 2
	sigPS256    = 3
	sigES256DER = 4
)

// Bounds on an RSA key's size in bits. 2048 is the least NIST allows for
// new keys; 8192 bounds how long generation takes.
const (
	sigRSAMinBits = 2048
	sigRSAMaxBits = 8192
)

// sigKind is a parsed private or public key's kind, or an error for a key
// std/signatures does not take.
func sigKind(key any) (int64, error) {
	switch k := key.(type) {
	case ed25519.PrivateKey, ed25519.PublicKey:
		return sigKindEd25519, nil
	case *ecdsa.PrivateKey:
		return p256Kind(k.Curve)
	case *ecdsa.PublicKey:
		return p256Kind(k.Curve)
	case *rsa.PrivateKey, *rsa.PublicKey:
		return sigKindRSA, nil
	}
	return -1, fmt.Errorf("unsupported key type %T", key)
}

func p256Kind(c elliptic.Curve) (int64, error) {
	if c != elliptic.P256() {
		return -1, fmt.Errorf("unsupported ECDSA curve %s; only P-256 is supported", c.Params().Name)
	}
	return sigKindP256, nil
}

// SigGenerateEd25519 is `signatures_host.generate_ed25519`: a new Ed25519
// private key from crypto/rand.
func SigGenerateEd25519() rt.Bytes {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		rt.Trap("signatures: generating an Ed25519 key: " + err.Error())
	}
	return marshalPrivate(key)
}

// SigGenerateP256 is `signatures_host.generate_p256`: a new ECDSA P-256
// private key from crypto/rand.
func SigGenerateP256() rt.Bytes {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		rt.Trap("signatures: generating a P-256 key: " + err.Error())
	}
	return marshalPrivate(key)
}

// SigGenerateRSA is `signatures_host.generate_rsa`: a new RSA private key
// of bits bits from crypto/rand, or why bits is out of range.
func SigGenerateRSA(bits int64) rt.Result[rt.Bytes, string] {
	if bits < sigRSAMinBits || bits > sigRSAMaxBits {
		return rt.Err[rt.Bytes](fmt.Sprintf("an RSA key has %d to %d bits, got %d", sigRSAMinBits, sigRSAMaxBits, bits))
	}
	key, err := rsa.GenerateKey(rand.Reader, int(bits))
	if err != nil {
		return rt.Err[rt.Bytes](err.Error())
	}
	return rt.Ok[rt.Bytes, string](marshalPrivate(key))
}

func marshalPrivate(key any) rt.Bytes {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		rt.Trap("signatures: encoding a private key: " + err.Error())
	}
	return rt.Bytes(der)
}

func marshalPublic(key any) rt.Bytes {
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		rt.Trap("signatures: encoding a public key: " + err.Error())
	}
	return rt.Bytes(der)
}

// parsePrivate is a private key from DER that SigPrivateFromDER accepted.
func parsePrivate(der rt.Bytes) any {
	key, err := x509.ParsePKCS8PrivateKey([]byte(der))
	if err != nil {
		rt.Trap("signatures: a private key does not parse: " + err.Error())
	}
	return key
}

// parsePublic is a public key from DER that SigPublicFromDER accepted.
func parsePublic(der rt.Bytes) any {
	key, err := x509.ParsePKIXPublicKey([]byte(der))
	if err != nil {
		rt.Trap("signatures: a public key does not parse: " + err.Error())
	}
	return key
}

// privateFrom is key as PKCS #8, or why std/signatures does not take it.
func privateFrom(key any, err error) rt.Result[rt.Bytes, string] {
	if err != nil {
		return rt.Err[rt.Bytes](err.Error())
	}
	if _, err := sigKind(key); err != nil {
		return rt.Err[rt.Bytes](err.Error())
	}
	return rt.Ok[rt.Bytes, string](marshalPrivate(key))
}

// publicFrom is key as PKIX, or why std/signatures does not take it.
func publicFrom(key any, err error) rt.Result[rt.Bytes, string] {
	if err != nil {
		return rt.Err[rt.Bytes](err.Error())
	}
	if _, err := sigKind(key); err != nil {
		return rt.Err[rt.Bytes](err.Error())
	}
	return rt.Ok[rt.Bytes, string](marshalPublic(key))
}

// SigPrivateFromDER is `signatures_host.private_from_der`: a private key
// in PKCS #8, PKCS #1 (RSA) or SEC 1 (EC) DER, as PKCS #8.
func SigPrivateFromDER(der rt.Bytes) rt.Result[rt.Bytes, string] {
	data := []byte(der)
	if key, err := x509.ParsePKCS8PrivateKey(data); err == nil {
		return privateFrom(key, nil)
	}
	if key, err := x509.ParsePKCS1PrivateKey(data); err == nil {
		return privateFrom(key, nil)
	}
	if key, err := x509.ParseECPrivateKey(data); err == nil {
		return privateFrom(key, nil)
	}
	return privateFrom(nil, errors.New("not a PKCS #8, PKCS #1 or SEC 1 private key"))
}

// SigPrivateFromPEM is `signatures_host.private_from_pem`: the first
// private key in text, a `PRIVATE KEY`, `RSA PRIVATE KEY` or
// `EC PRIVATE KEY` block, as PKCS #8.
func SigPrivateFromPEM(text string) rt.Result[rt.Bytes, string] {
	rest := []byte(text)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return rt.Err[rt.Bytes]("no private key PEM block")
		}
		if _, encrypted := block.Headers["DEK-Info"]; encrypted || block.Type == "ENCRYPTED PRIVATE KEY" {
			return rt.Err[rt.Bytes]("the private key is encrypted; decrypt it first (openssl pkey -in key.pem -out plain.pem)")
		}
		switch block.Type {
		case "PRIVATE KEY":
			return privateFrom(x509.ParsePKCS8PrivateKey(block.Bytes))
		case "RSA PRIVATE KEY":
			return privateFrom(x509.ParsePKCS1PrivateKey(block.Bytes))
		case "EC PRIVATE KEY":
			return privateFrom(x509.ParseECPrivateKey(block.Bytes))
		}
	}
}

// SigPrivateToPEM is `signatures_host.private_to_pem`: der in a PKCS #8
// `PRIVATE KEY` block.
func SigPrivateToPEM(der rt.Bytes) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte(der)}))
}

// SigPrivateKind is `signatures_host.private_kind`: a private key's kind.
func SigPrivateKind(der rt.Bytes) int64 {
	kind, _ := sigKind(parsePrivate(der))
	return kind
}

// SigPublicOf is `signatures_host.public_of`: a private key's public key.
func SigPublicOf(der rt.Bytes) rt.Bytes {
	signer, ok := parsePrivate(der).(crypto.Signer)
	if !ok {
		rt.Trap("signatures: a private key that cannot sign")
	}
	return marshalPublic(signer.Public())
}

// SigPublicFromDER is `signatures_host.public_from_der`: a public key in
// PKIX or PKCS #1 (RSA) DER, as PKIX.
func SigPublicFromDER(der rt.Bytes) rt.Result[rt.Bytes, string] {
	data := []byte(der)
	if key, err := x509.ParsePKIXPublicKey(data); err == nil {
		return publicFrom(key, nil)
	}
	if key, err := x509.ParsePKCS1PublicKey(data); err == nil {
		return publicFrom(key, nil)
	}
	return publicFrom(nil, errors.New("not a PKIX or PKCS #1 public key"))
}

// SigPublicFromPEM is `signatures_host.public_from_pem`: the first public
// key in text, a `PUBLIC KEY` or `RSA PUBLIC KEY` block, as PKIX.
func SigPublicFromPEM(text string) rt.Result[rt.Bytes, string] {
	rest := []byte(text)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return rt.Err[rt.Bytes]("no public key PEM block")
		}
		switch block.Type {
		case "PUBLIC KEY":
			return publicFrom(x509.ParsePKIXPublicKey(block.Bytes))
		case "RSA PUBLIC KEY":
			return publicFrom(x509.ParsePKCS1PublicKey(block.Bytes))
		}
	}
}

// SigPublicToPEM is `signatures_host.public_to_pem`: der in a PKIX
// `PUBLIC KEY` block.
func SigPublicToPEM(der rt.Bytes) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte(der)}))
}

// SigPublicKind is `signatures_host.public_kind`: a public key's kind.
func SigPublicKind(der rt.Bytes) int64 {
	kind, _ := sigKind(parsePublic(der))
	return kind
}

// SigEd25519FromSeed is `signatures_host.ed25519_from_seed`: the Ed25519
// private key whose 32-byte seed (RFC 8032's private key) is seed.
func SigEd25519FromSeed(seed rt.Bytes) rt.Result[rt.Bytes, string] {
	if len(seed) != ed25519.SeedSize {
		return rt.Err[rt.Bytes](fmt.Sprintf("an Ed25519 seed is %d bytes, got %d", ed25519.SeedSize, len(seed)))
	}
	return rt.Ok[rt.Bytes, string](marshalPrivate(ed25519.NewKeyFromSeed([]byte(seed))))
}

// SigEd25519Seed is `signatures_host.ed25519_seed`: an Ed25519 private
// key's 32-byte seed.
func SigEd25519Seed(der rt.Bytes) rt.Bytes {
	key, ok := parsePrivate(der).(ed25519.PrivateKey)
	if !ok {
		rt.Trap("signatures: not an Ed25519 private key")
	}
	return rt.Bytes(key.Seed())
}

// SigEd25519PublicFromBytes is `signatures_host.ed25519_public_from_bytes`:
// the Ed25519 public key whose 32-byte encoding is raw.
func SigEd25519PublicFromBytes(raw rt.Bytes) rt.Result[rt.Bytes, string] {
	if len(raw) != ed25519.PublicKeySize {
		return rt.Err[rt.Bytes](fmt.Sprintf("an Ed25519 public key is %d bytes, got %d", ed25519.PublicKeySize, len(raw)))
	}
	return rt.Ok[rt.Bytes, string](marshalPublic(ed25519.PublicKey([]byte(raw))))
}

// SigEd25519PublicBytes is `signatures_host.ed25519_public_bytes`: an
// Ed25519 public key's 32-byte encoding.
func SigEd25519PublicBytes(der rt.Bytes) rt.Bytes {
	key, ok := parsePublic(der).(ed25519.PublicKey)
	if !ok {
		rt.Trap("signatures: not an Ed25519 public key")
	}
	return rt.Bytes(key)
}

// ecdsaSignature is an ECDSA signature's ASN.1 form.
type ecdsaSignature struct{ R, S *big.Int }

// SigSign is `signatures_host.sign`: message signed under the PKCS #8 key
// der with scheme. std/signatures passes only a scheme the key's kind has.
func SigSign(der rt.Bytes, scheme int64, message rt.Bytes) rt.Bytes {
	key := parsePrivate(der)
	digest := sha256.Sum256([]byte(message))
	var sig []byte
	var err error
	switch k := key.(type) {
	case ed25519.PrivateKey:
		if scheme != sigEd25519 {
			break
		}
		return rt.Bytes(ed25519.Sign(k, []byte(message)))
	case *ecdsa.PrivateKey:
		if scheme != sigES256 && scheme != sigES256DER {
			break
		}
		sig, err = ecdsa.SignASN1(rand.Reader, k, digest[:])
		if err == nil && scheme == sigES256 {
			sig, err = ecdsaFixed(sig)
		}
	case *rsa.PrivateKey:
		switch scheme {
		case sigRS256:
			sig, err = rsa.SignPKCS1v15(nil, k, crypto.SHA256, digest[:])
		case sigPS256:
			sig, err = rsa.SignPSS(rand.Reader, k, crypto.SHA256, digest[:], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
		}
	}
	if sig == nil && err == nil {
		rt.Trap(fmt.Sprintf("signatures: scheme %d does not sign with a %T", scheme, key))
	}
	if err != nil {
		rt.Trap("signatures: signing: " + err.Error())
	}
	return rt.Bytes(sig)
}

// ecdsaFixed is an ASN.1 P-256 signature as r and s, 32 bytes each.
func ecdsaFixed(der []byte) ([]byte, error) {
	var s ecdsaSignature
	if _, err := asn1.Unmarshal(der, &s); err != nil {
		return nil, err
	}
	out := make([]byte, 64)
	s.R.FillBytes(out[:32])
	s.S.FillBytes(out[32:])
	return out, nil
}

// SigIsValid is `signatures_host.is_valid`: whether signature is message's
// under the PKIX key der with scheme. A signature of the wrong shape, or a
// scheme the key's kind does not have, is False.
func SigIsValid(der rt.Bytes, scheme int64, message, signature rt.Bytes) bool {
	key := parsePublic(der)
	digest := sha256.Sum256([]byte(message))
	sig := []byte(signature)
	switch k := key.(type) {
	case ed25519.PublicKey:
		return scheme == sigEd25519 && ed25519.Verify(k, []byte(message), sig)
	case *ecdsa.PublicKey:
		switch scheme {
		case sigES256:
			if len(sig) != 64 {
				return false
			}
			r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
			return ecdsa.Verify(k, digest[:], r, s)
		case sigES256DER:
			return ecdsa.VerifyASN1(k, digest[:], sig)
		}
	case *rsa.PublicKey:
		switch scheme {
		case sigRS256:
			return rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) == nil
		case sigPS256:
			return rsa.VerifyPSS(k, crypto.SHA256, digest[:], sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthAuto}) == nil
		}
	}
	return false
}
