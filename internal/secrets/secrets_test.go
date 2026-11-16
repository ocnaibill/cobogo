package secrets

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func newBox(t *testing.T, key []byte) *Box {
	t.Helper()
	b, err := NewBox(key, 1)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpenRoundTrip(t *testing.T) {
	b := newBox(t, testKey)
	for _, plain := range []string{"x", "valor com espaço e acento: ação", strings.Repeat("a", 32<<10), "-----BEGIN KEY-----\nlinha\n-----END KEY-----"} {
		sealed, err := b.Seal([]byte(plain), []byte("ctx"))
		if err != nil {
			t.Fatal(err)
		}
		got, err := b.Open(sealed, []byte("ctx"))
		if err != nil || string(got) != plain {
			t.Errorf("round trip of %d bytes: got %d bytes, err %v", len(plain), len(got), err)
		}
	}
}

func TestSealDoesNotRevealOrRepeat(t *testing.T) {
	b := newBox(t, testKey)
	plain := []byte("segredo-reconhecivel-no-texto-cifrado")
	first, _ := b.Seal(plain, nil)
	second, _ := b.Seal(plain, nil)
	if bytes.Contains(first, plain) {
		t.Error("the ciphertext contains the plaintext")
	}
	if bytes.Equal(first, second) {
		t.Error("two seals of the same value must differ (fresh nonce)")
	}
}

func TestOpenRejectsEverythingButTheOriginalContext(t *testing.T) {
	b := newBox(t, testKey)
	sealed, err := b.Seal([]byte("valor"), []byte("projeto-a/NOME/1"))
	if err != nil {
		t.Fatal(err)
	}

	tampered := bytes.Clone(sealed)
	tampered[len(tampered)-1] ^= 1
	truncated := sealed[:len(sealed)-1]

	otherKey := bytes.Clone(testKey)
	otherKey[0] ^= 1

	cases := map[string]func() ([]byte, error){
		"different associated data (another project)": func() ([]byte, error) { return b.Open(sealed, []byte("projeto-b/NOME/1")) },
		"different associated data (another version)": func() ([]byte, error) { return b.Open(sealed, []byte("projeto-a/NOME/2")) },
		"no associated data":                          func() ([]byte, error) { return b.Open(sealed, nil) },
		"flipped bit":                                 func() ([]byte, error) { return b.Open(tampered, []byte("projeto-a/NOME/1")) },
		"truncated":                                   func() ([]byte, error) { return b.Open(truncated, []byte("projeto-a/NOME/1")) },
		"too short":                                   func() ([]byte, error) { return b.Open([]byte("curto"), nil) },
		"another key":                                 func() ([]byte, error) { return newBox(t, otherKey).Open(sealed, []byte("projeto-a/NOME/1")) },
	}
	for name, open := range cases {
		if got, err := open(); !errors.Is(err, ErrCorrupt) || got != nil {
			t.Errorf("%s: got %q, err %v; want ErrCorrupt", name, got, err)
		}
	}
}

func TestKeyValidation(t *testing.T) {
	if _, err := NewBox(testKey[:16], 1); err == nil {
		t.Error("a 16-byte key must be rejected")
	}
	std := base64.StdEncoding.EncodeToString(testKey)
	for name, in := range map[string]string{
		"standard":   std,
		"no padding": strings.TrimRight(std, "="),
		"url-safe":   base64.URLEncoding.EncodeToString(testKey),
	} {
		if key, err := ParseKey(in); err != nil || !bytes.Equal(key, testKey) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, in := range map[string]string{
		"not base64": "isto não é base64!",
		"too short":  base64.StdEncoding.EncodeToString(testKey[:16]),
		"empty":      "",
	} {
		if _, err := ParseKey(in); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestGenerate(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		s, err := Generate(32)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil || len(raw) != 32 {
			t.Fatalf("Generate(32) = %q (%d bytes, %v)", s, len(raw), err)
		}
		if seen[s] {
			t.Fatal("repeated secret")
		}
		seen[s] = true
	}
}
