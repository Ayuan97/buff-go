// Package credential encrypts credentials before persistence.
package credential

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

const (
	// EnvelopeVersion is the only supported credential envelope format.
	EnvelopeVersion uint8 = 1
	keySize               = 32
	maxKeyIDSize          = 64
)

var (
	ErrInvalidKeyID      = errors.New("credential: invalid key id")
	ErrInvalidKey        = errors.New("credential: invalid encryption key")
	ErrInvalidPlaintext  = errors.New("credential: plaintext is empty")
	ErrInvalidAAD        = errors.New("credential: associated data is empty")
	ErrInvalidVersion    = errors.New("credential: unsupported envelope version")
	ErrKeyIDMismatch     = errors.New("credential: envelope key id mismatch")
	ErrInvalidNonce      = errors.New("credential: invalid nonce")
	ErrInvalidCiphertext = errors.New("credential: invalid ciphertext")
	ErrAuthentication    = errors.New("credential: authentication failed")
	ErrRandomSource      = errors.New("credential: random source failed")
)

// Envelope contains the versioned encrypted credential and its nonce.
type Envelope struct {
	Version    uint8
	KeyID      string
	Nonce      []byte
	Ciphertext []byte
}

// Cipher encrypts one envelope key version. It is safe for concurrent use.
type Cipher struct {
	keyID string
	aead  cipher.AEAD
}

// NewCipher creates a cipher from an explicit key identifier and AES-256 key.
// Key identifiers use lowercase ASCII letters, digits, dot, underscore, or
// hyphen, must start with a letter or digit, and may contain at most 64 bytes.
func NewCipher(keyID string, key []byte) (*Cipher, error) {
	if !validKeyID(keyID) {
		return nil, ErrInvalidKeyID
	}
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}

	keyCopy := append([]byte(nil), key...)
	block, err := aes.NewCipher(keyCopy)
	if err != nil {
		return nil, ErrInvalidKey
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, ErrInvalidKey
	}

	return &Cipher{keyID: keyID, aead: aead}, nil
}

// Seal encrypts a non-empty credential and binds it to non-empty associated
// data. A fresh random nonce is generated for every call.
func (c *Cipher) Seal(plaintext, aad []byte) (Envelope, error) {
	if c == nil || c.aead == nil {
		return Envelope{}, ErrInvalidKey
	}
	if len(plaintext) == 0 {
		return Envelope{}, ErrInvalidPlaintext
	}
	if len(aad) == 0 {
		return Envelope{}, ErrInvalidAAD
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, ErrRandomSource
	}
	ciphertext := c.aead.Seal(nil, nonce, plaintext, aad)

	return Envelope{
		Version:    EnvelopeVersion,
		KeyID:      c.keyID,
		Nonce:      nonce,
		Ciphertext: ciphertext,
	}, nil
}

// Open authenticates and decrypts an envelope bound to associated data.
func (c *Cipher) Open(envelope Envelope, aad []byte) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, ErrInvalidKey
	}
	if len(aad) == 0 {
		return nil, ErrInvalidAAD
	}
	if envelope.Version != EnvelopeVersion {
		return nil, ErrInvalidVersion
	}
	if !validKeyID(envelope.KeyID) {
		return nil, ErrInvalidKeyID
	}
	if envelope.KeyID != c.keyID {
		return nil, ErrKeyIDMismatch
	}
	if len(envelope.Nonce) != c.aead.NonceSize() {
		return nil, ErrInvalidNonce
	}
	if len(envelope.Ciphertext) <= c.aead.Overhead() {
		return nil, ErrInvalidCiphertext
	}

	plaintext, err := c.aead.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return nil, ErrAuthentication
	}
	return plaintext, nil
}

func validKeyID(keyID string) bool {
	if keyID == "" || len(keyID) > maxKeyIDSize || !isLowerAlphaNumeric(keyID[0]) {
		return false
	}
	for i := 1; i < len(keyID); i++ {
		if isLowerAlphaNumeric(keyID[i]) {
			continue
		}
		switch keyID[i] {
		case '.', '_', '-':
		default:
			return false
		}
	}
	return true
}

func isLowerAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}
