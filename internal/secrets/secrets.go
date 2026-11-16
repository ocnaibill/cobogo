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
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
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
