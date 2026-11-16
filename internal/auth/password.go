// Package auth reúne as peças de autenticação do administrador (DEC-27):
// senha com argon2id, tokens de sessão, limite de tentativas, IP do cliente
// atrás de proxy e o token de configuração inicial.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Parâmetros do argon2id (DEC-27 item 2): 64 MiB, 3 passadas, 2 threads.
const (
	argonMemoryKiB = 64 * 1024
	argonTime      = 3
	argonThreads   = 2
	argonKeyLen    = 32
	saltLen        = 16

	MinPasswordLen = 12
	MaxPasswordLen = 128
)

// ErrWeakPassword descreve por que uma senha foi recusada.
type ErrWeakPassword struct{ Reason string }

func (e *ErrWeakPassword) Error() string { return e.Reason }

// CheckPassword aplica a política de senha: de 12 a 128 caracteres e diferente
// do login, sem regras de composição (NIST SP 800-63B). O máximo evita que uma
// senha enorme vire ataque de recurso.
func CheckPassword(login, password string) error {
	n := utf8.RuneCountInString(password)
	switch {
	case n < MinPasswordLen:
		return &ErrWeakPassword{fmt.Sprintf("a senha deve ter pelo menos %d caracteres", MinPasswordLen)}
	case n > MaxPasswordLen:
		return &ErrWeakPassword{fmt.Sprintf("a senha deve ter no máximo %d caracteres", MaxPasswordLen)}
	case strings.EqualFold(password, login):
		return &ErrWeakPassword{"a senha não pode ser igual ao login"}
	case strings.TrimSpace(password) == "":
		return &ErrWeakPassword{"a senha não pode ser só espaços"}
	}
	return nil
}

// Hasher calcula e verifica senhas. Cada operação de argon2id usa 64 MiB,
// então só algumas correm ao mesmo tempo: as demais esperam, e uma enxurrada
// de tentativas de login não esgota a memória (DEC-27 item 9).
type Hasher struct {
	slots   chan struct{}
	dummy   string
	memory  uint32
	passes  uint32
	threads uint8
}

// NewHasher cria um Hasher com os parâmetros de produção (DEC-27 item 2) que
// permite até concurrent operações simultâneas.
func NewHasher(concurrent int) (*Hasher, error) {
	return NewHasherWithParams(concurrent, argonMemoryKiB, argonTime, argonThreads)
}

// NewHasherWithParams permite custos menores, para testes que precisam fazer
// muitos logins. Hashes já gravados continuam verificáveis: os parâmetros
// vêm dentro do próprio hash.
func NewHasherWithParams(concurrent int, memoryKiB, passes uint32, threads uint8) (*Hasher, error) {
	if concurrent < 1 {
		concurrent = 1
	}
	h := &Hasher{slots: make(chan struct{}, concurrent), memory: memoryKiB, passes: passes, threads: threads}
	dummy, err := h.hash("senha-fictícia-para-igualar-o-tempo")
	if err != nil {
		return nil, err
	}
	h.dummy = dummy
	return h, nil
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hasher) release() { <-h.slots }

// Hash devolve o hash PHC da senha, com salt aleatório.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	return h.hash(password)
}

func (h *Hasher) hash(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: gerar salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.passes, h.memory, h.threads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, h.memory, h.passes, h.threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify confere a senha contra um hash PHC, em tempo constante.
func (h *Hasher) Verify(ctx context.Context, encoded, password string) (bool, error) {
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer h.release()
	return verify(encoded, password)
}

// VerifyDummy gasta o mesmo tempo de uma verificação real. O login o usa
// quando o usuário não existe, para a resposta não revelar quais logins
// existem pelo tempo que leva.
func (h *Hasher) VerifyDummy(ctx context.Context, password string) error {
	_, err := h.Verify(ctx, h.dummy, password)
	return err
}

func verify(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	// ["", "argon2id", "v=19", "m=...,t=...,p=...", salt, key]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("auth: hash em formato desconhecido")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("auth: versão de argon2 não suportada")
	}
	var memory, passes uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &passes, &threads); err != nil {
		return false, errors.New("auth: parâmetros de argon2 inválidos")
	}
	// Um hash adulterado no banco não pode pedir memória ou tempo ilimitados.
	if memory > 1<<20 || passes > 10 || threads == 0 || threads > 16 {
		return false, errors.New("auth: parâmetros de argon2 fora do limite aceito")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return false, errors.New("auth: salt inválido")
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) < 16 {
		return false, errors.New("auth: hash inválido")
	}
	got := argon2.IDKey([]byte(password), salt, passes, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
