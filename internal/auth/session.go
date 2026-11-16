package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// NewSessionToken devolve um token de sessão de 32 bytes aleatórios e o hash
// dele. Só o hash vai para o banco: um vazamento do banco não entrega
// sessões válidas (DEC-27 item 4).
func NewSessionToken() (token string, hash []byte, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("auth: gerar token de sessão: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// HashToken é o SHA-256 do token. Como o token tem 256 bits de entropia, um
// hash rápido basta.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
