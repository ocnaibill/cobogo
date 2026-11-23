package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Colar o link (DEC-52): o projeto nasce lendo o repositório; confirmar a proposta cria
// as aplicações e publica o backend antes do frontend; excluir leva todas.
func TestStackFromALink(t *testing.T) {
	h := newHarness(t)
	h.login()

	for name, body := range map[string]string{
		"sem link":      `{}`,
		"link inválido": `{"repository_url":"ftp://x"}`,
		"nome inválido": `{"repository_url":"https://github.com/exemplo/fec","slug":"Fec!"}`,
	} {
		if rec := h.do("POST", "/api/v1/stacks", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	rec := h.do("POST", "/api/v1/stacks", `{"repository_url":"https://github.com/exemplo/fec"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar: %d %s", rec.Code, rec.Body)
	}
	st := decode[domain.Stack](t, rec)
	if st.Slug != "fec" || st.ScanState != domain.ScanScanning {
		t.Fatalf("stack = %+v", st)
	}
	if rec := h.do("POST", "/api/v1/stacks", `{"repository_url":"https://github.com/exemplo/fec"}`); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "slug_taken") {
		t.Errorf("nome repetido: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("POST", "/api/v1/stacks/fec/scan", ""); rec.Code != http.StatusConflict {
		t.Errorf("ler de novo durante a leitura: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("POST", "/api/v1/stacks/fec/deployments", `{"idempotency_key":"k1"}`); rec.Code != http.StatusConflict {
		t.Errorf("publicar sem aplicações: %d %s", rec.Code, rec.Body)
	}

	apps := `{"deploy":true,"idempotency_key":"primeiro","apps":[{"root_directory":"frontend","slug":"fec","role":"frontend"},{"root_directory":"","slug":"fec-api","role":"backend"}]}`
	if rec := h.do("POST", "/api/v1/stacks/fec/apps", strings.Replace(apps, `"slug":"fec-api"`, `"slug":"fec"`, 1)); rec.Code != http.StatusBadRequest {
		t.Errorf("dois endereços iguais: %d %s", rec.Code, rec.Body)
	}
	rec = h.do("POST", "/api/v1/stacks/fec/apps", apps)
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar aplicações: %d %s", rec.Code, rec.Body)
	}
	out := decode[struct {
		Stack       domain.Stack        `json:"stack"`
		Deployments []domain.Deployment `json:"deployments"`
	}](t, rec)
	if len(out.Stack.Apps) != 2 || out.Stack.Apps[0].Slug != "fec" || len(out.Deployments) != 2 {
		t.Fatalf("out = %+v", out)
	}
	byID := map[string]string{}
	for _, a := range out.Stack.Apps {
		byID[a.ID.String()] = a.Slug
	}
	if byID[out.Deployments[0].ProjectID.String()] != "fec-api" {
		t.Errorf("o backend publica primeiro: %v", out.Deployments)
	}

	// Publicar tudo de novo: um deploy por aplicação.
	rec = h.do("POST", "/api/v1/stacks/"+st.ID.String()+"/deployments", `{"idempotency_key":"segundo"}`)
	if rec.Code != http.StatusAccepted || strings.Count(rec.Body.String(), `"project_id"`) != 2 {
		t.Errorf("publicar tudo: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("GET", "/api/v1/stacks", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"fec-api"`) {
		t.Errorf("listar: %d %s", rec.Code, rec.Body)
	}

	if rec := h.do("DELETE", "/api/v1/stacks/fec", `{"confirm_slug":"outro"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("confirmação errada: %d %s", rec.Code, rec.Body)
	}
}
