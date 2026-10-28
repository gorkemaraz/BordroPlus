package secret

import (
	"bytes"
	"encoding/base64"
	"testing"
)

var testKey = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))

func TestSealOpenRoundTrip(t *testing.T) {
	b, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte("employee_salaries:42:2026-01-01")
	ct, err := b.SealInt64(4_500_050, aad)
	if err != nil {
		t.Fatal(err)
	}
	if ct[0] != 1 || len(ct) != 1+12+8+16 {
		t.Fatalf("unexpected layout: version=%d len=%d", ct[0], len(ct))
	}
	v, err := b.OpenInt64(ct, aad)
	if err != nil || v != 4_500_050 {
		t.Fatalf("OpenInt64 = %d, %v", v, err)
	}

	ct2, _ := b.SealInt64(4_500_050, aad)
	if bytes.Equal(ct, ct2) {
		t.Fatal("equal salaries must not produce equal ciphertexts (random nonce)")
	}
}

func TestOpenRejectsTamperingAndSwaps(t *testing.T) {
	b, _ := New(testKey)
	ct, _ := b.SealInt64(100, []byte("row:1"))

	// Ciphertext moved to another row.
	if _, err := b.OpenInt64(ct, []byte("row:2")); err == nil {
		t.Error("ciphertext opened under a different row's aad")
	}
	// Bit flip.
	bad := bytes.Clone(ct)
	bad[len(bad)-1] ^= 1
	if _, err := b.OpenInt64(bad, []byte("row:1")); err == nil {
		t.Error("tampered ciphertext opened")
	}
	// Unknown key version, truncated input.
	bad = bytes.Clone(ct)
	bad[0] = 9
	for _, c := range [][]byte{bad, ct[:10], nil} {
		if _, err := b.OpenInt64(c, []byte("row:1")); err == nil {
			t.Error("malformed ciphertext opened")
		}
	}
	// Different key.
	other, _ := New(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)))
	if _, err := other.OpenInt64(ct, []byte("row:1")); err == nil {
		t.Error("ciphertext opened with another key")
	}
}

func TestNewRejectsBadKeys(t *testing.T) {
	for _, k := range []string{"", "short", base64.StdEncoding.EncodeToString(make([]byte, 16))} {
		if _, err := New(k); err == nil {
			t.Errorf("key %q accepted", k)
		}
	}
}
