package adapter

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// DestinationCipher encrypts refund destinations with AES-256-GCM (AF-06).
// New ciphertext uses the current key version; older versions stay
// readable while their keys are still configured, so a key can rotate
// without rewriting stored rows.
type DestinationCipher struct {
	current int
	keys    map[int]cipher.AEAD
}

// NewDestinationCipher takes the current version and every configured key
// (32 bytes each), the current one included.
func NewDestinationCipher(current int, keys map[int][]byte) (*DestinationCipher, error) {
	if _, ok := keys[current]; !ok {
		return nil, fmt.Errorf("refund destination key version %d is not configured", current)
	}
	c := &DestinationCipher{current: current, keys: map[int]cipher.AEAD{}}
	for version, key := range keys {
		if version < 1 || len(key) != 32 {
			return nil, fmt.Errorf("refund destination key version %d must be a 32-byte key", version)
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
		c.keys[version] = aead
	}
	return c, nil
}

// Encrypt seals plaintext bound to aad; it returns the key version used.
func (c *DestinationCipher) Encrypt(plain []byte, aad string) ([]byte, int, error) {
	aead := c.keys[c.current]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, 0, err
	}
	return aead.Seal(nonce, nonce, plain, []byte(aad)), c.current, nil
}

var errDestinationCiphertext = errors.New("refund destination cannot be decrypted")

// Decrypt opens a ciphertext written with keyVersion for aad.
func (c *DestinationCipher) Decrypt(ciphertext []byte, keyVersion int, aad string) ([]byte, error) {
	aead, ok := c.keys[keyVersion]
	if !ok {
		return nil, fmt.Errorf("refund destination key version %d is not configured", keyVersion)
	}
	n := aead.NonceSize()
	if len(ciphertext) < n {
		return nil, errDestinationCiphertext
	}
	plain, err := aead.Open(nil, ciphertext[:n], ciphertext[n:], []byte(aad))
	if err != nil {
		return nil, errDestinationCiphertext
	}
	return plain, nil
}
