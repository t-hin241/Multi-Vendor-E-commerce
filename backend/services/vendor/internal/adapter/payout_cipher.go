package adapter

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

type PayoutCipher struct{ aead cipher.AEAD }

func NewPayoutCipher(key []byte) (*PayoutCipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("payout encryption requires a 32-byte key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &PayoutCipher{aead: aead}, nil
}
func (c *PayoutCipher) Encrypt(value, aad string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(value), []byte(aad)), nil
}
func (c *PayoutCipher) Decrypt(value []byte, aad string) (string, error) {
	n := c.aead.NonceSize()
	if len(value) < n {
		return "", fmt.Errorf("invalid payout ciphertext")
	}
	plain, err := c.aead.Open(nil, value[:n], value[n:], []byte(aad))
	if err != nil {
		return "", fmt.Errorf("payout decryption failed")
	}
	return string(plain), nil
}
