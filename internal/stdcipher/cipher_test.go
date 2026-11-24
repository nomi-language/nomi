package stdcipher

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Published vectors for each AEAD: AES-GCM from the Go standard library's
// own tests (crypto/cipher/gcm_test.go, key=16 and key=32 with a 16-byte
// plaintext), ChaCha20-Poly1305 from RFC 8439, section 2.8.2, and
// XChaCha20-Poly1305 from draft-irtf-cfrg-xchacha, appendix A.3.1, and
// AES-CBC with HMAC from RFC 7518, appendix B.1, B.2 and B.3. Each
// seals to the published ciphertext and tag, opens back, and does not open
// with any one byte of the sealed bytes, the nonce or the associated data
// changed.
func TestAEADKnownAnswers(t *testing.T) {
	sunscreen := hex.EncodeToString([]byte("Ladies and Gentlemen of the class of '99: " +
		"If I could offer you only one tip for the future, sunscreen would be it."))
	kerckhoffs := hex.EncodeToString([]byte("A cipher system must not be required to be secret, " +
		"and it must be able to fall into the hands of the enemy without inconvenience"))
	kerckhoffsAD := hex.EncodeToString([]byte("The second principle of Auguste Kerckhoffs"))
	const kerckhoffsIV = "1af38c2dc2b96ffdd86694092341bc04"
	for _, c := range []struct {
		name                                   string
		engine                                 int64
		key, nonce, plaintext, associated, out string
	}{
		{"AES-128-GCM", engineAESGCM, "ab72c77b97cb5fe9a382d9fe81ffdbed", "54cc7dc2c37ec006bcc6d1da",
			"007c5e5b3e59df24a7c355584fc1518d", "",
			"0e1bde206a07a9c2c1b65300f8c649972b4401346697138c7a4891ee59867d0c"},
		{"AES-256-GCM", engineAESGCM, "feffe9928665731c6d6a8f9467308308feffe9928665731c6d6a8f9467308308",
			"54cc7dc2c37ec006bcc6d1da", "007c5e5b3e59df24a7c355584fc1518d", "",
			"d50b9e252b70945d4240d351677eb10f937cdaef6f2822b6a3191654ba41b197"},
		{"ChaCha20-Poly1305", engineChaCha20Poly1305,
			"808182838485868788898a8b8c8d8e8f909192939495969798999a9b9c9d9e9f", "070000004041424344454647",
			sunscreen, "50515253c0c1c2c3c4c5c6c7",
			"d31a8d34648e60db7b86afbc53ef7ec2a4aded51296e08fea9e2b5a736ee62d63dbea45e8ca9671282fafb69da92728b" +
				"1a71de0a9e060b2905d6a5b67ecd3b3692ddbd7f2d778b8c9803aee328091b58fab324e4fad675945585808b4831d7bc" +
				"3ff4def08e4b7a9de576d26586cec64b6116" + "1ae10b594f09e26a7e902ecbd0600691"},
		{"XChaCha20-Poly1305", engineXChaCha20Poly1305,
			"808182838485868788898a8b8c8d8e8f909192939495969798999a9b9c9d9e9f",
			"404142434445464748494a4b4c4d4e4f5051525354555657", sunscreen, "50515253c0c1c2c3c4c5c6c7",
			"bd6d179d3e83d43b9576579493c0e939572a1700252bfaccbed2902c21396cbb731c7f1b0b4aa6440bf3a82f4eda7e39" +
				"ae64c6708c54c216cb96b72e1213b4522f8c9ba40db5d945b11b69b982c1bb9e3f3fac2bc369488f76b2383565d3fff9" +
				"21f9664c97637da9768812f615c68b13b52ec0875924c1c7987947deafd8780acf49"},
		{"AES_128_CBC_HMAC_SHA_256", engineAESCBCHMAC, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
			kerckhoffsIV, kerckhoffs, kerckhoffsAD,
			"c80edfa32ddf39d5ef00c0b468834279a2e46a1b8049f792f76bfe54b903a9c9a94ac9b47ad2655c5f10f9aef71427e2" +
				"fc6f9b3f399a221489f16362c703233609d45ac69864e3321cf82935ac4096c86e133314c54019e8ca7980dfa4b9cf1b" +
				"384c486f3a54c51078158ee5d79de59fbd34d848b3d69550a67646344427ade54b8851ffb598f7f80074b9473c82e2db" + "652c3fa36b0a7c5b3219fab3a30bc1c4"},
		{"AES_192_CBC_HMAC_SHA_384", engineAESCBCHMAC, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f",
			kerckhoffsIV, kerckhoffs, kerckhoffsAD,
			"ea65da6b59e61edb419be62d19712ae5d303eeb50052d0dfd6697f77224c8edb000d279bdc14c1072654bd30944230c6" +
				"57bed4ca0c9f4a8466f22b226d1746214bf8cfc2400add9f5126e479663fc90b3bed787a2f0ffcbf3904be2a641d5c21" +
				"05bfe591bae23b1d7449e532eef60a9ac8bb6c6b01d35d49787bcd57ef484927f280adc91ac0c4e79c7b11efc60054e3" + "8490ac0e58949bfe51875d733f93ac2075168039ccc733d7"},
		{"AES_256_CBC_HMAC_SHA_512", engineAESCBCHMAC, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f",
			kerckhoffsIV, kerckhoffs, kerckhoffsAD,
			"4affaaadb78c31c5da4b1b590d10ffbd3dd8d5d302423526912da037ecbcc7bd822c301dd67c373bccb584ad3e9279c2" +
				"e6d12a1374b77f077553df829410446b36ebd97066296ae6427ea75c2e0846a11a09ccf5370dc80bfecbad28c73f09b3" +
				"a3b75e662a2594410ae496b2e2e6609e31e6e02cc837f053d21f37ff4f51950bbe2638d09dd7a4930930806d0703b1f6" + "4dd3b4c088a7f45c216839645b2012bf2e6269a8c56a816dbc1b267761955bc5"},
	} {
		t.Run(c.name, func(t *testing.T) {
			key, nonce := unhex(t, c.key), unhex(t, c.nonce)
			plaintext, associated, want := unhex(t, c.plaintext), unhex(t, c.associated), unhex(t, c.out)
			sealed := Seal(c.engine, key, nonce, plaintext, associated)
			if !bytes.Equal(sealed, want) {
				t.Fatalf("sealed %x, want %x", sealed, want)
			}
			got := Open(c.engine, key, nonce, sealed, associated)
			if got == nil || !bytes.Equal(*got, plaintext) {
				t.Fatalf("opened %v, want %x", got, plaintext)
			}
			for name, part := range map[string][]byte{"sealed bytes": sealed, "nonce": nonce, "associated data": associated} {
				for i := range part {
					part[i] ^= 1
					if Open(c.engine, key, nonce, sealed, associated) != nil {
						t.Errorf("opened with byte %d of the %s flipped", i, name)
					}
					part[i] ^= 1
				}
			}
		})
	}
}

// An empty plaintext opens as empty bytes, not as nothing.
func TestOpenOfAnEmptyPlaintextIsEmpty(t *testing.T) {
	key := bytes.Repeat([]byte("k"), 32)
	nonce := bytes.Repeat([]byte("n"), 24)
	got := Open(engineXChaCha20Poly1305, key, nonce, Seal(engineXChaCha20Poly1305, key, nonce, nil, nil), nil)
	if got == nil || len(*got) != 0 {
		t.Fatalf("opened %v, want empty bytes", got)
	}
}

// NIST SP 800-38A, F.5.1, F.5.3 and F.5.5: CTR-AES128, AES192 and AES256
// over the four blocks of its example, as Go's crypto/cipher tests run them.
func TestAESCTRKnownAnswers(t *testing.T) {
	const (
		counter = "f0f1f2f3f4f5f6f7f8f9fafbfcfdfeff"
		input   = "6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e5130c81c46a35ce411e5fbc1191a0a52eff69f2445df4f9b17ad2b417be66c3710"
	)
	for _, c := range []struct{ key, out string }{
		{"2b7e151628aed2a6abf7158809cf4f3c",
			"874d6191b620e3261bef6864990db6ce9806f66b7970fdff8617187bb9fffdff5ae4df3edbd5d35e5b4f09020db03eab1e031dda2fbe03d1792170a0f3009cee"},
		{"8e73b0f7da0e6452c810f32b809079e562f8ead2522c6b7b",
			"1abc932417521ca24f2b0459fe7e6e0b090339ec0aa6faefd5ccc2c6f4ce8e941e36b26bd1ebc670d1bd1d665620abf74f78a7f6d29809585a97daec58c6b050"},
		{"603deb1015ca71be2b73aef0857d77811f352c073b6108d72d9810a30914dff4",
			"601ec313775789a5b7a7f504bbf3d228f443e3ca4d62b59aca84e990cacaf5c52b0930daa23de94ce87017ba2d84988ddfc9c58db67aada613c2dd08457941a6"},
	} {
		got := AESCTR(unhex(t, c.key), unhex(t, counter), unhex(t, input))
		if hex.EncodeToString(got) != c.out {
			t.Errorf("AES-CTR with a %d-byte key = %x, want %s", len(c.key)/2, got, c.out)
		}
		if back := AESCTR(unhex(t, c.key), unhex(t, counter), got); hex.EncodeToString(back) != input {
			t.Errorf("AES-CTR with a %d-byte key does not decrypt what it encrypted", len(c.key)/2)
		}
	}
}

// RFC 7748, section 6.1, and NIST CAVS 14.1's ECC CDH primitive vectors
// for P-256, P-384 and P-521, as Go's crypto/ecdh tests hold them: each
// private key gives its public key, and the pair agrees on the published
// secret.
func TestKeyExchangeKnownAnswers(t *testing.T) {
	for _, c := range []struct {
		name                          string
		curve                         int64
		private, public, peer, shared string
	}{
		{"X25519", 0, "77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a",
			"8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a",
			"de9edb7d7b7dc1b4d35b61c2ece435373f8343c85b78674dadfc7e146f882b4f",
			"4a5d9d5ba4ce2de1728e3bf480350f25e07e21c947d19e3376f09b3c1e161742"},
		{"P-256", 1, "7d7dc5f71eb29ddaf80d6214632eeae03d9058af1fb6d22ed80badb62bc1a534",
			"04ead218590119e8876b29146ff89ca61770c4edbbf97d38ce385ed281d8a6b230" +
				"28af61281fd35e2fa7002523acc85a429cb06ee6648325389f59edfce1405141",
			"04700c48f77f56584c5cc632ca65640db91b6bacce3a4df6b42ce7cc838833d287" +
				"db71e509e3fd9b060ddb20ba5c51dcc5948d46fbf640dfe0441782cab85fa4ac",
			"46fc62106420ff012e54a434fbdd2d25ccc5852060561e68040dd7778997bd7b"},
		{"P-384", 2, "3cc3122a68f0d95027ad38c067916ba0eb8c38894d22e1b15618b6818a661774ad463b205da88cf699ab4d43c9cf98a1",
			"049803807f2f6d2fd966cdd0290bd410c0190352fbec7ff6247de1302df86f25d34fe4a97bef60cff548355c015dbb3e5f" +
				"ba26ca69ec2f5b5d9dad20cc9da711383a9dbe34ea3fa5a2af75b46502629ad54dd8b7d73a8abb06a3a3be47d650cc99",
			"04a7c76b970c3b5fe8b05d2838ae04ab47697b9eaf52e764592efda27fe7513272734466b400091adbf2d68c58e0c50066" +
				"ac68f19f2e1cb879aed43a9969b91a0839c4c38a49749b661efedf243451915ed0905a32b060992b468c64766fc8437a",
			"5f9d29dc5e31a163060356213669c8ce132e22f57c9a04f40ba7fcead493b457e5621e766c40a2e3d4d6a04b25e533f1"},
		{"P-521", 3, "017eecc07ab4b329068fba65e56a1f8890aa935e57134ae0ffcce802735151f4eac6564f6ee9974c5e6887a1fefee5743ae2241bfeb95d5ce31ddcb6f9edb4d6fc47",
			"0400602f9d0cf9e526b29e22381c203c48a886c2b0673033366314f1ffbcba240ba42f4ef38a76174635f91e6b4ed34275eb01c8467d05ca80315bf1a7bbd945f550a5" +
				"01b7c85f26f5d4b2d7355cf6b02117659943762b6d1db5ab4f1dbc44ce7b2946eb6c7de342962893fd387d1b73d7a8672d1f236961170b7eb3579953ee5cdc88cd2d",
			"0400685a48e86c79f0f0875f7bc18d25eb5fc8c0b07e5da4f4370f3a9490340854334b1e1b87fa395464c60626124a4e70d0f785601d37c09870ebf176666877a2046d" +
				"01ba52c56fc8776d9e8f5db4f0cc27636d0b741bbe05400697942e80b739884a83bde99e0f6716939e632bc8986fa18dccd443a348b6c3e522497955a4f3c302f676",
			"005fc70477c3e63bc3954bd0df3ea0d1f41ee21746ed95fc5e1fdf90930d5e136672d72cc770742d1711c3c3a4c334a0ad9759436a4d3c5bf6e74b9578fac148c831"},
	} {
		t.Run(c.name, func(t *testing.T) {
			private, peer := unhex(t, c.private), unhex(t, c.peer)
			if !PrivateIsValid(c.curve, private) || !PublicIsValid(c.curve, peer) {
				t.Fatal("a published key is refused")
			}
			if got := hex.EncodeToString(PublicOf(c.curve, private)); got != c.public {
				t.Errorf("public key %s, want %s", got, c.public)
			}
			got := SharedSecret(c.curve, private, peer)
			if got == nil || hex.EncodeToString(*got) != c.shared {
				t.Errorf("shared secret %v, want %s", got, c.shared)
			}
			fresh := Generate(c.curve)
			if !PrivateIsValid(c.curve, fresh) || len(fresh) != len(private) {
				t.Errorf("a generated key of %d bytes is not a key of the curve", len(fresh))
			}
		})
	}
}

// Keys a curve refuses, and an X25519 point of low order, whose secret
// would be all zeros: the u-coordinates 0 and 1 (RFC 7748, section 6.1's
// check). A NIST curve's zero scalar, a scalar at its order, the point at
// infinity and a compressed point are refused when read.
func TestKeyExchangeRefusals(t *testing.T) {
	x25519 := unhex(t, "77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a")
	for _, low := range []string{
		"0000000000000000000000000000000000000000000000000000000000000000",
		"0100000000000000000000000000000000000000000000000000000000000000",
	} {
		if !PublicIsValid(0, unhex(t, low)) {
			t.Fatalf("X25519 refuses the 32 bytes %s; it reads any", low)
		}
		if got := SharedSecret(0, x25519, unhex(t, low)); got != nil {
			t.Errorf("X25519 with the low-order point %s agreed on %x", low, *got)
		}
	}
	p256Order := "ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551"
	for _, c := range []struct {
		name  string
		valid func(int64, []byte) bool
		curve int64
		data  string
	}{
		{"a zero P-256 scalar", PrivateIsValid, 1, "0000000000000000000000000000000000000000000000000000000000000000"},
		{"P-256's order as a scalar", PrivateIsValid, 1, p256Order},
		{"a 31-byte P-256 scalar", PrivateIsValid, 1, p256Order[2:]},
		{"a 31-byte X25519 key", PrivateIsValid, 0, "77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c"},
		{"P-256's point at infinity", PublicIsValid, 1, "00"},
		{"a compressed P-256 point", PublicIsValid, 1, "03ead218590119e8876b29146ff89ca61770c4edbbf97d38ce385ed281d8a6b230"},
		{"a P-256 point off the curve", PublicIsValid, 1,
			"04ead218590119e8876b29146ff89ca61770c4edbbf97d38ce385ed281d8a6b230" +
				"28af61281fd35e2fa7002523acc85a429cb06ee6648325389f59edfce1405142"},
	} {
		if c.valid(c.curve, unhex(t, c.data)) {
			t.Errorf("%s is accepted", c.name)
		}
	}
}
