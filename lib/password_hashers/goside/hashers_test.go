package goside

import (
	"encoding/hex"
	"testing"
)

// The reference implementation's Argon2id test (phc-winner-argon2's
// test.c): password "password", salt "somesalt", t=2, m=65536 KiB, p=1.
func TestArgon2idReference(t *testing.T) {
	got := hex.EncodeToString(Argon2id([]byte("password"), []byte("somesalt"), 2, 65536, 1, 32))
	want := "09316115d5cf24ed5a15a31a3ba326e5cf32edc24702987c02b6566f61913cf7"
	if got != want {
		t.Errorf("Argon2id = %s, want %s", got, want)
	}
}

// OpenBSD's and John the Ripper's bcrypt vector "U*U".
func TestBcryptVector(t *testing.T) {
	ok, why := BcryptVerify([]byte("U*U"), "$2a$05$CCCCCCCCCCCCCCCCCCCCC.E5YPO9kmyuRGyh0XouQYb4YMJKvyOeW")
	if !ok || why != "" {
		t.Errorf("BcryptVerify = %v, %q", ok, why)
	}
	if ok, _ := BcryptVerify([]byte("U*V"), "$2a$05$CCCCCCCCCCCCCCCCCCCCC.E5YPO9kmyuRGyh0XouQYb4YMJKvyOeW"); ok {
		t.Error("a wrong password verified")
	}
	if _, why := BcryptVerify([]byte("x"), "not bcrypt"); why == "" {
		t.Error("a malformed hash gave no reason")
	}
	if cost := BcryptCost("$2a$05$CCCCCCCCCCCCCCCCCCCCC.E5YPO9kmyuRGyh0XouQYb4YMJKvyOeW"); cost != 5 {
		t.Errorf("BcryptCost = %d", cost)
	}
	if _, why := BcryptHash(make([]byte, 73), 4); why == "" {
		t.Error("a 73-byte password hashed")
	}
}
