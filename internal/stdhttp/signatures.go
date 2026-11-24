package stdhttp

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	_ "crypto/sha256" // the hashes the schemes name
	_ "crypto/sha512"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"

	"github.com/nomi-language/nomi/rt"
)

// std/signatures/host: Ed25519, ECDSA and RSA keys and signatures over
// Go's crypto packages. The key encodings are crypto/x509's, which links
// net, so these sit with the network adapters (the internal/hostgen/stdtable
// split): a program that never touches a key runs on the runner without
// crypto/x509.
//
// A key crosses as DER: a private key as PKCS #8, a public key as PKIX
// (SubjectPublicKeyInfo). Every function that takes one takes DER that
// SigPrivateFromDER or SigPublicFromDER accepted, which std/signatures'
// opaque key types guarantee, so a key that does not parse is a trap.
//
// Two tables say what std/signatures offers: sigKinds is its KeyKind and
// sigSchemes its Scheme, each in declaration order, so a variant's code is
// its position in both (std/signatures.nomi's `kind_code` and
// `scheme_code`). Signing and checking read the scheme's row: the kind of
// key it takes, the hash it signs, and the form of its signature.

// sigKindRow is one KeyKind: its variant's name and, for ECDSA, its curve.
type sigKindRow struct {
	variant string
	curve   elliptic.Curve
}

var sigKinds = [...]sigKindRow{
	{"Ed25519", nil},
	{"EcdsaP256", elliptic.P256()},
	{"EcdsaP384", elliptic.P384()},
	{"EcdsaP521", elliptic.P521()},
	{"Rsa", nil},
}

// The kinds a switch on a key's Go type names.
const (
	sigKindEd25519 = 0
	sigKindRSA     = 4
)

// A signature's form: how a scheme's row signs and checks.
const (
	formEd25519    = iota // Ed25519 over the message itself
	formECDSAFixed        // r and s, each the curve's size, as JWS writes them
	formECDSADER          // ASN.1 DER, as X.509 and OpenSSL write it
	formPKCS1             // RSA PKCS #1 v1.5
	formPSS               // RSA PSS, MGF1 over the same hash, the salt the hash's size
)

// sigSchemeRow is one Scheme: its variant's name, the key kind it signs
// with, the hash it signs (none for Ed25519), and its form.
type sigSchemeRow struct {
	variant string
	kind    int64
	hash    crypto.Hash
	form    int
}

var sigSchemes = [...]sigSchemeRow{
	{"Ed25519", sigKindEd25519, 0, formEd25519},
	{"EcdsaSha256", 1, crypto.SHA256, formECDSAFixed},
	{"EcdsaSha256Der", 1, crypto.SHA256, formECDSADER},
	{"EcdsaSha384", 2, crypto.SHA384, formECDSAFixed},
	{"EcdsaSha384Der", 2, crypto.SHA384, formECDSADER},
	{"EcdsaSha512", 3, crypto.SHA512, formECDSAFixed},
	{"EcdsaSha512Der", 3, crypto.SHA512, formECDSADER},
	{"RsaPkcs1Sha256", sigKindRSA, crypto.SHA256, formPKCS1},
	{"RsaPkcs1Sha384", sigKindRSA, crypto.SHA384, formPKCS1},
	{"RsaPkcs1Sha512", sigKindRSA, crypto.SHA512, formPKCS1},
	{"RsaPssSha256", sigKindRSA, crypto.SHA256, formPSS},
	{"RsaPssSha384", sigKindRSA, crypto.SHA384, formPSS},
	{"RsaPssSha512", sigKindRSA, crypto.SHA512, formPSS},
}

// sigEncryptions is std/signatures' Encryption in declaration order: each
// is RSA-OAEP (RFC 8017) with one hash for its digest and its MGF1.
var sigEncryptions = [...]struct {
	variant string
	hash    crypto.Hash
}{
	{"RsaOaepSha256", crypto.SHA256},
	{"RsaOaepSha384", crypto.SHA384},
	{"RsaOaepSha512", crypto.SHA512},
}

// sigEncryption is the hash of encryption, a code std/signatures.nomi's
// `encryption_code` answers and so always in the table.
func sigEncryption(encryption int64) crypto.Hash {
	if encryption < 0 || encryption >= int64(len(sigEncryptions)) {
		panic(fmt.Sprintf("signatures: no encryption has code %d", encryption))
	}
	return sigEncryptions[encryption].hash
}

// SigEncrypt is `std::signatures::host.encrypt`: plaintext encrypted to the
// RSA public key der with RSA-OAEP, label bound to it, or why it is too long
// for the key and the hash. std/signatures passes only an RSA key.
func SigEncrypt(der rt.Bytes, encryption int64, plaintext, label rt.Bytes) rt.Result[rt.Bytes, string] {
	h := sigEncryption(encryption)
	key := parsePublic(der).(*rsa.PublicKey)
	most := key.Size() - 2*h.Size() - 2
	if len(plaintext) > most {
		return rt.Err[rt.Bytes](fmt.Sprintf("RSA-OAEP with this key and %s encrypts at most %d bytes, got %d",
			sigEncryptions[encryption].variant, max(most, 0), len(plaintext)))
	}
	out, err := rsa.EncryptOAEP(h.New(), rand.Reader, key, []byte(plaintext), []byte(label))
	if err != nil {
		rt.Trap("signatures: encrypting: " + err.Error())
	}
	return rt.Ok[rt.Bytes, string](rt.Bytes(out))
}

// SigDecrypt is `std::signatures::host.decrypt`: the plaintext ciphertext
// holds under the RSA private key der with RSA-OAEP and label, or None.
// Every cause of a failure (a ciphertext of the wrong length, a value past
// the modulus, a bad padding, another label) is the one None, and Go's
// rsa.DecryptOAEP checks the padding in constant time, so a failure tells
// nothing about the key or the plaintext. std/signatures passes only an
// RSA key.
func SigDecrypt(der rt.Bytes, encryption int64, ciphertext, label rt.Bytes) rt.Option[rt.Bytes] {
	h := sigEncryption(encryption)
	key := parsePrivate(der).(*rsa.PrivateKey)
	out, err := rsa.DecryptOAEP(h.New(), nil, key, []byte(ciphertext), []byte(label))
	if err != nil {
		return rt.None[rt.Bytes]()
	}
	return rt.Some(rt.Bytes(out))
}

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
		return curveKind(k.Curve)
	case *ecdsa.PublicKey:
		return curveKind(k.Curve)
	case *rsa.PrivateKey, *rsa.PublicKey:
		return sigKindRSA, nil
	}
	return -1, fmt.Errorf("unsupported key type %T", key)
}

// curveKind is the kind of an ECDSA key over c.
func curveKind(c elliptic.Curve) (int64, error) {
	for i, k := range sigKinds {
		if k.curve != nil && k.curve == c {
			return int64(i), nil
		}
	}
	return -1, fmt.Errorf("unsupported ECDSA curve %s; P-256, P-384 and P-521 are supported", c.Params().Name)
}

// sigScheme is the row of scheme, a code std/signatures.nomi's
// `scheme_code` answers and so always in the table.
func sigScheme(scheme int64) sigSchemeRow {
	if scheme < 0 || scheme >= int64(len(sigSchemes)) {
		panic(fmt.Sprintf("signatures: no scheme has code %d", scheme))
	}
	return sigSchemes[scheme]
}

// SigSchemeName is `std::signatures::host.scheme_name`: a scheme's variant
// name.
func SigSchemeName(scheme int64) string { return sigScheme(scheme).variant }

// SigSchemeKind is `std::signatures::host.scheme_kind`: the kind of key a
// scheme signs with.
func SigSchemeKind(scheme int64) int64 { return sigScheme(scheme).kind }

// SigGenerateEd25519 is `std::signatures::host.generate_ed25519`: a new Ed25519
// private key from crypto/rand.
func SigGenerateEd25519() rt.Bytes {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		rt.Trap("signatures: generating an Ed25519 key: " + err.Error())
	}
	return marshalPrivate(key)
}

// SigGenerateECDSA is `std::signatures::host.generate_ecdsa`: a new ECDSA
// private key of kind, an ECDSA kind, from crypto/rand.
func SigGenerateECDSA(kind int64) rt.Bytes {
	curve := sigKinds[kind].curve
	if curve == nil {
		panic(fmt.Sprintf("signatures: kind %d is not ECDSA", kind))
	}
	key, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		rt.Trap("signatures: generating an " + sigKinds[kind].variant + " key: " + err.Error())
	}
	return marshalPrivate(key)
}

// SigGenerateRSA is `std::signatures::host.generate_rsa`: a new RSA private key
// of bits bits from crypto/rand, or why bits is out of range.
func SigGenerateRSA(bits rt.Int) rt.Result[rt.Bytes, string] {
	// A count past int64 saturates, and is out of range as the bound is.
	n := bits.Saturated()
	if n < sigRSAMinBits || n > sigRSAMaxBits {
		return rt.Err[rt.Bytes](fmt.Sprintf("an RSA key has %d to %d bits, got %s", sigRSAMinBits, sigRSAMaxBits, bits))
	}
	key, err := rsa.GenerateKey(rand.Reader, int(n))
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

// SigPrivateFromDER is `std::signatures::host.private_from_der`: a private key
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

// SigPrivateFromPEM is `std::signatures::host.private_from_pem`: the first
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

// SigPrivateToPEM is `std::signatures::host.private_to_pem`: der in a PKCS #8
// `PRIVATE KEY` block.
func SigPrivateToPEM(der rt.Bytes) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte(der)}))
}

// SigPrivateKind is `std::signatures::host.private_kind`: a private key's kind.
func SigPrivateKind(der rt.Bytes) int64 {
	kind, _ := sigKind(parsePrivate(der))
	return kind
}

// SigPublicOf is `std::signatures::host.public_of`: a private key's public key.
func SigPublicOf(der rt.Bytes) rt.Bytes {
	signer, ok := parsePrivate(der).(crypto.Signer)
	if !ok {
		rt.Trap("signatures: a private key that cannot sign")
	}
	return marshalPublic(signer.Public())
}

// SigPublicFromDER is `std::signatures::host.public_from_der`: a public key in
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

// SigPublicFromPEM is `std::signatures::host.public_from_pem`: the first public
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

// SigPublicToPEM is `std::signatures::host.public_to_pem`: der in a PKIX
// `PUBLIC KEY` block.
func SigPublicToPEM(der rt.Bytes) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte(der)}))
}

// SigPublicKind is `std::signatures::host.public_kind`: a public key's kind.
func SigPublicKind(der rt.Bytes) int64 {
	kind, _ := sigKind(parsePublic(der))
	return kind
}

// SigEd25519FromSeed is `std::signatures::host.ed25519_from_seed`: the Ed25519
// private key whose 32-byte seed (RFC 8032's private key) is seed.
func SigEd25519FromSeed(seed rt.Bytes) rt.Result[rt.Bytes, string] {
	if len(seed) != ed25519.SeedSize {
		return rt.Err[rt.Bytes](fmt.Sprintf("an Ed25519 seed is %d bytes, got %d", ed25519.SeedSize, len(seed)))
	}
	return rt.Ok[rt.Bytes, string](marshalPrivate(ed25519.NewKeyFromSeed([]byte(seed))))
}

// SigEd25519Seed is `std::signatures::host.ed25519_seed`: an Ed25519 private
// key's 32-byte seed.
func SigEd25519Seed(der rt.Bytes) rt.Bytes {
	key, ok := parsePrivate(der).(ed25519.PrivateKey)
	if !ok {
		rt.Trap("signatures: not an Ed25519 private key")
	}
	return rt.Bytes(key.Seed())
}

// SigEd25519PublicFromBytes is `std::signatures::host.ed25519_public_from_bytes`:
// the Ed25519 public key whose 32-byte encoding is raw.
func SigEd25519PublicFromBytes(raw rt.Bytes) rt.Result[rt.Bytes, string] {
	if len(raw) != ed25519.PublicKeySize {
		return rt.Err[rt.Bytes](fmt.Sprintf("an Ed25519 public key is %d bytes, got %d", ed25519.PublicKeySize, len(raw)))
	}
	return rt.Ok[rt.Bytes, string](marshalPublic(ed25519.PublicKey([]byte(raw))))
}

// SigEd25519PublicBytes is `std::signatures::host.ed25519_public_bytes`: an
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

// digestOf is message hashed with h.
func digestOf(h crypto.Hash, message rt.Bytes) []byte {
	d := h.New()
	d.Write([]byte(message))
	return d.Sum(nil)
}

// SigSign is `std::signatures::host.sign`: message signed under the PKCS #8
// key der with scheme. std/signatures passes only a scheme of the key's
// kind.
func SigSign(der rt.Bytes, scheme int64, message rt.Bytes) rt.Bytes {
	row := sigScheme(scheme)
	key := parsePrivate(der)
	if kind, _ := sigKind(key); kind != row.kind {
		rt.Trap(fmt.Sprintf("signatures: %s does not sign with a %s key", row.variant, sigKinds[kind].variant))
	}
	var sig []byte
	var err error
	switch row.form {
	case formEd25519:
		sig = ed25519.Sign(key.(ed25519.PrivateKey), []byte(message))
	case formECDSAFixed, formECDSADER:
		k := key.(*ecdsa.PrivateKey)
		sig, err = ecdsa.SignASN1(rand.Reader, k, digestOf(row.hash, message))
		if err == nil && row.form == formECDSAFixed {
			sig, err = ecdsaFixed(sig, ecdsaSize(k.Curve))
		}
	case formPKCS1:
		sig, err = rsa.SignPKCS1v15(nil, key.(*rsa.PrivateKey), row.hash, digestOf(row.hash, message))
	case formPSS:
		sig, err = rsa.SignPSS(rand.Reader, key.(*rsa.PrivateKey), row.hash, digestOf(row.hash, message),
			&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	}
	if err != nil {
		rt.Trap("signatures: signing: " + err.Error())
	}
	return rt.Bytes(sig)
}

// ecdsaSize is the size of r and of s in a fixed-form signature over c:
// the curve's order in whole bytes (32, 48 and 66).
func ecdsaSize(c elliptic.Curve) int { return (c.Params().N.BitLen() + 7) / 8 }

// ecdsaFixed is an ASN.1 ECDSA signature as r and s, size bytes each.
func ecdsaFixed(der []byte, size int) ([]byte, error) {
	var s ecdsaSignature
	if _, err := asn1.Unmarshal(der, &s); err != nil {
		return nil, err
	}
	out := make([]byte, 2*size)
	s.R.FillBytes(out[:size])
	s.S.FillBytes(out[size:])
	return out, nil
}

// SigIsValid is `std::signatures::host.is_valid`: whether signature is
// message's under the PKIX key der with scheme. A signature of the wrong
// shape, or a scheme of another kind than the key's, is False.
func SigIsValid(der rt.Bytes, scheme int64, message, signature rt.Bytes) bool {
	row := sigScheme(scheme)
	key := parsePublic(der)
	if kind, _ := sigKind(key); kind != row.kind {
		return false
	}
	sig := []byte(signature)
	switch row.form {
	case formEd25519:
		return ed25519.Verify(key.(ed25519.PublicKey), []byte(message), sig)
	case formECDSAFixed:
		k := key.(*ecdsa.PublicKey)
		size := ecdsaSize(k.Curve)
		if len(sig) != 2*size {
			return false
		}
		r, s := new(big.Int).SetBytes(sig[:size]), new(big.Int).SetBytes(sig[size:])
		return ecdsa.Verify(k, digestOf(row.hash, message), r, s)
	case formECDSADER:
		return ecdsa.VerifyASN1(key.(*ecdsa.PublicKey), digestOf(row.hash, message), sig)
	case formPKCS1:
		return rsa.VerifyPKCS1v15(key.(*rsa.PublicKey), row.hash, digestOf(row.hash, message), sig) == nil
	case formPSS:
		return rsa.VerifyPSS(key.(*rsa.PublicKey), row.hash, digestOf(row.hash, message), sig,
			&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthAuto}) == nil
	}
	return false
}
