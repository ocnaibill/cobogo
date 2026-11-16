package auth

import (
	"strings"
	"sync"
	"time"
)

// Limites de tentativas de login (DEC-27 item 9).
const (
	PairFailures   = 5  // falhas do par (IP, login) na janela
	LoginFailures  = 30 // falhas de um login, de qualquer IP, na janela
	LimitWindow    = 15 * time.Minute
	BlockDuration  = 15 * time.Minute
	maxTrackedKeys = 10000
)

// LoginLimiter limita tentativas de senha. Os contadores ficam em memória e
// reiniciam com o processo; reiniciar a API é a saída do operador se um
// ataque trancar o painel. Uma sessão já aberta nunca é afetada: o limiter só
// é consultado no login.
type LoginLimiter struct {
	mu    sync.Mutex
	now   func() time.Time
	pairs map[string]*bucket
	names map[string]*bucket
}

type bucket struct {
	failures     []time.Time
	blockedUntil time.Time
}

func NewLoginLimiter() *LoginLimiter { return newLoginLimiter(time.Now) }

func newLoginLimiter(now func() time.Time) *LoginLimiter {
	return &LoginLimiter{now: now, pairs: map[string]*bucket{}, names: map[string]*bucket{}}
}

func normalize(login string) string { return strings.ToLower(strings.TrimSpace(login)) }

// Allow diz se uma tentativa pode seguir; se não, quanto falta para liberar.
func (l *LoginLimiter) Allow(ip, login string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var wait time.Duration
	for _, b := range []*bucket{l.pairs[ip+"|"+normalize(login)], l.names[normalize(login)]} {
		if b != nil && now.Before(b.blockedUntil) {
			if d := b.blockedUntil.Sub(now); d > wait {
				wait = d
			}
		}
	}
	return wait == 0, wait
}

// Fail registra uma tentativa errada.
func (l *LoginLimiter) Fail(ip, login string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	l.record(l.pairs, ip+"|"+normalize(login), PairFailures, now)
	l.record(l.names, normalize(login), LoginFailures, now)
}

// Succeed zera o contador do par: quem acertou a senha não carrega falhas
// antigas. O contador global do login não é zerado (um atacante não pode
// limpá-lo errando de um IP e acertando de outro).
func (l *LoginLimiter) Succeed(ip, login string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.pairs, ip+"|"+normalize(login))
}

func (l *LoginLimiter) record(m map[string]*bucket, key string, limit int, now time.Time) {
	b := m[key]
	if b == nil {
		b = &bucket{}
		m[key] = b
	}
	cutoff := now.Add(-LimitWindow)
	kept := b.failures[:0]
	for _, t := range b.failures {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	b.failures = append(kept, now)
	if len(b.failures) >= limit {
		b.blockedUntil = now.Add(BlockDuration)
		b.failures = nil
	}
}

// sweep descarta contadores vazios ou vencidos para a memória não crescer sem
// limite com logins e IPs inventados.
func (l *LoginLimiter) sweep(now time.Time) {
	if len(l.pairs)+len(l.names) < maxTrackedKeys {
		return
	}
	cutoff := now.Add(-LimitWindow)
	for _, m := range []map[string]*bucket{l.pairs, l.names} {
		for k, b := range m {
			if now.After(b.blockedUntil) && (len(b.failures) == 0 || b.failures[len(b.failures)-1].Before(cutoff)) {
				delete(m, k)
			}
		}
	}
}
