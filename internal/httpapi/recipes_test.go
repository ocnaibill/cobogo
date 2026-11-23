package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

// withReleaseResult leva um projeto novo a ter a análise dada e uma release ativa.
func (h *harness) withReleaseResult(slug string, result domain.AnalysisResult) string {
	h.t.Helper()
	p := h.createProject(slug)
	dep := h.deployment(p.ID.String(), `{"idempotency_key":"r-`+slug+`"}`)
	o := h.leased()
	if _, err := o.SaveAnalysis(h.t.Context(), dep.ID, p.ID, "abc123", result); err != nil {
		h.t.Fatal(err)
	}
	for _, to := range []domain.DeploymentState{domain.DeploymentFetching, domain.DeploymentAnalyzing, domain.DeploymentGenerating, domain.DeploymentBuilding,
		domain.DeploymentProvisioning, domain.DeploymentStarting, domain.DeploymentVerifying} {
		if err := o.Transition(h.t.Context(), dep.ID, to, ""); err != nil {
			h.t.Fatal(err)
		}
	}
	if _, err := o.PromoteRelease(h.t.Context(), dep.ID, store.ReleaseSpec{ImageID: "img", ContainerID: "ctr-" + slug, Health: "healthy", HostPort: 40223}); err != nil {
		h.t.Fatal(err)
	}
	if err := o.Finish(h.t.Context()); err != nil {
		h.t.Fatal(err)
	}
	return p.ID.String()
}

func djangoResult(extra ...domain.Evidence) domain.AnalysisResult {
	ev := []domain.Evidence{
		{Kind: "django.settings_module", Value: "backend.settings", Source: "manage.py", Line: 9},
		{Kind: "django.setting", Name: "AUTH", Source: "backend/settings.py", Line: 61},
	}
	return domain.AnalysisResult{Outcome: domain.AnalysisReady,
		Fields:    map[string]domain.Field{domain.FieldProfile: {Value: domain.ProfilePython, Origin: domain.OriginInference}},
		Evidences: append(ev, extra...)}
}

// Receitas (DEC-52 C): o administrador do Django pelos campos do modelo de usuário, com a
// senha só nesta execução, que nunca volta pela API.
func TestDjangoSuperuserRecipe(t *testing.T) {
	h := newHarness(t)
	h.login()
	// O modelo próprio do fec: login por e-mail, nenhum outro obrigatório.
	pid := h.withReleaseResult("fec-api", djangoResult(
		domain.Evidence{Kind: "django.setting", Name: "AUTH_USER_MODEL", Value: "CustomUser", Source: "backend/settings.py", Line: 150},
		domain.Evidence{Kind: "django.user_model", Name: "CustomUser", Value: "email|", Source: "users/models.py", Line: 40},
	))
	base := "/api/v1/projects/" + pid + "/commands"
	rec := h.do("GET", base, "")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"id":"django_superuser"`) || !strings.Contains(body, "DJANGO_SUPERUSER_EMAIL") ||
		strings.Contains(body, "DJANGO_SUPERUSER_USERNAME") || !strings.Contains(body, "users/models.py:40") {
		t.Fatalf("receitas: %d %s", rec.Code, body)
	}
	if rec := h.do("POST", "/api/v1/auth/reauth", `{"password":"`+adminPass+`"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	for name, req := range map[string]string{
		"falta a senha":      `{"recipe":"django_superuser","inputs":{"DJANGO_SUPERUSER_EMAIL":"a@b.c"}}`,
		"valor que não usa":  `{"recipe":"django_superuser","inputs":{"DJANGO_SUPERUSER_EMAIL":"a@b.c","DJANGO_SUPERUSER_PASSWORD":"x","OUTRO":"y"}}`,
		"receita que não há": `{"recipe":"laravel_admin"}`,
		"receita e comando":  `{"recipe":"django_superuser","command":"ls"}`,
		"nome inválido":      `{"command":"ls","inputs":{"minha-var":"x"}}`,
		"nome da plataforma": `{"command":"ls","inputs":{"PORT":"1"}}`,
	} {
		if rec := h.do("POST", base, req); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	const secret = "senha-do-admin-canario"
	rec = h.do("POST", base, `{"recipe":"django_superuser","inputs":{"DJANGO_SUPERUSER_EMAIL":"admin@fec.test","DJANGO_SUPERUSER_PASSWORD":"`+secret+`"}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("rodar a receita: %d %s", rec.Code, rec.Body)
	}
	cmd := decode[domain.Command](t, rec)
	if cmd.Command != "python manage.py createsuperuser --noinput" || cmd.Source != domain.CommandRecipe || strings.Join(cmd.InputNames, ",") != "DJANGO_SUPERUSER_EMAIL,DJANGO_SUPERUSER_PASSWORD" {
		t.Errorf("comando = %+v", cmd)
	}
	for _, path := range []string{base, base + "/" + cmd.ID.String()} {
		if rec := h.do("GET", path, ""); strings.Contains(rec.Body.String(), secret) {
			t.Errorf("%s devolveu o valor", path)
		}
	}
	if strings.Contains(h.logs.b.String(), secret) {
		t.Error("o valor apareceu no log")
	}
	// O worker abre os valores; ao concluir, eles somem.
	vals, err := h.st.CommandInputs(t.Context(), cmd.ID)
	if err != nil || vals["DJANGO_SUPERUSER_PASSWORD"] != secret {
		t.Fatalf("valores: %v %v", vals, err)
	}
	if err := h.st.FinishCommand(t.Context(), cmd.ID, domain.CommandSucceeded, nil, cmd.Command, "Superuser created successfully.", false, ""); err != nil {
		t.Fatal(err)
	}
	if vals, _ := h.st.CommandInputs(t.Context(), cmd.ID); len(vals) != 0 {
		t.Errorf("os valores ficaram depois de concluir: %v", vals)
	}

	// O usuário padrão: usuário e e-mail. Um modelo próprio que não se acha: nenhuma receita.
	other := h.withReleaseResult("padrao", djangoResult())
	if rec := h.do("GET", "/api/v1/projects/"+other+"/commands", ""); !strings.Contains(rec.Body.String(), "DJANGO_SUPERUSER_USERNAME") || !strings.Contains(rec.Body.String(), "DJANGO_SUPERUSER_EMAIL") {
		t.Errorf("usuário padrão: %s", rec.Body)
	}
	lost := h.withReleaseResult("sem-modelo", djangoResult(domain.Evidence{Kind: "django.setting", Name: "AUTH_USER_MODEL", Value: "Conta", Source: "s.py", Line: 1}))
	if rec := h.do("GET", "/api/v1/projects/"+lost+"/commands", ""); !strings.Contains(rec.Body.String(), `"recipes":[]`) {
		t.Errorf("modelo não achado: %s", rec.Body)
	}
}
