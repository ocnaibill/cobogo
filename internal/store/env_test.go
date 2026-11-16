package store_test

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/secrets"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

const canaryValue = "valor-canario-do-usuario-8f3a"

func TestEnvVariableLifecycle(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)

	v, err := st.PutEnvVariable(ctx, p.ID, "API_URL", "https://a.example", domain.EnvSourceUser)
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != 1 || v.Source != domain.EnvSourceUser || v.KeyVersion != 1 {
		t.Errorf("first write = %+v", v)
	}

	v, err = st.PutEnvVariable(ctx, p.ID, "API_URL", "https://b.example", domain.EnvSourceUser)
	if err != nil || v.Version != 2 {
		t.Fatalf("second write = %+v, %v; want version 2", v, err)
	}
	if _, err := st.PutEnvVariable(ctx, p.ID, "JWT_SECRET", "s3", domain.EnvSourceGenerated); err != nil {
		t.Fatal(err)
	}

	values, meta, err := st.RevealEnvValues(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if values["API_URL"] != "https://b.example" || values["JWT_SECRET"] != "s3" || len(values) != 2 {
		t.Errorf("values = %v", values)
	}
	if len(meta) != 2 || meta[0].Name != "API_URL" || meta[0].Version != 2 || meta[1].Source != domain.EnvSourceGenerated {
		t.Errorf("meta = %+v", meta)
	}

	list, err := st.ListEnvVariables(ctx, p.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}

	if err := st.DeleteEnvVariable(ctx, p.ID, "API_URL"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteEnvVariable(ctx, p.ID, "API_URL"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleting twice: %v, want ErrNotFound", err)
	}
	if _, err := st.GetEnvVariable(ctx, p.ID, "API_URL"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get after delete: %v, want ErrNotFound", err)
	}
}

func TestEnvVariablesAreIsolatedPerProject(t *testing.T) {
	st := storetest.New(t)
	a, b := newProject(t, st), newProject(t, st)
	if _, err := st.PutEnvVariable(ctx, a.ID, "TOKEN", "do-projeto-a", domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}
	values, _, err := st.RevealEnvValues(ctx, b.ID)
	if err != nil || len(values) != 0 {
		t.Errorf("project b sees %v, %v", values, err)
	}
	if _, err := st.PutEnvVariable(ctx, uuid.New(), "TOKEN", "x", domain.EnvSourceUser); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown project: %v, want ErrNotFound", err)
	}
}

func TestEnvValueIsOnlyStoredEncrypted(t *testing.T) {
	pool := storetest.Pool(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.NewBox(storetest.Key, 1)
	st := store.New(pool).WithCipher(box)
	p := newProject(t, st)

	if _, err := st.PutEnvVariable(ctx, p.ID, "DB_PASSWORD", canaryValue, domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err := pool.QueryRow(ctx, `SELECT ciphertext FROM env_variables WHERE name = 'DB_PASSWORD'`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(canaryValue)) {
		t.Error("the stored ciphertext contains the plaintext")
	}

	// Nenhuma outra coluna da tabela guarda o valor.
	var leaked bool
	if err := pool.QueryRow(ctx, `SELECT ((to_jsonb(e) - 'ciphertext')::text) LIKE '%' || $1 || '%' FROM env_variables e`, canaryValue).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked {
		t.Error("the plaintext appears in a metadata column")
	}
}

// Um texto cifrado copiado para outro projeto, outro nome ou outra versão não
// abre: os dados autenticados o prendem ao contexto original.
func TestSealedValueCannotBeReusedElsewhere(t *testing.T) {
	pool := storetest.Pool(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.NewBox(storetest.Key, 1)
	st := store.New(pool).WithCipher(box)
	a, b := newProject(t, st), newProject(t, st)

	if _, err := st.PutEnvVariable(ctx, a.ID, "SECRET_A", "valor-de-a", domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutEnvVariable(ctx, b.ID, "SECRET_B", "valor-de-b", domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}

	copyOver := func(t *testing.T, query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("another project", func(t *testing.T) {
		copyOver(t, `UPDATE env_variables SET ciphertext = (SELECT ciphertext FROM env_variables WHERE name = 'SECRET_A') WHERE name = 'SECRET_B'`)
		if _, _, err := st.RevealEnvValues(ctx, b.ID); !errors.Is(err, secrets.ErrCorrupt) {
			t.Errorf("reveal = %v, want ErrCorrupt", err)
		}
	})
	t.Run("another version", func(t *testing.T) {
		copyOver(t, `UPDATE env_variables SET version = version + 1 WHERE name = 'SECRET_A'`)
		if _, _, err := st.RevealEnvValues(ctx, a.ID); !errors.Is(err, secrets.ErrCorrupt) {
			t.Errorf("reveal = %v, want ErrCorrupt", err)
		}
	})
}

func TestRevealWithAnotherKeyFails(t *testing.T) {
	pool := storetest.Pool(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.NewBox(storetest.Key, 1)
	st := store.New(pool).WithCipher(box)
	p := newProject(t, st)
	if _, err := st.PutEnvVariable(ctx, p.ID, "TOKEN", "x", domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}

	other := bytes.Clone(storetest.Key)
	other[0] ^= 1
	otherBox, _ := secrets.NewBox(other, 1)
	if _, _, err := store.New(pool).WithCipher(otherBox).RevealEnvValues(ctx, p.ID); !errors.Is(err, secrets.ErrCorrupt) {
		t.Errorf("reveal with another key = %v, want ErrCorrupt", err)
	}
}

func TestEnvRequiresACipher(t *testing.T) {
	pool := storetest.Pool(t)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)
	p := newProject(t, st)
	if _, err := st.PutEnvVariable(ctx, p.ID, "A", "b", domain.EnvSourceUser); !errors.Is(err, store.ErrNoCipher) {
		t.Errorf("put without cipher = %v", err)
	}
	if _, _, err := st.RevealEnvValues(ctx, p.ID); !errors.Is(err, store.ErrNoCipher) {
		t.Errorf("reveal without cipher = %v", err)
	}
}

func TestConcurrentWritesOfTheSameVariable(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)

	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := st.PutEnvVariable(ctx, p.ID, "COUNTER", fmt.Sprint(i), domain.EnvSourceUser); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	v, err := st.GetEnvVariable(ctx, p.ID, "COUNTER")
	if err != nil || v.Version != n {
		t.Errorf("version = %d (%v), want %d: every write must take its own version", v.Version, err, n)
	}
	if _, _, err := st.RevealEnvValues(ctx, p.ID); err != nil {
		t.Errorf("the final value must be readable: %v", err)
	}
}
