// Package secrets cifra os valores de variáveis de ambiente dos projetos em
// repouso (DEC-24 item 10, DEC-14, RNF-08): AES-256-GCM, chave fora do banco e
// dados autenticados que prendem o texto cifrado ao projeto, ao nome e à
// versão da variável.
//
// Alcance (RN-18): quem tem a chave decifra. Em M1 a API e o worker usam a
// mesma chave; o Cobogó não promete E2EE nem que o servidor seja incapaz de ler
// os valores.
package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// KeySize é o tamanho da chave AES-256, em bytes.
const KeySize = 32

// ErrCorrupt indica texto cifrado adulterado, aberto com chave errada ou com
// dados autenticados diferentes dos usados ao cifrar. O motivo exato não é
// distinguido de propósito.
var ErrCorrupt = errors.New("secrets: valor cifrado inválido, adulterado ou associado a outro contexto")

type Box struct {
	aead       cipher.AEAD
	keyVersion int
}

// NewBox cria uma caixa com a chave dada (32 bytes). keyVersion identifica a
// chave no registro, para uma rotação futura.
func NewBox(key []byte, keyVersion int) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secrets: a chave deve ter %d bytes, recebi %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: criar cifra: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: criar GCM: %w", err)
	}
	return &Box{aead: aead, keyVersion: keyVersion}, nil
}

// ParseKey decodifica uma chave em base64 (padrão ou URL-safe).
func ParseKey(encoded string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if key, err := enc.DecodeString(encoded); err == nil {
			if len(key) != KeySize {
				return nil, fmt.Errorf("secrets: a chave deve ter %d bytes, recebi %d", KeySize, len(key))
			}
			return key, nil
		}
	}
	return nil, errors.New("secrets: a chave não está em base64")
}

func (b *Box) KeyVersion() int { return b.keyVersion }

// ErrUnknownKey indica texto cifrado de uma versão de chave que o chaveiro não
// tem (a chave antiga foi tirada antes de rotacionar tudo, DEC-39 item 6).
var ErrUnknownKey = errors.New("secrets: a versão de chave do valor não está no chaveiro")

// OpenVersion decifra um valor gravado com a versão de chave dada. Uma caixa só
// tem uma chave: outra versão é ErrUnknownKey.
func (b *Box) OpenVersion(keyVersion int, sealed, aad []byte) ([]byte, error) {
	if keyVersion != b.keyVersion {
		return nil, ErrUnknownKey
	}
	return b.Open(sealed, aad)
}

// Seal cifra o texto. O resultado é nonce || texto cifrado || tag; o nonce é
// aleatório e novo a cada chamada.
func (b *Box) Seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secrets: gerar nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plaintext, aad), nil
}

// Open decifra o resultado de Seal, exigindo os mesmos dados autenticados.
func (b *Box) Open(sealed, aad []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n+b.aead.Overhead() {
		return nil, ErrCorrupt
	}
	plaintext, err := b.aead.Open(nil, sealed[:n], sealed[n:], aad)
	if err != nil {
		return nil, ErrCorrupt
	}
	return plaintext, nil
}

// Generate devolve um segredo aleatório de n bytes em base64 URL-safe, sem
// padding, para variáveis que o próprio app inventa (JWT_SECRET etc.).
func Generate(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("secrets: gerar segredo: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Keyring guarda várias chaves por versão: grava com a atual e abre cada valor
// com a chave da versão dele (DEC-39 item 6). Isso é o que permite rotacionar.
type Keyring struct {
	boxes   map[int]*Box
	current int
}

// NewKeyring monta o chaveiro. current precisa estar entre as chaves.
func NewKeyring(keys map[int][]byte, current int) (*Keyring, error) {
	if len(keys) == 0 {
		return nil, errors.New("secrets: o chaveiro precisa de ao menos uma chave")
	}
	k := &Keyring{boxes: map[int]*Box{}, current: current}
	for version, key := range keys {
		if version < 1 {
			return nil, fmt.Errorf("secrets: versão de chave inválida %d (use 1 ou mais)", version)
		}
		box, err := NewBox(key, version)
		if err != nil {
			return nil, fmt.Errorf("secrets: chave da versão %d: %w", version, err)
		}
		k.boxes[version] = box
	}
	if _, ok := k.boxes[current]; !ok {
		return nil, fmt.Errorf("secrets: a chave atual (versão %d) não está no chaveiro", current)
	}
	return k, nil
}

func (k *Keyring) KeyVersion() int { return k.current }

// Seal cifra com a chave atual.
func (k *Keyring) Seal(plaintext, aad []byte) ([]byte, error) {
	return k.boxes[k.current].Seal(plaintext, aad)
}

// Open decifra com a chave atual.
func (k *Keyring) Open(sealed, aad []byte) ([]byte, error) {
	return k.boxes[k.current].Open(sealed, aad)
}

// OpenVersion decifra com a chave da versão dada.
func (k *Keyring) OpenVersion(keyVersion int, sealed, aad []byte) ([]byte, error) {
	box, ok := k.boxes[keyVersion]
	if !ok {
		return nil, ErrUnknownKey
	}
	return box.Open(sealed, aad)
}

// Versions lista as versões do chaveiro, em ordem crescente.
func (k *Keyring) Versions() []int {
	out := make([]int, 0, len(k.boxes))
	for v := range k.boxes {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

// ParseKeyring monta o chaveiro a partir do ambiente (DEC-39 item 6).
//
//   - Só `single` (COBOGO_SECRET_KEY): uma chave, versão 1. É o que vale para
//     quem não rotaciona.
//   - `keys` (COBOGO_SECRET_KEYS, "1:<chave>,2:<chave>") e `current`
//     (COBOGO_SECRET_KEY_CURRENT): várias chaves; as novas gravações usam a
//     atual. Com mais de uma chave, `current` é obrigatória, para um esquecimento
//     não decidir qual chave grava. Se `single` também vier, é a versão 1 e
//     precisa ser igual à da lista, se ela a tiver.
//
// Nenhuma mensagem de erro repete uma chave.
func ParseKeyring(keys, current, single string) (*Keyring, error) {
	keys, current, single = strings.TrimSpace(keys), strings.TrimSpace(current), strings.TrimSpace(single)
	if keys == "" {
		if single == "" {
			return nil, errors.New("falta COBOGO_SECRET_KEY (32 bytes em base64; gere com `openssl rand -base64 32`)")
		}
		if current != "" && current != "1" {
			return nil, errors.New("COBOGO_SECRET_KEY_CURRENT só vale com COBOGO_SECRET_KEYS")
		}
		key, err := ParseKey(single)
		if err != nil {
			return nil, fmt.Errorf("COBOGO_SECRET_KEY inválida: %w", err)
		}
		return NewKeyring(map[int][]byte{1: key}, 1)
	}

	parsed := map[int][]byte{}
	for _, item := range strings.Split(keys, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		rawVersion, rawKey, ok := strings.Cut(item, ":")
		version, err := strconv.Atoi(strings.TrimSpace(rawVersion))
		if !ok || err != nil || version < 1 {
			return nil, errors.New("COBOGO_SECRET_KEYS: use versão:chave separados por vírgula, com a versão 1 ou mais")
		}
		key, err := ParseKey(strings.TrimSpace(rawKey))
		if err != nil {
			return nil, fmt.Errorf("COBOGO_SECRET_KEYS: chave da versão %d inválida: %w", version, err)
		}
		if _, dup := parsed[version]; dup {
			return nil, fmt.Errorf("COBOGO_SECRET_KEYS: a versão %d aparece duas vezes", version)
		}
		parsed[version] = key
	}
	if len(parsed) == 0 {
		return nil, errors.New("COBOGO_SECRET_KEYS está vazia")
	}
	if single != "" {
		key, err := ParseKey(single)
		if err != nil {
			return nil, fmt.Errorf("COBOGO_SECRET_KEY inválida: %w", err)
		}
		if other, ok := parsed[1]; ok && !bytes.Equal(other, key) {
			return nil, errors.New("COBOGO_SECRET_KEY (versão 1) é diferente da versão 1 de COBOGO_SECRET_KEYS")
		}
		parsed[1] = key
	}

	cur := 0
	switch {
	case current != "":
		v, err := strconv.Atoi(current)
		if err != nil {
			return nil, errors.New("COBOGO_SECRET_KEY_CURRENT precisa ser o número de uma versão")
		}
		cur = v
	case len(parsed) == 1:
		for v := range parsed {
			cur = v
		}
	default:
		return nil, errors.New("com várias chaves, defina COBOGO_SECRET_KEY_CURRENT (a versão que grava)")
	}
	return NewKeyring(parsed, cur)
}
