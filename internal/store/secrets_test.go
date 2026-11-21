package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/secrets"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

var (
	keyOne = []byte("0123456789abcdef0123456789abcdef")
	keyTwo = []byte("fedcba9876543210fedcba9876543210")
)

func keyring(t *testing.T, current int, versions ...int) *secrets.Keyring {
	t.Helper()
	keys := map[int][]byte{}
	for _, v := range versions {
		keys[v] = map[int][]byte{1: keyOne, 2: keyTwo}[v]
	}
	k, err := secrets.NewKeyring(keys, current)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestRequiredEnvRoundTripAndScope(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)

	if got, err := st.RequiredEnv(ctx, p.ID); err != nil || len(got) != 0 {
		t.Fatalf("a new project requires nothing: %v, %v", got, err)
	}
	got, err := st.SetRequiredEnv(ctx, p.ID, own.ID, []string{"DATABASE_URL", "API_KEY", "DATABASE_URL"})
	if err != nil || strings.Join(got, ",") != "API_KEY,DATABASE_URL" {
		t.Fatalf("set = %v, %v; want sorted and deduplicated", got, err)
	}
	if read, _ := st.RequiredEnv(ctx, p.ID); strings.Join(read, ",") != "API_KEY,DATABASE_URL" {
		t.Errorf("read back = %v", read)
	}
	if _, err := st.SetRequiredEnv(ctx, p.ID, uuid.New(), []string{"X"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another owner = %v, want ErrNotFound", err)
	}
	if cleared, err := st.SetRequiredEnv(ctx, p.ID, own.ID, nil); err != nil || len(cleared) != 0 {
		t.Errorf("clearing = %v, %v", cleared, err)
	}
	if _, err := st.RequiredEnv(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown project = %v, want ErrNotFound", err)
	}
	if _, err := st.DeleteProject(ctx, p.ID, own.ID, p.Slug, false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetRequiredEnv(ctx, p.ID, own.ID, []string{"X"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a deleted project = %v, want ErrNotFound", err)
	}
}

func TestEnvChangesComeFromTheAuditAndNeverHaveValues(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	other := newProject(t, st)
	for _, ev := range []domain.AuditEvent{
		{ActorID: &own.ID, Action: "env.set", Target: p.ID.String() + "/API_URL", Outcome: "ok", Detail: map[string]any{"version": 1, "source": "user"}},
		{ActorID: &own.ID, Action: "env.generate", Target: p.ID.String() + "/JWT_SECRET", Outcome: "ok", Detail: map[string]any{"version": 1, "source": "generated"}},
		{ActorID: &own.ID, Action: "env.set", Target: p.ID.String() + "/API_URL", Outcome: "ok", Detail: map[string]any{"version": 2, "source": "imported"}},
		{ActorID: &own.ID, Action: "env.required", Target: p.ID.String(), Outcome: "ok", Detail: map[string]any{"names": []string{"API_URL"}}},
		{ActorID: &own.ID, Action: "env.set", Target: other.ID.String() + "/OUTRA", Outcome: "ok", Detail: map[string]any{"version": 1, "source": "user"}},
		{ActorID: &own.ID, Action: "env.set", Target: p.ID.String() + "/RECUSADA", Outcome: "refused"},
		{ActorID: &own.ID, Action: "login", Target: p.ID.String(), Outcome: "ok"},
	} {
		if err := st.AppendAudit(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	changes, err := st.ListEnvChanges(ctx, p.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 4 {
		t.Fatalf("changes = %+v, want the project's 4 successful env events (not the other project's, the refusal or the login)", changes)
	}
	// Da mais nova para a mais antiga.
	if changes[0].Action != "env.required" || len(changes[0].Names) != 1 || changes[0].Name != "" {
		t.Errorf("newest = %+v", changes[0])
	}
	if changes[1].Name != "API_URL" || changes[1].Version != 2 || changes[1].Source != "imported" || changes[1].Actor != own.Login {
		t.Errorf("second = %+v", changes[1])
	}
	if changes[3].Name != "API_URL" || changes[3].Version != 1 || changes[3].Source != "user" {
		t.Errorf("oldest = %+v", changes[3])
	}
	if len(mustList(t, st, p.ID, 2)) != 2 {
		t.Error("the limit must apply")
	}
}

func mustList(t *testing.T, st *store.Store, id uuid.UUID, limit int) []store.EnvChange {
	t.Helper()
	c, err := st.ListEnvChanges(ctx, id, limit)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestKeyRotationKeepsEverythingReadableWithoutANewVersion(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	p := newProject(t, st)
	old := st.WithCipher(keyring(t, 1, 1))
	if _, err := old.PutEnvVariable(ctx, p.ID, "ANTIGA", "valor-antigo", domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}

	// A chave nova entra como atual; a antiga continua no chaveiro.
	both := st.WithCipher(keyring(t, 2, 1, 2))
	if values, _, err := both.RevealEnvValues(ctx, p.ID); err != nil || values["ANTIGA"] != "valor-antigo" {
		t.Fatalf("a value written with the old key must open with the keyring: %v, %v", values, err)
	}
	fresh, err := both.PutEnvVariable(ctx, p.ID, "NOVA", "valor-novo", domain.EnvSourceUser)
	if err != nil || fresh.KeyVersion != 2 {
		t.Fatalf("new writes use the current key: %+v, %v", fresh, err)
	}
	counts, _ := both.KeyVersionCounts(ctx)
	if counts[1] != 1 || counts[2] != 1 {
		t.Fatalf("counts before = %v", counts)
	}

	// Tirar a chave antiga antes de rotacionar deixa o valor antigo ilegível, e diz por quê.
	onlyNew := st.WithCipher(keyring(t, 2, 2))
	if _, _, err := onlyNew.RevealEnvValues(ctx, p.ID); !errors.Is(err, secrets.ErrUnknownKey) {
		t.Fatalf("an unrotated value without its key = %v, want ErrUnknownKey", err)
	}

	report, err := both.RotateSecrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Current != 2 || report.Rotated[1] != 1 || len(report.Rotated) != 1 {
		t.Errorf("report = %+v", report)
	}
	counts, _ = both.KeyVersionCounts(ctx)
	if counts[2] != 2 || counts[1] != 0 {
		t.Errorf("counts after = %v, want everything on key 2", counts)
	}
	// O valor é o mesmo: a versão da variável não mudou (não pede novo deploy).
	meta, _ := st.GetEnvVariable(ctx, p.ID, "ANTIGA")
	if meta.Version != 1 || meta.KeyVersion != 2 {
		t.Errorf("after rotation ANTIGA = %+v, want version 1 on key 2", meta)
	}
	// Agora a chave antiga pode sair.
	if values, _, err := onlyNew.RevealEnvValues(ctx, p.ID); err != nil || values["ANTIGA"] != "valor-antigo" || values["NOVA"] != "valor-novo" {
		t.Fatalf("after rotation the old key is not needed: %v, %v", values, err)
	}
	// E a chave antiga sozinha já não abre nada que foi rotacionado.
	if _, _, err := st.WithCipher(keyring(t, 1, 1)).RevealEnvValues(ctx, p.ID); err == nil {
		t.Error("the old key alone must not open rotated values")
	}
	// Rodar de novo não faz nada.
	if again, err := both.RotateSecrets(ctx); err != nil || len(again.Rotated) != 0 {
		t.Errorf("a second rotation = %+v, %v; want nothing to do", again, err)
	}
	_ = pool
}

func TestKeyRotationIsAllOrNothingAndNamesTheVariableNeverTheValue(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	secret := "valor-que-nao-pode-aparecer-no-erro"
	one := st.WithCipher(keyring(t, 1, 1))
	for _, name := range []string{"A", "B"} {
		if _, err := one.PutEnvVariable(ctx, p.ID, name, secret, domain.EnvSourceUser); err != nil {
			t.Fatal(err)
		}
	}
	// Chave atual 2, mas a antiga (1) não está no chaveiro: nada abre, nada muda.
	broken := st.WithCipher(keyring(t, 2, 2))
	_, err := broken.RotateSecrets(ctx)
	if err == nil {
		t.Fatal("a rotation that cannot open a value must fail")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the error leaked a value: %v", err)
	}
	if !strings.Contains(err.Error(), "A") && !strings.Contains(err.Error(), "B") {
		t.Errorf("the error must name the variable: %v", err)
	}
	counts, _ := st.KeyVersionCounts(ctx)
	if counts[1] != 2 || counts[2] != 0 {
		t.Errorf("a failed rotation must change nothing, counts = %v", counts)
	}
}

func TestTamperedAndSwappedCiphertextIsRefused(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	a, b := newProject(t, st), newProject(t, st)
	for _, c := range []struct {
		p    domain.Project
		name string
	}{{a, "SEGREDO"}, {a, "OUTRO"}, {b, "SEGREDO"}} {
		if _, err := st.PutEnvVariable(ctx, c.p.ID, c.name, "v-"+c.p.Slug+"-"+c.name, domain.EnvSourceUser); err != nil {
			t.Fatal(err)
		}
	}
	reveal := func(p domain.Project) error { _, _, err := st.RevealEnvValues(ctx, p.ID); return err }
	if err := reveal(a); err != nil {
		t.Fatalf("untouched = %v", err)
	}

	// Um byte alterado.
	if _, err := pool.Exec(ctx, `UPDATE env_variables SET ciphertext = set_byte(ciphertext, length(ciphertext) - 1, get_byte(ciphertext, length(ciphertext) - 1) # 1) WHERE project_id = $1 AND name = 'SEGREDO'`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := reveal(a); !errors.Is(err, secrets.ErrCorrupt) {
		t.Errorf("a flipped byte = %v, want ErrCorrupt", err)
	}
	if err := reveal(b); err != nil {
		t.Errorf("another project must be unaffected: %v", err)
	}

	// Texto cifrado de um projeto colado em outro (mesmo nome), e de um nome em outro.
	reset := func() {
		for _, c := range []struct {
			p    domain.Project
			name string
		}{{a, "SEGREDO"}, {a, "OUTRO"}, {b, "SEGREDO"}} {
			if _, err := st.PutEnvVariable(ctx, c.p.ID, c.name, "v-"+c.p.Slug+"-"+c.name, domain.EnvSourceUser); err != nil {
				t.Fatal(err)
			}
		}
	}
	reset()
	if _, err := pool.Exec(ctx, `UPDATE env_variables x SET ciphertext = y.ciphertext FROM env_variables y WHERE x.project_id = $1 AND x.name = 'SEGREDO' AND y.project_id = $2 AND y.name = 'SEGREDO'`, a.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := reveal(a); !errors.Is(err, secrets.ErrCorrupt) {
		t.Errorf("ciphertext moved between projects = %v, want ErrCorrupt", err)
	}
	reset()
	if _, err := pool.Exec(ctx, `UPDATE env_variables x SET ciphertext = y.ciphertext FROM env_variables y WHERE x.project_id = $1 AND x.name = 'SEGREDO' AND y.project_id = $1 AND y.name = 'OUTRO'`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := reveal(a); !errors.Is(err, secrets.ErrCorrupt) {
		t.Errorf("ciphertext moved between names = %v, want ErrCorrupt", err)
	}
	// E entre versões: o texto cifrado da versão 1 não vale como versão 2.
	reset()
	if _, err := st.PutEnvVariable(ctx, a.ID, "SEGREDO", "segunda", domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE env_variables SET version = 1 WHERE project_id = $1 AND name = 'SEGREDO'`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := reveal(a); !errors.Is(err, secrets.ErrCorrupt) {
		t.Errorf("ciphertext replayed as another version = %v, want ErrCorrupt", err)
	}
}

func TestThePlaintextIsNowhereInTheDatabase(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	own := owner(t, st)
	p := newProject(t, st)
	canary := "canario-que-nunca-deve-estar-em-texto-claro-93b1"
	if _, err := st.PutEnvVariable(ctx, p.ID, "TOKEN", canary, domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}
	// As ações que cercam a variável também passam pelo banco: auditoria e histórico.
	_ = st.AppendAudit(ctx, domain.AuditEvent{ActorID: &own.ID, Action: "env.set", Target: p.ID.String() + "/TOKEN", Outcome: "ok", Detail: map[string]any{"version": 1, "source": "user"}})
	if _, _, err := st.RevealEnvValues(ctx, p.ID); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		tables = append(tables, n)
	}
	rows.Close()
	if len(tables) < 8 {
		t.Fatalf("expected the whole schema, found %v", tables)
	}
	for _, table := range tables {
		var hits int
		q := `SELECT count(*) FROM "` + table + `" t WHERE t::text LIKE '%' || $1 || '%'`
		if err := pool.QueryRow(ctx, q, canary).Scan(&hits); err != nil {
			t.Fatalf("scan %s: %v", table, err)
		}
		if hits != 0 {
			t.Errorf("the plaintext value appears in table %s", table)
		}
	}
	// E o texto cifrado não é o valor em outra codificação óbvia.
	var raw []byte
	_ = pool.QueryRow(ctx, `SELECT ciphertext FROM env_variables WHERE name = 'TOKEN'`).Scan(&raw)
	if strings.Contains(string(raw), canary) || len(raw) < len(canary) {
		t.Error("the ciphertext must not contain the value")
	}
}
