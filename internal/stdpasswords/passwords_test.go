package stdpasswords

import (
	"encoding/hex"
	"strings"
	"testing"
)

// The reference implementation's Argon2id vectors (phc-winner-argon2's
// test.c, which x/crypto's tests also run): password "password", salt
// "somesalt". RFC 9106's own vector uses a secret and associated data,
// which Argon2id here does not take.
func TestArgon2idReferenceVectors(t *testing.T) {
	for _, tc := range []struct {
		passes, memory uint32
		lanes          uint32
		want           string
	}{
		{2, 65536, 1, "09316115d5cf24ed5a15a31a3ba326e5cf32edc24702987c02b6566f61913cf7"},
		{2, 256, 1, "9dfeb910e80bad0311fee20f9c0e2b12c17987b4cac90c2ef54d5b3021c68bfe"},
		{2, 256, 2, "6d093c501fd5999645e0ea3bf620d7b8be7fd2db59c20d9fff9539da2bf57037"},
	} {
		got := hex.EncodeToString(Argon2id([]byte("password"), []byte("somesalt"), tc.passes, tc.memory, tc.lanes, 32))
		if got != tc.want {
			t.Errorf("Argon2id(t=%d, m=%d, p=%d) = %s, want %s", tc.passes, tc.memory, tc.lanes, got, tc.want)
		}
	}
}

// RFC 7914, section 12.
func TestScryptRFCVectors(t *testing.T) {
	for _, tc := range []struct {
		password, salt string
		logCost        uint32
		r, p           uint32
		want           string
	}{
		{"", "", 4, 1, 1, "77d6576238657b203b19ca42c18a0497f16b4844e3074ae8dfdffa3fede21442fcd0069ded0948f8326a753a0fc81f17e8d3e0fb2e0d3628cf35e20c38d18906"},
		{"password", "NaCl", 10, 8, 16, "fdbabe1c9d3472007856e7190d01e9fe7c6ad7cbc8237830e77376634b3731622eaf30d92e22a3886ff109279d9830dac727afb94a83ee6d8360cbdfa2cc0640"},
		{"pleaseletmein", "SodiumChloride", 14, 8, 1, "7023bdcb3afd7348461c06cd81fd38ebfda8fbba904f8e3ea9b543f6545da1f2d5432955613f0fcf62d49705242a9af9e61e85dc0d651e40dfcf017b45575887"},
	} {
		key, err := Scrypt([]byte(tc.password), []byte(tc.salt), tc.logCost, tc.r, tc.p, 64)
		if got := hex.EncodeToString(key); got != tc.want || err != nil {
			t.Errorf("Scrypt(%q, N=2^%d) = %s %v, want %s", tc.password, tc.logCost, got, err, tc.want)
		}
	}
	if _, err := Scrypt([]byte("x"), []byte("y"), 4, 1<<15, 1<<15, 32); err == nil {
		t.Error("a block size and parallelism of 2^30 derived a key")
	}
}

// OpenBSD's and John the Ripper's bcrypt vectors, under each prefix bcrypt
// writes.
func TestBcryptVectors(t *testing.T) {
	for _, tc := range []struct{ password, stored string }{
		{"U*U", "$2a$05$CCCCCCCCCCCCCCCCCCCCC.E5YPO9kmyuRGyh0XouQYb4YMJKvyOeW"},
		{"U*U*", "$2a$05$CCCCCCCCCCCCCCCCCCCCC.VGOzA784oUp/Z0DY336zx7pLYAy0lwK"},
		{"", "$2a$05$CCCCCCCCCCCCCCCCCCCCC.7uG0VCzI2bS7j6ymqJi9CdcdxiRTWNy"},
		{"U*U", "$2b$05$CCCCCCCCCCCCCCCCCCCCC.E5YPO9kmyuRGyh0XouQYb4YMJKvyOeW"},
		{"U*U", "$2y$05$CCCCCCCCCCCCCCCCCCCCC.E5YPO9kmyuRGyh0XouQYb4YMJKvyOeW"},
	} {
		if ok, err := BcryptVerify([]byte(tc.password), tc.stored); !ok || err != nil {
			t.Errorf("BcryptVerify(%q, %s) = %v, %v", tc.password, tc.stored, ok, err)
		}
		if ok, err := BcryptVerify([]byte(tc.password+"x"), tc.stored); ok || err != nil {
			t.Errorf("a wrong password verified against %s", tc.stored)
		}
		if cost := BcryptCost(tc.stored); cost == nil || *cost != 5 {
			t.Errorf("BcryptCost(%s) = %v", tc.stored, cost)
		}
	}
	if _, err := BcryptVerify([]byte("x"), "not bcrypt"); err == nil {
		t.Error("a malformed hash gave no reason")
	}
	if cost := BcryptCost("not bcrypt"); cost != nil {
		t.Errorf("BcryptCost of a malformed hash = %d", *cost)
	}
	stored, err := BcryptHash([]byte("hunter2"), 4)
	if err != nil || !strings.HasPrefix(stored, "$2a$04$") {
		t.Fatalf("BcryptHash = %q, %v", stored, err)
	}
	if ok, _ := BcryptVerify([]byte("hunter2"), stored); !ok {
		t.Error("a fresh hash did not verify")
	}
	if _, err := BcryptHash([]byte(strings.Repeat("x", 73)), 4); err == nil {
		t.Error("a 73-byte password hashed")
	}
}
