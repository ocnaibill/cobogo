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

func TestParseKeyringFromTheEnvironment(t *testing.T) {
	k1 := base64.StdEncoding.EncodeToString(testKey)
	other := make([]byte, 32)
	for i := range other {
		other[i] = byte(200 - i)
	}
	k2 := base64.StdEncoding.EncodeToString(other)
	aad := []byte("cobogo/env/p/N/1")

	// Só COBOGO_SECRET_KEY: uma chave, versão 1 (quem não rotaciona não muda nada).
	ring, err := ParseKeyring("", "", k1)
	if err != nil || ring.KeyVersion() != 1 || len(ring.Versions()) != 1 {
		t.Fatalf("single key: %v, %v", ring, err)
	}

	// Duas chaves, a 2 grava; o que a 1 cifrou continua abrindo.
	old, err := ParseKeyring("", "", k1)
	if err != nil {
		t.Fatal(err)
	}
	sealedOld, _ := old.Seal([]byte("segredo"), aad)
	ring, err = ParseKeyring("1:"+k1+", 2:"+k2, "2", "")
	if err != nil || ring.KeyVersion() != 2 {
		t.Fatalf("two keys: %v, %v", ring, err)
	}
	if got, err := ring.OpenVersion(1, sealedOld, aad); err != nil || string(got) != "segredo" {
		t.Errorf("the old key must still open what it sealed: %q, %v", got, err)
	}
	sealedNew, _ := ring.Seal([]byte("novo"), aad)
	if _, err := old.OpenVersion(1, sealedNew, aad); err == nil {
		t.Error("the old key alone must not open what the new key sealed")
	}

	// COBOGO_SECRET_KEY junto da lista é a versão 1.
	if _, err := ParseKeyring("2:"+k2, "2", k1); err != nil {
		t.Errorf("legacy key beside the list: %v", err)
	}

	for name, c := range map[string]struct{ keys, current, single string }{
		"nothing":                 {"", "", ""},
		"current without a list":  {"", "2", k1},
		"several without current": {"1:" + k1 + ",2:" + k2, "", ""},
		"current not in the list": {"1:" + k1, "3", ""},
		"current not a number":    {"1:" + k1 + ",2:" + k2, "dois", ""},
		"duplicate version":       {"1:" + k1 + ",1:" + k2, "1", ""},
		"version zero":            {"0:" + k1, "0", ""},
		"missing version":         {k1, "1", ""},
		"bad key in the list":     {"1:curta", "1", ""},
		"legacy differs from v1":  {"1:" + k1, "1", k2},
		"legacy key invalid":      {"2:" + k2, "2", "curta"},
		"empty list":              {" , ", "1", ""},
	} {
		_, err := ParseKeyring(c.keys, c.current, c.single)
		if err == nil {
			t.Errorf("%s: want an error", name)
			continue
		}
		// Nenhuma mensagem repete uma chave.
		if strings.Contains(err.Error(), k1) || strings.Contains(err.Error(), k2) {
			t.Errorf("%s: the error repeats a key: %v", name, err)
		}
	}
}
