package usecase_test

import (
	"bytes"
	"testing"

	"shopee/backend/services/identity/internal/usecase"
)

func TestResetCipherRejectsTamperingAndWrongUser(t *testing.T) {
	c, err := usecase.NewTokenCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	data, err := c.Encrypt("synthetic-reset-token", "user-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Decrypt(data, "user-b"); err == nil {
		t.Fatal("cross-user decrypt accepted")
	}
	data[len(data)-1] ^= 1
	if _, err = c.Decrypt(data, "user-a"); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}
