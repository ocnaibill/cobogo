package dockerrt

import (
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func TestEngineSpecsAreComplete(t *testing.T) {
	for name, e := range engines {
		if e.Name != name || e.Image == "" || e.DataPath == "" || e.Port == 0 || e.User == "" || e.UID == "" || e.MemoryMB == 0 || e.Pids == 0 {
			t.Errorf("engine %s incompleta: %+v", name, e)
		}
		if len(e.Health) == 0 || e.InitEnv == nil || e.ClientEnv == nil || e.DumpCommand == nil || e.RestoreScript == "" {
			t.Errorf("engine %s sem saúde, inicialização, dump ou restauração", name)
		}
		if got := domain.EngineDataPath(name); got != e.DataPath {
			t.Errorf("engine %s: o diretório de dados do domínio (%q) difere do runtime (%q)", name, got, e.DataPath)
		}
		// A senha vai por ambiente, nunca na linha de comando.
		cmd := strings.Join(e.DumpCommand("host", "f.dump"), " ")
		if strings.Contains(cmd, "S3nh4") {
			t.Errorf("engine %s põe a senha na linha de comando", name)
		}
		for _, v := range e.InitEnv("S3nh4") {
			if strings.Contains(v, "S3nh4") {
				goto next
			}
		}
		t.Errorf("engine %s não leva a senha na inicialização", name)
	next:
	}
	if _, err := EngineFor("oracle"); err == nil {
		t.Error("engine desconhecida aceita")
	}
}

// O PostgreSQL não muda de comportamento: os mesmos dados que o runtime usava antes da
// especificação por engine.
func TestPostgresEngineKeepsItsBehaviour(t *testing.T) {
	e, err := EngineFor(domain.ServicePostgres)
	if err != nil {
		t.Fatal(err)
	}
	if e.Image != PostgresImage || e.DataPath != "/var/lib/postgresql/data" || e.Port != 5432 || e.UID != "70" {
		t.Errorf("%+v", e)
	}
	if strings.Join(e.Health, " ") != "CMD-SHELL pg_isready -U app -d app -h 127.0.0.1" {
		t.Errorf("saúde = %v", e.Health)
	}
	if strings.Join(e.InitEnv("x"), ",") != "POSTGRES_USER=app,POSTGRES_DB=app,POSTGRES_PASSWORD=x" {
		t.Errorf("init = %v", e.InitEnv("x"))
	}
	if strings.Join(e.DumpCommand("h", "f"), " ") != "pg_dump --format=custom --no-owner --no-acl -h h -U app -d app -f /out/f" {
		t.Errorf("dump = %v", e.DumpCommand("h", "f"))
	}
	if !strings.Contains(e.RestoreScript, "--single-transaction") || e.TLS == nil {
		t.Error("restauração em transação única e TLS do PostgreSQL")
	}
}
