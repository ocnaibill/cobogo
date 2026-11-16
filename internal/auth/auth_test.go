package auth

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func mustHasher(t *testing.T, n int) *Hasher {
	t.Helper()
	h, err := NewHasher(n)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestPasswordRoundTrip(t *testing.T) {
	h := mustHasher(t, 2)
	ctx := context.Background()
	hash, err := h.Hash(ctx, "uma senha bem comprida e única")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Errorf("hash = %q: expected an argon2id PHC string with the DEC-27 parameters", hash)
	}
	if strings.Contains(hash, "senha") {
		t.Error("the hash must not contain the password")
	}
	if ok, err := h.Verify(ctx, hash, "uma senha bem comprida e única"); err != nil || !ok {
		t.Errorf("the right password: %v, %v", ok, err)
	}
	for _, wrong := range []string{"outra senha bem comprida", "", "uma senha bem comprida e única ", "UMA SENHA BEM COMPRIDA E ÚNICA"} {
		if ok, err := h.Verify(ctx, hash, wrong); err != nil || ok {
			t.Errorf("wrong password %q accepted: %v, %v", wrong, ok, err)
		}
	}
	// Mesmo senha, salts diferentes.
	other, _ := h.Hash(ctx, "uma senha bem comprida e única")
	if other == hash {
		t.Error("two hashes of the same password must differ (random salt)")
	}
}

func TestVerifyRefusesMalformedAndAbusiveHashes(t *testing.T) {
	h := mustHasher(t, 1)
	for name, encoded := range map[string]string{
		"empty":            "",
		"plain text":       "senha",
		"other algorithm":  "$argon2i$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
		"bad version":      "$argon2id$v=16$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
		"bad params":       "$argon2id$v=19$x$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
		"huge memory":      "$argon2id$v=19$m=4194304,t=3,p=2$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
		"huge time":        "$argon2id$v=19$m=65536,t=999,p=2$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
		"zero threads":     "$argon2id$v=19$m=65536,t=3,p=0$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA",
		"short salt":       "$argon2id$v=19$m=65536,t=3,p=2$YQ$aGFzaGhhc2hoYXNoaGFzaA",
		"short hash":       "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0$YQ",
		"non base64 parts": "$argon2id$v=19$m=65536,t=3,p=2$!!!$???",
	} {
		if ok, err := h.Verify(context.Background(), encoded, "qualquer"); ok || err == nil {
			t.Errorf("%s: ok=%v err=%v; a malformed hash must be refused with an error", name, ok, err)
		}
	}
}

func TestCheckPasswordPolicy(t *testing.T) {
	for _, ok := range []string{"doze caracteres", "uma senha longa com espaços e acentuação", strings.Repeat("a", 128), "açaí-e-café-com-leite"} {
		if err := CheckPassword("admin", ok); err != nil {
			t.Errorf("%q must be accepted: %v", ok, err)
		}
	}
	for name, c := range map[string][2]string{
		"short":       {"admin", "curta123"},
		"eleven":      {"admin", "onzecaracte"},
		"too long":    {"admin", strings.Repeat("a", 129)},
		"same login":  {"administrador", "Administrador"},
		"only spaces": {"admin", strings.Repeat(" ", 20)},
	} {
		var weak *ErrWeakPassword
		if err := CheckPassword(c[0], c[1]); !errors.As(err, &weak) {
			t.Errorf("%s: err = %v, want ErrWeakPassword", name, err)
		}
	}
	// Conta caracteres, não bytes: 12 letras acentuadas passam.
	if err := CheckPassword("x", strings.Repeat("é", 12)); err != nil {
		t.Errorf("12 characters of 2 bytes: %v", err)
	}
}

// A enxurrada de verificações não pode ocupar mais memória do que o limite.
func TestHasherLimitsConcurrentWork(t *testing.T) {
	h := mustHasher(t, 2)
	hash, err := h.Hash(context.Background(), "uma senha bem comprida e única")
	if err != nil {
		t.Fatal(err)
	}
	var running, peak atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Reproduz a concorrência de h.Verify medindo quantas passam juntas.
			if err := h.acquire(context.Background()); err != nil {
				return
			}
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			_, _ = verify(hash, "senha errada")
			running.Add(-1)
			h.release()
		}()
	}
	wg.Wait()
	if peak.Load() > 2 {
		t.Errorf("peak concurrency = %d, want at most 2", peak.Load())
	}
}

func TestHasherGivesUpWhenTheContextEnds(t *testing.T) {
	h := mustHasher(t, 1)
	h.slots <- struct{}{} // ocupa o único espaço
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := h.Hash(ctx, "uma senha bem comprida e única"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the context error instead of waiting forever", err)
	}
	if err := h.VerifyDummy(ctx, "x"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("VerifyDummy err = %v", err)
	}
}

func TestVerifyDummyCostsAboutAsMuchAsARealVerification(t *testing.T) {
	h := mustHasher(t, 2)
	hash, _ := h.Hash(context.Background(), "uma senha bem comprida e única")
	time1 := func(f func()) time.Duration {
		start := time.Now()
		f()
		return time.Since(start)
	}
	real := time1(func() { _, _ = h.Verify(context.Background(), hash, "errada") })
	dummy := time1(func() { _ = h.VerifyDummy(context.Background(), "errada") })
	if dummy < real/3 || dummy > real*3 {
		t.Errorf("dummy %s vs real %s: the unknown-login path must not be distinguishable by time", dummy, real)
	}
}

func TestSessionTokens(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		token, hash, err := NewSessionToken()
		if err != nil {
			t.Fatal(err)
		}
		if len(token) < 40 || len(hash) != 32 || seen[token] {
			t.Fatalf("token %q (%d chars), hash %d bytes, repeated=%v", token, len(token), len(hash), seen[token])
		}
		seen[token] = true
		if string(HashToken(token)) != string(hash) {
			t.Error("HashToken must reproduce the stored hash")
		}
		if strings.Contains(string(hash), token) {
			t.Error("the hash must not contain the token")
		}
	}
}

func TestSetupTokenIsSingleUse(t *testing.T) {
	tok, plain, err := NewSetupToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) < 30 {
		t.Errorf("token = %q", plain)
	}
	for _, bad := range []string{"", "x", plain + "x", plain[:len(plain)-1], strings.ToUpper(plain)} {
		if bad != plain && tok.Check(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if !tok.Check(plain) || !tok.Check(plain) {
		t.Error("the token must stay valid until consumed (a failed form can retry)")
	}
	tok.Consume()
	if tok.Check(plain) {
		t.Error("a consumed token must never be accepted again")
	}
	_, other, _ := NewSetupToken()
	if other == plain {
		t.Error("each start generates a different token")
	}
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func TestLoginLimiterBlocksThePairAfterFiveFailures(t *testing.T) {
	c := &clock{now: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	l := newLoginLimiter(c.Now)

	for i := 0; i < PairFailures-1; i++ {
		if ok, _ := l.Allow("1.2.3.4", "admin"); !ok {
			t.Fatalf("attempt %d must be allowed", i+1)
		}
		l.Fail("1.2.3.4", "admin")
	}
	if ok, _ := l.Allow("1.2.3.4", "admin"); !ok {
		t.Fatal("the 5th attempt must still be allowed")
	}
	l.Fail("1.2.3.4", "admin") // 5ª falha
	ok, wait := l.Allow("1.2.3.4", "admin")
	if ok || wait != BlockDuration {
		t.Fatalf("after 5 failures: allowed=%v wait=%s", ok, wait)
	}
	// Outro IP não é afetado pelo bloqueio do par.
	if ok, _ := l.Allow("5.6.7.8", "admin"); !ok {
		t.Error("another IP must not be blocked by one pair")
	}
	// O login é comparado sem diferenciar caixa nem espaços.
	if ok, _ := l.Allow("1.2.3.4", "  ADMIN "); ok {
		t.Error("the same login written differently must hit the same bucket")
	}

	c.Advance(BlockDuration + time.Second)
	if ok, _ := l.Allow("1.2.3.4", "admin"); !ok {
		t.Error("the block must end after 15 minutes")
	}
}

func TestLoginLimiterForgetsOldFailures(t *testing.T) {
	c := &clock{now: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	l := newLoginLimiter(c.Now)
	for i := 0; i < PairFailures-1; i++ {
		l.Fail("1.2.3.4", "admin")
	}
	c.Advance(LimitWindow + time.Minute)
	l.Fail("1.2.3.4", "admin") // a janela já passou: conta como a primeira
	if ok, _ := l.Allow("1.2.3.4", "admin"); !ok {
		t.Error("failures older than the window must not add up")
	}
}

// Quem troca de IP a cada tentativa é pego pelo limite global do login.
func TestLoginLimiterBlocksTheLoginAcrossIPs(t *testing.T) {
	c := &clock{now: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	l := newLoginLimiter(c.Now)
	for i := 0; i < LoginFailures; i++ {
		ip := netip.AddrFrom4([4]byte{10, 0, byte(i / 250), byte(i%250 + 1)}).String()
		if ok, _ := l.Allow(ip, "admin"); !ok {
			t.Fatalf("attempt %d from a fresh IP must be allowed", i+1)
		}
		l.Fail(ip, "admin")
	}
	if ok, _ := l.Allow("99.99.99.99", "admin"); ok {
		t.Error("after 30 failures the login must be blocked for every IP")
	}
	if ok, _ := l.Allow("99.99.99.99", "outro-login"); !ok {
		t.Error("another login must not be affected")
	}
}

func TestLoginLimiterSuccessClearsOnlyThePair(t *testing.T) {
	c := &clock{now: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	l := newLoginLimiter(c.Now)
	for i := 0; i < PairFailures-1; i++ {
		l.Fail("1.2.3.4", "admin")
	}
	l.Succeed("1.2.3.4", "admin")
	l.Fail("1.2.3.4", "admin")
	if ok, _ := l.Allow("1.2.3.4", "admin"); !ok {
		t.Error("a successful login clears the pair's earlier failures")
	}
	// O contador global do login não é zerado por um sucesso: ele acumula.
	l.mu.Lock()
	n := len(l.names["admin"].failures)
	l.mu.Unlock()
	if n != PairFailures {
		t.Errorf("global failures = %d, want %d: a success from one IP must not erase an attacker's trail", n, PairFailures)
	}
}

func TestLoginLimiterMemoryIsBounded(t *testing.T) {
	c := &clock{now: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
	l := newLoginLimiter(c.Now)
	for i := 0; i < maxTrackedKeys+500; i++ {
		l.Fail("1.1.1.1", "login-"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+time.Duration(i).String())
		if i%1000 == 0 {
			c.Advance(time.Minute)
		}
	}
	c.Advance(2 * LimitWindow)
	l.Fail("2.2.2.2", "gatilho")
	l.mu.Lock()
	total := len(l.pairs) + len(l.names)
	l.mu.Unlock()
	if total > maxTrackedKeys {
		t.Errorf("%d tracked keys after the sweep, want at most %d", total, maxTrackedKeys)
	}
}

func req(remote string, headers map[string]string) *http.Request {
	r, _ := http.NewRequest("GET", "http://x/", nil)
	r.RemoteAddr = remote
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestClientIPIgnoresProxyHeadersFromUntrustedPeers(t *testing.T) {
	// Sem proxies configurados: nada é confiável, e quem forja o cabeçalho é sinalizado.
	ip, suspicious := ClientIP(req("172.18.0.5:44321", map[string]string{"CF-Connecting-IP": "9.9.9.9"}), nil)
	if ip.String() != "172.18.0.5" || !suspicious {
		t.Errorf("ip = %s suspicious = %v: with no trusted proxy the header must be ignored and reported", ip, suspicious)
	}
	trusted := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}
	ip, suspicious = ClientIP(req("8.8.8.8:1234", map[string]string{"CF-Connecting-IP": "9.9.9.9", "X-Forwarded-For": "7.7.7.7"}), trusted)
	if ip.String() != "8.8.8.8" || !suspicious {
		t.Errorf("a direct client forging headers: ip = %s suspicious = %v", ip, suspicious)
	}
	ip, suspicious = ClientIP(req("8.8.8.8:1234", nil), trusted)
	if ip.String() != "8.8.8.8" || suspicious {
		t.Errorf("a plain request: ip = %s suspicious = %v", ip, suspicious)
	}
}

func TestClientIPBehindTheTunnel(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}
	cases := map[string]struct {
		remote  string
		headers map[string]string
		want    string
	}{
		"cloudflare header":        {"172.18.0.5:1", map[string]string{"CF-Connecting-IP": "203.0.113.7"}, "203.0.113.7"},
		"cloudflare wins":          {"172.18.0.5:1", map[string]string{"CF-Connecting-IP": "203.0.113.7", "X-Forwarded-For": "1.1.1.1"}, "203.0.113.7"},
		"forwarded chain":          {"172.18.0.5:1", map[string]string{"X-Forwarded-For": "203.0.113.7, 172.18.0.9"}, "203.0.113.7"},
		"forged left entry":        {"172.18.0.5:1", map[string]string{"X-Forwarded-For": "6.6.6.6, 203.0.113.7, 172.18.0.9"}, "203.0.113.7"},
		"single entry":             {"172.18.0.5:1", map[string]string{"X-Forwarded-For": "203.0.113.7"}, "203.0.113.7"},
		"multiple header lines":    {"172.18.0.5:1", nil, "172.18.0.5"},
		"garbage cloudflare":       {"172.18.0.5:1", map[string]string{"CF-Connecting-IP": "não-é-ip", "X-Forwarded-For": "203.0.113.7"}, "203.0.113.7"},
		"only trusted in the list": {"172.18.0.5:1", map[string]string{"X-Forwarded-For": "172.18.0.9, 172.18.0.10"}, "172.18.0.5"},
		"ipv6 client":              {"172.18.0.5:1", map[string]string{"CF-Connecting-IP": "2001:db8::7"}, "2001:db8::7"},
		"ipv4-mapped":              {"172.18.0.5:1", map[string]string{"CF-Connecting-IP": "::ffff:203.0.113.7"}, "203.0.113.7"},
		"no headers":               {"172.18.0.5:1", nil, "172.18.0.5"},
	}
	for name, c := range cases {
		ip, suspicious := ClientIP(req(c.remote, c.headers), trusted)
		if ip.String() != c.want || suspicious {
			t.Errorf("%s: ip = %s (suspicious %v), want %s", name, ip, suspicious, c.want)
		}
	}

	r := req("172.18.0.5:1", nil)
	r.Header.Add("X-Forwarded-For", "6.6.6.6")
	r.Header.Add("X-Forwarded-For", "203.0.113.7")
	if ip, _ := ClientIP(r, trusted); ip.String() != "203.0.113.7" {
		t.Errorf("repeated X-Forwarded-For lines: ip = %s", ip)
	}
}

func TestClientIPWithABrokenRemoteAddress(t *testing.T) {
	ip, _ := ClientIP(req("sem-porta", nil), nil)
	if ip.IsValid() {
		t.Errorf("an unparsable address must yield an invalid IP, got %s", ip)
	}
}
