package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// O que a plataforma já sabe vira configuração (DEC-52 parte B): colar o link do fec e
// confirmar a proposta deixa o backend e o frontend prontos, sem variável digitada.
func TestStackAppsArePreparedByThePlatform(t *testing.T) {
	h := newHarness(t)
	h.login()
	if rec := h.do("PUT", "/api/v1/settings/apps_domain", `{"value":"cobogo.test"}`); rec.Code != http.StatusOK {
		t.Fatalf("apps_domain: %d %s", rec.Code, rec.Body)
	}
	rec := h.do("POST", "/api/v1/stacks", `{"repository_url":"https://github.com/exemplo/fec"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar: %d %s", rec.Code, rec.Body)
	}
	st := decode[domain.Stack](t, rec)
	userVar := func(name string, class domain.VarClass) domain.ConfigVariable {
		return domain.ConfigVariable{Name: name, Class: class}
	}
	scan := domain.StackScan{CommitSHA: "abc", Apps: []domain.StackScanApp{
		{RootDirectory: "", Role: domain.CompBackend, Slug: "fec-api",
			Variables: []domain.ConfigVariable{
				userVar("ALLOWED_HOSTS", domain.ClassUser), userVar("CSRF_TRUSTED_ORIGINS", domain.ClassUser),
				userVar("FRONTEND_URL", domain.ClassUser), userVar("SECRET_KEY", domain.ClassGenerable),
				userVar("DB_HOST", domain.ClassUser), userVar("EMAIL_HOST", domain.ClassUser),
				userVar("VITE_API_URL", domain.ClassPublic), userVar("PORT", domain.ClassPlatform),
			},
			ServiceNeeds: []domain.ServiceNeed{
				{Engine: domain.ServiceMySQL, Level: domain.NeedRequired, Style: domain.StyleParts, Variable: "DB_PASSWORD",
					Variables: map[string]string{"host": "DB_HOST", "port": "DB_PORT", "user": "DB_USER", "password": "DB_PASSWORD", "database": "DB_NAME"}},
				{Engine: domain.ServiceRedis, Level: domain.NeedRequired, Style: domain.StyleURL, Variable: "CELERY_BROKER_URL", Variables: map[string]string{"url": "CELERY_BROKER_URL"}},
			}},
		{RootDirectory: "frontend", Role: domain.CompFrontend, Slug: "fec",
			Variables: []domain.ConfigVariable{userVar("VITE_API_URL", domain.ClassPublic)},
			APIPaths:  map[string]string{"VITE_API_URL": "/api"}},
	}}
	if err := h.st.SaveStackScan(t.Context(), st.ID, scan); err != nil {
		t.Fatal(err)
	}
	rec = h.do("POST", "/api/v1/stacks/fec/apps", `{"deploy":true,"idempotency_key":"k","apps":[{"root_directory":"","slug":"fec-api","role":"backend"},{"root_directory":"frontend","slug":"fec","role":"frontend"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar aplicações: %d %s", rec.Code, rec.Body)
	}
	out := decode[struct {
		Stack domain.Stack `json:"stack"`
	}](t, rec)
	var back, front domain.ProjectSummary
	for _, a := range out.Stack.Apps {
		if a.Role == domain.CompBackend {
			back = a
		} else {
			front = a
		}
	}
	vals, vars, err := h.st.RevealEnvValues(t.Context(), back.ID)
	if err != nil {
		t.Fatal(err)
	}
	source := map[string]domain.EnvSource{}
	for _, v := range vars {
		source[v.Name] = v.Source
	}
	for name, want := range map[string]string{
		"ALLOWED_HOSTS":        "fec-api.cobogo.test",
		"CSRF_TRUSTED_ORIGINS": "https://fec-api.cobogo.test,https://fec.cobogo.test",
		"FRONTEND_URL":         "https://fec.cobogo.test",
	} {
		if vals[name] != want || source[name] != domain.EnvSourcePlatform {
			t.Errorf("%s = %q (%s), want %q da plataforma", name, vals[name], source[name], want)
		}
	}
	if len(vals["SECRET_KEY"]) < 32 || source["SECRET_KEY"] != domain.EnvSourceGenerated {
		t.Errorf("SECRET_KEY gerada: %d caracteres, %s", len(vals["SECRET_KEY"]), source["SECRET_KEY"])
	}
	if source["DB_HOST"] != domain.EnvSourceLinked || source["CELERY_BROKER_URL"] != domain.EnvSourceLinked || !strings.HasPrefix(vals["CELERY_BROKER_URL"], "redis://") {
		t.Errorf("serviços sem pergunta: DB_HOST %s, CELERY_BROKER_URL %s", source["DB_HOST"], source["CELERY_BROKER_URL"])
	}
	for _, name := range []string{"EMAIL_HOST", "PORT", "VITE_API_URL"} {
		if _, ok := vals[name]; ok {
			t.Errorf("%s não é da plataforma e não devia ter valor", name)
		}
	}
	fvals, _, _ := h.st.RevealEnvValues(t.Context(), front.ID)
	if fvals["VITE_API_URL"] != "https://fec-api.cobogo.test/api" {
		t.Errorf("VITE_API_URL = %q", fvals["VITE_API_URL"])
	}
	if svc, err := h.st.GetService(t.Context(), back.ID, domain.ServiceMySQL); err != nil || svc.Mode != domain.ServiceManaged {
		t.Errorf("MySQL do Cobogó: %+v %v", svc, err)
	}

	// O painel vê o valor e a regra das variáveis da plataforma (endereços, não segredos).
	rec = h.do("GET", "/api/v1/projects/"+back.ID.String()+"/env", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"value":"fec-api.cobogo.test"`) || strings.Contains(rec.Body.String(), vals["SECRET_KEY"]) {
		t.Errorf("lista: %d %s", rec.Code, rec.Body)
	}

	// O que a pessoa digita é dela: trocar o domínio não mexe.
	if rec := h.do("PUT", "/api/v1/projects/"+back.ID.String()+"/env/FRONTEND_URL", `{"value":"https://meu.site"}`); rec.Code != http.StatusOK {
		t.Fatalf("definir: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("PUT", "/api/v1/settings/apps_domain", `{"value":"outro.test"}`); rec.Code != http.StatusOK {
		t.Fatalf("apps_domain: %d %s", rec.Code, rec.Body)
	}
	vals, _, _ = h.st.RevealEnvValues(t.Context(), back.ID)
	fvals, _, _ = h.st.RevealEnvValues(t.Context(), front.ID)
	if vals["ALLOWED_HOSTS"] != "fec-api.outro.test" || fvals["VITE_API_URL"] != "https://fec-api.outro.test/api" || vals["FRONTEND_URL"] != "https://meu.site" {
		t.Errorf("depois do domínio novo: %q %q %q", vals["ALLOWED_HOSTS"], fvals["VITE_API_URL"], vals["FRONTEND_URL"])
	}

	// Um serviço apagado de propósito não volta sozinho.
	if rec := h.do("DELETE", "/api/v1/projects/"+back.ID.String()+"/database", `{"confirm_slug":"fec-api","engine":"redis"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("apagar o Redis: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("POST", "/api/v1/stacks/fec/deployments", `{"idempotency_key":"k2"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("publicar: %d %s", rec.Code, rec.Body)
	}
	if _, vars, _ := h.st.RevealEnvValues(t.Context(), back.ID); hasVar(vars, "CELERY_BROKER_URL") {
		t.Error("o Redis apagado voltou sozinho")
	}
}

func hasVar(vars []domain.EnvVariable, name string) bool {
	for _, v := range vars {
		if v.Name == name {
			return true
		}
	}
	return false
}
