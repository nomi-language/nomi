package stdcipher

import (
	"crypto/ecdh"
	"crypto/rand"
	"fmt"
)

// curve is a curve as std/key_exchange's `curve_number` numbers a Curve.
func curve(number int64) ecdh.Curve {
	switch number {
	case 0:
		return ecdh.X25519()
	case 1:
		return ecdh.P256()
	case 2:
		return ecdh.P384()
	case 3:
		return ecdh.P521()
	}
	panic(fmt.Sprintf("no curve %d", number))
}

// Generate is `std::key_exchange::host.generate`: a new private key on the
// curve from the operating system's secure random source, as its scalar.
func Generate(curveNumber int64) []byte {
	key, err := curve(curveNumber).GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return key.Bytes()
}

// PrivateIsValid is `std::key_exchange::host.private_is_valid`: whether
// data is a private key on the curve. Any 32 bytes are an X25519 key; a
// NIST curve's is its order's length, not zero and below the order.
func PrivateIsValid(curveNumber int64, data []byte) bool {
	_, err := curve(curveNumber).NewPrivateKey(data)
	return err == nil
}

// PublicIsValid is `std::key_exchange::host.public_is_valid`: whether data
// is a public key on the curve. Any 32 bytes are an X25519 key; a NIST
// curve's is an uncompressed SEC 1 point on the curve, not the point at
// infinity.
func PublicIsValid(curveNumber int64, data []byte) bool {
	_, err := curve(curveNumber).NewPublicKey(data)
	return err == nil
}

// PublicOf is `std::key_exchange::host.public_of`: the public key of a
// private key PrivateIsValid accepted.
func PublicOf(curveNumber int64, private []byte) []byte {
	return mustPrivate(curveNumber, private).PublicKey().Bytes()
}

// SharedSecret is `std::key_exchange::host.shared_secret`: the
// Diffie-Hellman secret of a private and a public key on the curve, or nil
// when the exchange gives none, which only an X25519 public key of low
// order does (its secret would be all zeros).
func SharedSecret(curveNumber int64, private, public []byte) *[]byte {
	c := curve(curveNumber)
	remote, err := c.NewPublicKey(public)
	if err != nil {
		panic(err)
	}
	secret, err := mustPrivate(curveNumber, private).ECDH(remote)
	if err != nil {
		return nil
	}
	return &secret
}

func mustPrivate(curveNumber int64, data []byte) *ecdh.PrivateKey {
	key, err := curve(curveNumber).NewPrivateKey(data)
	if err != nil {
		panic(err)
	}
	return key
}
