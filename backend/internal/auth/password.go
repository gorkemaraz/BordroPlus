package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters: OWASP's 19 MiB / 2 passes / 1 lane profile. It keeps
// a login around 20–40 ms so many concurrent logins do not exhaust memory.
const (
	argonMemory  = 19 * 1024
	argonTime    = 2
	argonThreads = 1
	argonSaltLen = 16
	argonKeyLen  = 32

	// MaxPasswordLen bounds hashing cost for attacker-supplied input.
	MaxPasswordLen = 256
)

var errMalformedHash = errors.New("malformed argon2id hash")

var b64 = base64.RawStdEncoding

// HashPassword returns a PHC-formatted argon2id hash:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the encoded hash. The
// parameters are read from the hash, so older hashes keep verifying after the
// defaults change.
func VerifyPassword(password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errMalformedHash
	}
	var mem uint32
	var iter uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &iter, &threads); err != nil {
		return false, errMalformedHash
	}
	if mem == 0 || iter == 0 || threads == 0 || mem > 1<<20 || iter > 16 {
		return false, errMalformedHash
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errMalformedHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errMalformedHash
	}
	got := argon2.IDKey([]byte(password), salt, iter, mem, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// dummyHash is verified against when the e-mail is unknown, so a failed
// login costs the same time whether or not the account exists.
var dummyHash = func() string {
	h, err := HashPassword("bordroplus-timing-equaliser")
	if err != nil {
		panic(err)
	}
	return h
}()
