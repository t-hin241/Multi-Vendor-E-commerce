package adapter

import (
	"bytes"
	"testing"
)

func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestDestinationCipherBindsAADAndRotatesKeys(t *testing.T) {
	old, err := NewDestinationCipher(1, map[int][]byte{1: testKey(1)})
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"account_number":"0123456789"}`)
	sealed, version, err := old.Encrypt(plain, "refund_destination:r1:v1")
	if err != nil || version != 1 {
		t.Fatalf("encrypt: %v %d", err, version)
	}
	if bytes.Contains(sealed, []byte("0123456789")) {
		t.Fatal("ciphertext must not contain the account number")
	}
	if _, err := old.Decrypt(sealed, 1, "refund_destination:r2:v1"); err == nil {
		t.Fatal("a ciphertext copied to another refund must not decrypt")
	}

	rotated, err := NewDestinationCipher(2, map[int][]byte{2: testKey(2), 1: testKey(1)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := rotated.Decrypt(sealed, 1, "refund_destination:r1:v1")
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("old rows stay readable after rotation: %v", err)
	}
	if _, version, _ := rotated.Encrypt(plain, "x"); version != 2 {
		t.Fatalf("new rows use the current key, got v%d", version)
	}
	retired, _ := NewDestinationCipher(2, map[int][]byte{2: testKey(2)})
	if _, err := retired.Decrypt(sealed, 1, "refund_destination:r1:v1"); err == nil {
		t.Fatal("a retired key version must not decrypt")
	}
	if _, err := NewDestinationCipher(1, map[int][]byte{1: []byte("short")}); err == nil {
		t.Fatal("keys must be 32 bytes")
	}
	if _, err := NewDestinationCipher(3, map[int][]byte{1: testKey(1)}); err == nil {
		t.Fatal("the current version must be configured")
	}
}
