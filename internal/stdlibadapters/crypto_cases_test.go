package stdlibadapters

import (
	"encoding/hex"
	"strings"

	"github.com/nomi-language/nomi/internal/stdhttp"
	"github.com/nomi-language/nomi/rt"
)

// The keys the signatures_host cases run over: RFC 8032's first Ed25519
// key, a P-256 key from a fixed scalar, and keys that do not parse. A
// freshly generated key, an ECDSA or PSS signature and an RSA key are
// random, so their rows are recorded by shape.
var (
	sigEdSeed = mustHex("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
	sigEdPriv = string(stdhttp.SigEd25519FromSeed(rt.Bytes(sigEdSeed)).Ok)
	sigEdPub  = string(stdhttp.SigPublicOf(rt.Bytes(sigEdPriv)))
	// RFC 7515, appendix A.3's P-256 key.
	sigP256 = string(stdhttp.SigPrivateFromPEM("-----BEGIN PRIVATE KEY-----\n" +
		"MIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQgjpsQnnGQmL+YBIff\n" +
		"H1136cspYG6+0iY7X1fCE9+E9LKhRANCAAR/zc4ncPbEXUGDy+5v20t7WAczNXvp\n" +
		"7xO6z248e9FURcfxRM0bvZt+hyzf7bnuufSzaV1uqQskrYpGIyiFiOWt\n" +
		"-----END PRIVATE KEY-----\n").Ok)
	sigP256Pub = string(stdhttp.SigPublicOf(rt.Bytes(sigP256)))
)

func mustHex(s string) string {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func cryptoCases() map[string][]hostCase {
	edPEM := string(stdhttp.SigPrivateToPEM(rt.Bytes(sigEdPriv)))
	edPubPEM := string(stdhttp.SigPublicToPEM(rt.Bytes(sigEdPub)))
	message := hBytes("hello")
	edSig := string(stdhttp.SigSign(rt.Bytes(sigEdPriv), 0, rt.Bytes("hello")))
	return map[string][]hostCase{
		"hash.pbkdf2_sha256": cases(
			args(hBytes("passwd"), hBytes("salt"), hInt(1), hInt(64)),
			args(hBytes(""), hBytes(""), hInt(2), hInt(16)),
			args(hBytes("p"), hBytes("s"), hInt(0), hInt(32)),
			args(hBytes("p"), hBytes("s"), hInt(1), hInt(0)),
			args(hBytes("p"), hBytes("s"), hInt(1), hInt(1025)),
		),
		"signatures_host.generate_ed25519": {{args: args(), shapeOnly: true}},
		"signatures_host.generate_p256":    {{args: args(), shapeOnly: true}},
		"signatures_host.generate_rsa": {
			{args: args(hInt(1024))},
			{args: args(hInt(8193))},
			{args: args(hInt(2048)), shapeOnly: true},
		},
		"signatures_host.private_from_der": unary(hBytes(sigEdPriv), hBytes(sigP256), hBytes("not a key"), hBytes("")),
		"signatures_host.private_from_pem": unary(hStr(edPEM), hStr(edPubPEM), hStr("hello"),
			hStr("-----BEGIN ENCRYPTED PRIVATE KEY-----\nAAAA\n-----END ENCRYPTED PRIVATE KEY-----\n"),
			hStr("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n")),
		"signatures_host.private_to_pem":    unary(hBytes(sigEdPriv), hBytes(sigP256)),
		"signatures_host.private_kind":      unary(hBytes(sigEdPriv), hBytes(sigP256), hBytes("junk")),
		"signatures_host.public_of":         unary(hBytes(sigEdPriv), hBytes(sigP256), hBytes("junk")),
		"signatures_host.public_from_der":   unary(hBytes(sigEdPub), hBytes(sigP256Pub), hBytes(sigEdPriv), hBytes("")),
		"signatures_host.public_from_pem":   unary(hStr(edPubPEM), hStr(edPEM), hStr("")),
		"signatures_host.public_to_pem":     unary(hBytes(sigEdPub), hBytes(sigP256Pub)),
		"signatures_host.public_kind":       unary(hBytes(sigEdPub), hBytes(sigP256Pub), hBytes("junk")),
		"signatures_host.ed25519_from_seed": unary(hBytes(sigEdSeed), hBytes(sigEdSeed[:31]), hBytes("")),
		"signatures_host.ed25519_seed":      unary(hBytes(sigEdPriv), hBytes(sigP256)),
		"signatures_host.ed25519_public_from_bytes": unary(
			hBytes(mustHex("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")), hBytes("short")),
		"signatures_host.ed25519_public_bytes": unary(hBytes(sigEdPub), hBytes(sigP256Pub)),
		"signatures_host.sign": {
			{args: args(hBytes(sigEdPriv), hInt(0), message)},
			{args: args(hBytes(sigEdPriv), hInt(1), message)},
			{args: args(hBytes(sigP256), hInt(1), message), shapeOnly: true},
			{args: args(hBytes(sigP256), hInt(4), message), shapeOnly: true},
			{args: args(hBytes("junk"), hInt(0), message)},
		},
		"signatures_host.is_valid": cases(
			args(hBytes(sigEdPub), hInt(0), message, hBytes(edSig)),
			args(hBytes(sigEdPub), hInt(0), hBytes("hellO"), hBytes(edSig)),
			args(hBytes(sigEdPub), hInt(1), message, hBytes(edSig)),
			args(hBytes(sigP256Pub), hInt(1), message, hBytes(strings.Repeat("\x00", 64))),
			args(hBytes(sigP256Pub), hInt(4), message, hBytes("")),
			args(hBytes("junk"), hInt(0), message, hBytes(edSig)),
		),
	}
}
