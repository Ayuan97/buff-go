package credential

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestNewCipherValidation(t *testing.T) {
	t.Parallel()

	validKey := bytes.Repeat([]byte{0x2a}, keySize)
	for _, keyID := range []string{"", "Primary", "key id", "-primary", "primary/key", "密钥", strings.Repeat("a", maxKeyIDSize+1)} {
		keyID := keyID
		t.Run(fmt.Sprintf("key-id-%q", keyID), func(t *testing.T) {
			_, err := NewCipher(keyID, validKey)
			if !errors.Is(err, ErrInvalidKeyID) {
				t.Fatalf("NewCipher() error = %v, want %v", err, ErrInvalidKeyID)
			}
		})
	}

	for _, size := range []int{0, keySize - 1, keySize + 1} {
		size := size
		t.Run(fmt.Sprintf("key-size-%d", size), func(t *testing.T) {
			_, err := NewCipher("primary-1", make([]byte, size))
			if !errors.Is(err, ErrInvalidKey) {
				t.Fatalf("NewCipher() error = %v, want %v", err, ErrInvalidKey)
			}
		})
	}

	if _, err := NewCipher("primary.v1_test", validKey); err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	if _, err := NewCipher(strings.Repeat("a", maxKeyIDSize), validKey); err != nil {
		t.Fatalf("NewCipher(maximum key id) error = %v", err)
	}
}

func TestNilCipherReturnsControlledError(t *testing.T) {
	t.Parallel()

	var cipher *Cipher
	if _, err := cipher.Seal([]byte("synthetic-marker"), []byte("resource:1")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("nil Cipher.Seal() error = %v, want %v", err, ErrInvalidKey)
	}
	if _, err := cipher.Open(Envelope{}, []byte("resource:1")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("nil Cipher.Open() error = %v, want %v", err, ErrInvalidKey)
	}

	zero := &Cipher{}
	if _, err := zero.Seal([]byte("synthetic-marker"), []byte("resource:1")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("zero Cipher.Seal() error = %v, want %v", err, ErrInvalidKey)
	}
	if _, err := zero.Open(Envelope{}, []byte("resource:1")); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("zero Cipher.Open() error = %v, want %v", err, ErrInvalidKey)
	}
}

func TestSealOpenAndInputIsolation(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x31}, keySize)
	cipher, err := NewCipher("primary-1", key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	key[0] ^= 0xff

	want := []byte("synthetic-credential-marker")
	plaintext := append([]byte(nil), want...)
	aad := []byte("account:42:session")
	envelope, err := cipher.Seal(plaintext, aad)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	plaintext[0] ^= 0xff
	aad[0] ^= 0xff

	opened, err := cipher.Open(envelope, []byte("account:42:session"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !bytes.Equal(opened, want) {
		t.Fatalf("Open() = %q, want %q", opened, want)
	}

	opened[0] ^= 0xff
	reopened, err := cipher.Open(envelope, []byte("account:42:session"))
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	if !bytes.Equal(reopened, want) {
		t.Fatalf("second Open() = %q, want %q", reopened, want)
	}
}

func TestSealRejectsEmptyInputs(t *testing.T) {
	t.Parallel()

	cipher := mustCipher(t, "primary-1", 0x41)
	tests := []struct {
		name      string
		plaintext []byte
		aad       []byte
		want      error
	}{
		{name: "nil plaintext", aad: []byte("resource:1"), want: ErrInvalidPlaintext},
		{name: "empty plaintext", plaintext: []byte{}, aad: []byte("resource:1"), want: ErrInvalidPlaintext},
		{name: "nil aad", plaintext: []byte("marker"), want: ErrInvalidAAD},
		{name: "empty aad", plaintext: []byte("marker"), aad: []byte{}, want: ErrInvalidAAD},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			_, err := cipher.Seal(test.plaintext, test.aad)
			if !errors.Is(err, test.want) {
				t.Fatalf("Seal() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	t.Parallel()

	cipher := mustCipher(t, "primary-1", 0x51)
	plaintext := []byte("synthetic-credential-marker")
	aad := []byte("account:42:session")
	seenNonces := make(map[string]struct{})
	seenCiphertexts := make(map[string]struct{})
	for i := 0; i < 32; i++ {
		envelope, err := cipher.Seal(plaintext, aad)
		if err != nil {
			t.Fatalf("Seal() error = %v", err)
		}
		if len(envelope.Nonce) != 12 {
			t.Fatalf("nonce length = %d, want 12", len(envelope.Nonce))
		}
		if _, exists := seenNonces[string(envelope.Nonce)]; exists {
			t.Fatal("Seal() reused a nonce")
		}
		if _, exists := seenCiphertexts[string(envelope.Ciphertext)]; exists {
			t.Fatal("Seal() produced deterministic ciphertext")
		}
		seenNonces[string(envelope.Nonce)] = struct{}{}
		seenCiphertexts[string(envelope.Ciphertext)] = struct{}{}
	}
}

func TestOpenValidatesEnvelope(t *testing.T) {
	t.Parallel()

	cipher := mustCipher(t, "primary-1", 0x61)
	aad := []byte("account:42:session")
	valid, err := cipher.Seal([]byte("synthetic-credential-marker"), aad)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(Envelope) Envelope
		testAAD []byte
		want    error
	}{
		{name: "empty aad", mutate: identityEnvelope, want: ErrInvalidAAD},
		{name: "version zero", mutate: func(e Envelope) Envelope { e.Version = 0; return e }, testAAD: aad, want: ErrInvalidVersion},
		{name: "version future", mutate: func(e Envelope) Envelope { e.Version = 2; return e }, testAAD: aad, want: ErrInvalidVersion},
		{name: "invalid key id", mutate: func(e Envelope) Envelope { e.KeyID = "PRIMARY"; return e }, testAAD: aad, want: ErrInvalidKeyID},
		{name: "other key id", mutate: func(e Envelope) Envelope { e.KeyID = "secondary-1"; return e }, testAAD: aad, want: ErrKeyIDMismatch},
		{name: "short nonce", mutate: func(e Envelope) Envelope { e.Nonce = make([]byte, 11); return e }, testAAD: aad, want: ErrInvalidNonce},
		{name: "long nonce", mutate: func(e Envelope) Envelope { e.Nonce = make([]byte, 13); return e }, testAAD: aad, want: ErrInvalidNonce},
		{name: "empty ciphertext", mutate: func(e Envelope) Envelope { e.Ciphertext = nil; return e }, testAAD: aad, want: ErrInvalidCiphertext},
		{name: "tag only ciphertext", mutate: func(e Envelope) Envelope { e.Ciphertext = make([]byte, 16); return e }, testAAD: aad, want: ErrInvalidCiphertext},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			_, err := cipher.Open(test.mutate(cloneEnvelope(valid)), test.testAAD)
			if !errors.Is(err, test.want) {
				t.Fatalf("Open() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestOpenRejectsTamperingWrongKeyAndWrongAAD(t *testing.T) {
	t.Parallel()

	cipher := mustCipher(t, "primary-1", 0x71)
	aad := []byte("account:42:session")
	valid, err := cipher.Seal([]byte("synthetic-credential-marker"), aad)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	tamperedNonce := cloneEnvelope(valid)
	tamperedNonce.Nonce[0] ^= 0xff
	tamperedCiphertext := cloneEnvelope(valid)
	tamperedCiphertext.Ciphertext[0] ^= 0xff
	wrongKeyCipher := mustCipher(t, "primary-1", 0x72)

	tests := []struct {
		name     string
		cipher   *Cipher
		envelope Envelope
		aad      []byte
	}{
		{name: "tampered nonce", cipher: cipher, envelope: tamperedNonce, aad: aad},
		{name: "tampered ciphertext", cipher: cipher, envelope: tamperedCiphertext, aad: aad},
		{name: "wrong key", cipher: wrongKeyCipher, envelope: valid, aad: aad},
		{name: "wrong aad", cipher: cipher, envelope: valid, aad: []byte("account:43:session")},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			_, err := test.cipher.Open(test.envelope, test.aad)
			if !errors.Is(err, ErrAuthentication) {
				t.Fatalf("Open() error = %v, want %v", err, ErrAuthentication)
			}
		})
	}
}

func TestEnvelopeCannotBeSwappedAcrossResources(t *testing.T) {
	t.Parallel()

	cipher := mustCipher(t, "primary-1", 0x21)
	resourceOneAAD := []byte("account:42:session")
	resourceTwoAAD := []byte("account:43:session")
	envelope, err := cipher.Seal([]byte("synthetic-resource-one-marker"), resourceOneAAD)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if _, err := cipher.Open(envelope, resourceTwoAAD); !errors.Is(err, ErrAuthentication) {
		t.Fatalf("Open(swapped envelope) error = %v, want %v", err, ErrAuthentication)
	}
}

func TestErrorsDoNotContainSensitiveInputs(t *testing.T) {
	t.Parallel()

	keyMarker := "synthetic-32-byte-key-material!!"
	if len(keyMarker) != keySize {
		t.Fatalf("test key length = %d, want %d", len(keyMarker), keySize)
	}
	cipher, err := NewCipher("primary-1", []byte(keyMarker))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	plaintextMarker := "synthetic-plaintext-sensitive-marker"
	aadMarker := "synthetic-aad-sensitive-marker"
	envelope, err := cipher.Seal([]byte(plaintextMarker), []byte(aadMarker))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	envelope.Ciphertext[0] ^= 0xff
	_, err = cipher.Open(envelope, []byte(aadMarker))
	if err == nil {
		t.Fatal("Open() error = nil, want authentication failure")
	}
	for _, marker := range []string{keyMarker, plaintextMarker, aadMarker, string(envelope.Ciphertext)} {
		if strings.Contains(err.Error(), marker) {
			t.Fatalf("error exposed sensitive input: %q", err)
		}
	}
}

func TestCipherConcurrentUse(t *testing.T) {
	cipher := mustCipher(t, "primary-1", 0x11)
	const workers = 32
	const iterations = 50

	var wait sync.WaitGroup
	errorsCh := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < iterations; iteration++ {
				plaintext := []byte(fmt.Sprintf("synthetic-marker:%d:%d", worker, iteration))
				aad := []byte(fmt.Sprintf("account:%d:session", worker))
				envelope, err := cipher.Seal(plaintext, aad)
				if err != nil {
					errorsCh <- err
					return
				}
				opened, err := cipher.Open(envelope, aad)
				if err != nil {
					errorsCh <- err
					return
				}
				if !bytes.Equal(opened, plaintext) {
					errorsCh <- errors.New("concurrent round trip mismatch")
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		t.Errorf("concurrent cipher use: %v", err)
	}
}

func mustCipher(t *testing.T, keyID string, fill byte) *Cipher {
	t.Helper()
	cipher, err := NewCipher(keyID, bytes.Repeat([]byte{fill}, keySize))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	return cipher
}

func cloneEnvelope(envelope Envelope) Envelope {
	envelope.Nonce = append([]byte(nil), envelope.Nonce...)
	envelope.Ciphertext = append([]byte(nil), envelope.Ciphertext...)
	return envelope
}

func identityEnvelope(envelope Envelope) Envelope {
	return envelope
}
