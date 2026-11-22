package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

type cmdBody struct {
	ID       string `json:"id"`
	Command  string `json:"command"`
	Source   string `json:"source"`
	State    string `json:"state"`
	PauseApp bool   `json:"pause_app"`
	Commands []struct {
		ID      string `json:"id"`
		Output  string `json:"output"`
		Command string `json:"command"`
	} `json:"commands"`
	Scripts []struct {
		Name, Command, Runs string
	} `json:"scripts"`
	Available   bool    `json:"available"`
	Reason      string  `json:"reason"`
	ReauthUntil *string `json:"reauth_until"`
}

// withRelease leva um projeto novo a ter uma análise com scripts e uma release ativa.
func (h *harness) withRelease(slug string, profile string) string {
	h.t.Helper()
	p := h.createProject(slug)
	dep := h.deployment(p.ID.String(), `{"idempotency_key":"c-`+slug+`"}`)
	o := h.leased()
	script := func(name, value string) domain.Evidence {
		return domain.Evidence{Kind: "npm.script", Name: name, Value: value, Source: "package.json#scripts." + name}
	}
	result := domain.AnalysisResult{Outcome: domain.AnalysisReady,
		Fields: map[string]domain.Field{
			domain.FieldProfile:        {Value: profile, Origin: domain.OriginInference},
			domain.FieldPackageManager: {Value: domain.ManagerBun, Origin: domain.OriginInference},
		},
		Evidences: []domain.Evidence{
			script("reindex", "bun run src/reindex.ts"), script("migrations:up", "node-pg-migrate up"), script("start", "bun run src/index.ts"),
			script("dev", "bun --watch src/index.ts"), script("test", "bun test"), script("postinstall", "echo"), script("com espaço", "x"),
			script("seed", "bun run src/seed.ts"),
		}}
	if _, err := o.SaveAnalysis(h.t.Context(), dep.ID, p.ID, "abc123", result); err != nil {
		h.t.Fatal(err)
	}
	for _, to := range []domain.DeploymentState{domain.DeploymentFetching, domain.DeploymentAnalyzing, domain.DeploymentGenerating, domain.DeploymentBuilding,
		domain.DeploymentProvisioning, domain.DeploymentStarting, domain.DeploymentVerifying} {
		if err := o.Transition(h.t.Context(), dep.ID, to, ""); err != nil {
			h.t.Fatal(err)
		}
	}
	if _, err := o.PromoteRelease(h.t.Context(), dep.ID, store.ReleaseSpec{ImageID: "img", ContainerID: "ctr-" + slug, Health: "healthy", HostPort: 40222}); err != nil {
		h.t.Fatal(err)
	}
	if err := o.Finish(h.t.Context()); err != nil {
		h.t.Fatal(err)
	}
	return p.ID.String()
}

// Comandos pontuais (DEC-45): nada se enfileira sem a senha digitada de novo, que
// vale por sessão; os botões vêm dos scripts, o campo livre tem limites.
func TestCommandsNeedTheReauthWindowAndValidateTheirInput(t *testing.T) {
	h := newHarness(t)
	h.login()
	pid := h.withRelease("comandos", domain.ProfileBun)
	base := "/api/v1/projects/" + pid + "/commands"

	// A lista oferece só scripts pontuais, e diz que a janela não está aberta.
	list := decode[cmdBody](t, h.do("GET", base, ""))
	names := []string{}
	for _, s := range list.Scripts {
		names = append(names, s.Name+"="+s.Command)
	}
	if got := strings.Join(names, ","); got != "migrations:up=bun run migrations:up,reindex=bun run reindex,seed=bun run seed" {
		t.Errorf("scripts = %s", got)
	}
	if !list.Available || list.ReauthUntil != nil || len(list.Commands) != 0 {
		t.Errorf("list = %+v", list)
	}

	// Sem a janela: recusado antes de qualquer outra coisa, e auditado.
	for _, body := range []string{`{"script":"reindex"}`, `{"command":"ls"}`, `{}`} {
		rec := h.do("POST", base, body)
		if rec.Code != http.StatusForbidden || decode[apiError](t, rec).Error.Code != "reauth_required" {
			t.Errorf("%s without the window: %d %s", body, rec.Code, rec.Body)
		}
	}

	// A senha errada não abre a janela; a certa abre.
	if rec := h.do("POST", "/api/v1/auth/reauth", `{"password":"errada-errada-errada"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("POST", base, `{"command":"ls"}`); rec.Code != http.StatusForbidden {
		t.Errorf("a failed reauth must not open the window: %d", rec.Code)
	}
	if rec := h.do("POST", "/api/v1/auth/reauth", `{"password":"`+adminPass+`"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "reauth_until") {
		t.Fatalf("reauth: %d %s", rec.Code, rec.Body)
	}
	if list := decode[cmdBody](t, h.do("GET", base, "")); list.ReauthUntil == nil {
		t.Error("the list must show the open window")
	}

	// Validação: um dos dois, script oferecido, tamanho e prazo.
	for name, body := range map[string]string{
		"neither":            `{}`,
		"both":               `{"script":"reindex","command":"ls"}`,
		"unknown script":     `{"script":"inexistente"}`,
		"excluded script":    `{"script":"start"}`,
		"unsafe script name": `{"script":"com espaço"}`,
		"too long":           `{"command":"` + strings.Repeat("a", 2001) + `"}`,
		"timeout too short":  `{"command":"ls","timeout_seconds":1}`,
		"timeout too long":   `{"command":"ls","timeout_seconds":601}`,
		"not json":           `oi`,
	} {
		if rec := h.do("POST", base, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}

	// Um botão vira o comando do script; um por vez.
	rec := h.do("POST", base, `{"script":"reindex","pause_app":true}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("script: %d %s", rec.Code, rec.Body)
	}
	first := decode[cmdBody](t, rec)
	if first.Command != "bun run reindex" || first.Source != "script" || !first.PauseApp || first.State != "queued" {
		t.Errorf("created = %+v", first)
	}
	if rec := h.do("POST", base, `{"command":"ls"}`); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "command_active" {
		t.Errorf("a second command: %d %s", rec.Code, rec.Body)
	}

	// Cancelar marca o pedido; o comando se lê, com e sem a saída.
	if rec := h.do("POST", base+"/"+first.ID+"/cancel", ""); rec.Code != http.StatusAccepted {
		t.Errorf("cancel: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("GET", base+"/"+first.ID, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cancel_requested":true`) {
		t.Errorf("get: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("GET", base+"/00000000-0000-0000-0000-000000000000", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown command: %d", rec.Code)
	}
	if rec := h.do("GET", base+"/nao-e-uuid", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", rec.Code)
	}

	// A janela é da sessão: outra sessão do mesmo usuário não a tem.
	h.signIn(adminLogin, adminPass)
	if rec := h.do("POST", base, `{"command":"ls"}`); rec.Code != http.StatusForbidden {
		t.Errorf("a new session must not inherit the window: %d %s", rec.Code, rec.Body)
	}
}

func TestCommandsAreUnavailableWithoutAReleaseOrForAStaticSite(t *testing.T) {
	h := newHarness(t)
	h.login()
	reauth := func() {
		if rec := h.do("POST", "/api/v1/auth/reauth", `{"password":"`+adminPass+`"}`); rec.Code != http.StatusOK {
			t.Fatalf("reauth: %d", rec.Code)
		}
	}

	// Sem release.
	bare := h.createProject("sem-release")
	base := "/api/v1/projects/" + bare.ID.String() + "/commands"
	if v := decode[cmdBody](t, h.do("GET", base, "")); v.Available || !strings.Contains(v.Reason, "release") {
		t.Errorf("without a release: %+v", v)
	}
	reauth()
	if rec := h.do("POST", base, `{"command":"ls"}`); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "commands_unavailable" {
		t.Errorf("without a release: %d %s", rec.Code, rec.Body)
	}

	// Site estático.
	static := h.withRelease("estatico", domain.ProfileStatic)
	sbase := "/api/v1/projects/" + static + "/commands"
	if v := decode[cmdBody](t, h.do("GET", sbase, "")); v.Available || !strings.Contains(v.Reason, "estático") {
		t.Errorf("static site: %+v", v)
	}
	if rec := h.do("POST", sbase, `{"command":"ls"}`); rec.Code != http.StatusConflict {
		t.Errorf("static site: %d %s", rec.Code, rec.Body)
	}

	// Outro dono não vê nem enfileira.
	if rec := h.do("GET", "/api/v1/projects/00000000-0000-0000-0000-000000000000/commands", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d", rec.Code)
	}
}
