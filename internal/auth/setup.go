package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"sync"
)

// SetupToken é o token de uso único que autoriza criar o administrador
// (DEC-27 item 3). Fica só em memória, como hash: reiniciar a API gera outro.
type SetupToken struct {
	mu   sync.Mutex
	hash [32]byte
	used bool
}

// NewSetupToken gera o token e devolve seu valor em claro, para ser impresso
// no log uma única vez.
func NewSetupToken() (*SetupToken, string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, "", fmt.Errorf("auth: gerar token de configuração: %w", err)
	}
	plain := base64.RawURLEncoding.EncodeToString(raw)
	return &SetupToken{hash: sha256.Sum256([]byte(plain))}, plain, nil
}

// Check confere o token em tempo constante. Depois de Consume, nenhum valor
// é aceito.
func (t *SetupToken) Check(candidate string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.used {
		return false
	}
	sum := sha256.Sum256([]byte(candidate))
	return subtle.ConstantTimeCompare(sum[:], t.hash[:]) == 1
}

// Consume invalida o token (o administrador foi criado).
func (t *SetupToken) Consume() {
	t.mu.Lock()
	t.used = true
	t.mu.Unlock()
}
