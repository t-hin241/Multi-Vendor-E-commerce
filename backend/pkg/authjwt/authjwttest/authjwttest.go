// Package authjwttest gives tests a token issuer without real key
// material: the keys are derived from fixed, public strings, so every test
// in every module that calls Manager() issues and verifies the same tokens.
// Never use it outside tests.
package authjwttest

import (
	"crypto/ed25519"
	"crypto/sha256"

	"shopee/backend/pkg/authjwt"
)

func key(label string) authjwt.SigningKey {
	seed := sha256.Sum256([]byte("shopee authjwttest " + label + " (public test key, not a secret)"))
	return authjwt.SigningKey{ID: "test-" + label, Key: ed25519.NewKeyFromSeed(seed[:])}
}

func issuer(label string) *authjwt.Manager {
	m, err := authjwt.NewIssuer(key(label), nil)
	if err != nil {
		panic(err)
	}
	return m
}

// SigningKey is the shared test key itself, for tests that forge tokens
// around it (another algorithm, kid or issuer).
func SigningKey() authjwt.SigningKey { return key("a") }

// Manager issues and verifies with the shared test key.
func Manager() *authjwt.Manager { return issuer("a") }

// OtherManager uses another key: its tokens do not verify with Manager.
func OtherManager() *authjwt.Manager { return issuer("b") }

// Verifier verifies the shared test key's tokens and cannot issue.
func Verifier() *authjwt.Manager {
	k := key("a")
	return authjwt.NewVerifier(authjwt.VerifyKeys{k.ID: k.Key.Public().(ed25519.PublicKey)})
}
