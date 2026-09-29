package adapter

import (
	"crypto/rand"
	"testing"
)

func TestPayoutCipherBindsAccountVersionAndField(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	cipher, err := NewPayoutCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := cipher.Encrypt("TEST ACCOUNT", "vendor:account:1:name")
	if err != nil {
		t.Fatal(err)
	}
	for _, aad := range []string{"other:account:1:name", "vendor:other:1:name", "vendor:account:2:name", "vendor:account:1:number"} {
		if _, err = cipher.Decrypt(ciphertext, aad); err == nil {
			t.Fatal("ciphertext accepted outside its bound context")
		}
	}
	plain, err := cipher.Decrypt(ciphertext, "vendor:account:1:name")
	if err != nil || plain != "TEST ACCOUNT" {
		t.Fatal("round trip failed", err)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err = cipher.Decrypt(ciphertext, "vendor:account:1:name"); err == nil {
		t.Fatal("tampering accepted")
	}
}
