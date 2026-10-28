// Package secret encrypts sensitive fields (salaries) with AES-256-GCM before
// they reach the database. Ciphertexts carry a key version so keys can be
// rotated: new data uses the current key, old data stays readable.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
)

const keyLen = 32

var ErrDecrypt = errors.New("secret: decryption failed")

// Box holds the encryption keys by version.
type Box struct {
	current uint8
	aeads   map[uint8]cipher.AEAD
}

// New builds a Box whose current key is version 1, from a base64-encoded
// 32-byte key (SALARY_ENCRYPTION_KEY).
func New(b64Key string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(b64Key)
	if err != nil || len(key) != keyLen {
		return nil, fmt.Errorf("secret: key must be %d bytes, base64-encoded", keyLen)
	}
	b := &Box{aeads: map[uint8]cipher.AEAD{}}
	if err := b.add(1, key); err != nil {
		return nil, err
	}
	b.current = 1
	return b, nil
}

func (b *Box) add(version uint8, key []byte) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	b.aeads[version] = aead
	return nil
}

// KeyVersion is the version new ciphertexts are written with.
func (b *Box) KeyVersion() int16 { return int16(b.current) }

// Seal encrypts plaintext. aad binds the ciphertext to its context (table and
// row identity); Open must be given the same aad.
// Layout: version (1) | nonce (12) | ciphertext+tag.
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	aead := b.aeads[b.current]
	out := make([]byte, 1+aead.NonceSize(), 1+aead.NonceSize()+len(plaintext)+aead.Overhead())
	out[0] = b.current
	if _, err := rand.Read(out[1:]); err != nil {
		return nil, err
	}
	return aead.Seal(out, out[1:], plaintext, aad), nil
}

// Open decrypts a Seal output.
func (b *Box) Open(ciphertext, aad []byte) ([]byte, error) {
	if len(ciphertext) < 1 {
		return nil, ErrDecrypt
	}
	aead, ok := b.aeads[ciphertext[0]]
	if !ok || len(ciphertext) < 1+aead.NonceSize()+aead.Overhead() {
		return nil, ErrDecrypt
	}
	nonce := ciphertext[1 : 1+aead.NonceSize()]
	pt, err := aead.Open(nil, nonce, ciphertext[1+aead.NonceSize():], aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// SealInt64 and OpenInt64 encrypt integer amounts (kuruş).
func (b *Box) SealInt64(v int64, aad []byte) ([]byte, error) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(v))
	return b.Seal(buf[:], aad)
}

func (b *Box) OpenInt64(ciphertext, aad []byte) (int64, error) {
	pt, err := b.Open(ciphertext, aad)
	if err != nil {
		return 0, err
	}
	if len(pt) != 8 {
		return 0, ErrDecrypt
	}
	return int64(binary.BigEndian.Uint64(pt)), nil
}
