package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

type persistentBody struct {
	Paths   []string `json:"paths"`
	Volumes []struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		SizeBytes *int64 `json:"size_bytes"`
		Orphan    bool   `json:"orphan"`
	} `json:"volumes"`
}

// Dados persistentes (DEC-41): o usuário declara, a API valida, lista e pede ao
// worker para apagar o que ficou sem projeto.
func TestPersistentPathsAndVolumesThroughTheAPI(t *testing.T) {
	h := newHarness(t)
	h.login()
	p := h.createProject("com-dados")
	base := "/api/v1/projects/" + p.ID.String()

	if rec := h.do("GET", base+"/persistent-paths", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"paths":[]`) {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]string{
		"missing paths": `{}`,
		"system path":   `{"paths":["/etc/app"]}`,
		"relative":      `{"paths":["data"]}`,
		"nested":        `{"paths":["/app","/app/data"]}`,
		"not json":      `oi`,
	} {
		if rec := h.do("PUT", base+"/persistent-paths", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
	rec := h.do("PUT", base+"/persistent-paths", `{"paths":["/app/data/","/app/content"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	var got persistentBody
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Paths) != 2 || got.Paths[0] != "/app/content" || got.Paths[1] != "/app/data" {
		t.Errorf("paths = %v", got.Paths)
	}
	if project := decode[domain.Project](t, h.do("GET", base, "")); len(project.PersistentPaths) != 2 {
		t.Errorf("the project must carry its paths: %+v", project.PersistentPaths)
	}
	if rec := h.do("PUT", "/api/v1/projects/00000000-0000-0000-0000-000000000000/persistent-paths", `{"paths":[]}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d, want 404", rec.Code)
	}

	// Volumes criados pelo worker aparecem, com tamanho, e em uso não se apagam.
	ctx := t.Context()
	if err := h.st.UpsertProjectVolume(ctx, "vol-api-data", p.ID, "/app/data"); err != nil {
		t.Fatal(err)
	}
	h.st.UpdateVolumeSizes(ctx, map[string]int64{"vol-api-data": 2048})
	json.Unmarshal(h.do("GET", base+"/persistent-paths", "").Body.Bytes(), &got)
	if len(got.Volumes) != 1 || got.Volumes[0].Name != "vol-api-data" || got.Volumes[0].SizeBytes == nil || *got.Volumes[0].SizeBytes != 2048 || got.Volumes[0].Orphan {
		t.Fatalf("volumes = %+v", got.Volumes)
	}
	del := func(name, confirm string) *httptest.ResponseRecorder {
		return h.do("DELETE", "/api/v1/volumes/"+name, `{"confirm_name":"`+confirm+`"}`)
	}
	if rec := del("vol-api-data", "vol-api-data"); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "volume_in_use" {
		t.Errorf("deleting a volume in use: %d %s", rec.Code, rec.Body)
	}

	// Tirar o caminho da lista deixa o volume sem projeto, e ele pode ser apagado.
	h.do("PUT", base+"/persistent-paths", `{"paths":["/app/content"]}`)
	orphans := h.do("GET", "/api/v1/volumes/orphans", "")
	if orphans.Code != http.StatusOK || !strings.Contains(orphans.Body.String(), "vol-api-data") || !strings.Contains(orphans.Body.String(), `"project_slug":"com-dados"`) {
		t.Fatalf("orphans: %d %s", orphans.Code, orphans.Body)
	}
	if rec := del("vol-api-data", "outro-nome"); rec.Code != http.StatusBadRequest || decode[apiError](t, rec).Error.Code != "confirmation_mismatch" {
		t.Errorf("wrong confirmation: %d %s", rec.Code, rec.Body)
	}
	if rec := del("nao-existe", "nao-existe"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown volume: %d, want 404", rec.Code)
	}
	rec = del("vol-api-data", "vol-api-data")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if op := decode[domain.Operation](t, rec); op.Kind != domain.OpDeleteVolume || op.State != domain.OpPending {
		t.Errorf("operation = %+v", op)
	}

	h.cookie = nil
	for _, c := range []struct{ method, path, body string }{
		{"GET", base + "/persistent-paths", ""}, {"PUT", base + "/persistent-paths", `{"paths":[]}`},
		{"GET", "/api/v1/volumes/orphans", ""}, {"DELETE", "/api/v1/volumes/x", `{"confirm_name":"x"}`},
	} {
		if rec := h.do(c.method, c.path, c.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d, want 401", c.method, c.path, rec.Code)
		}
	}
}

// Excluir o projeto leva ao worker se os dados também saem.
func TestDeleteProjectCarriesTheDataChoice(t *testing.T) {
	h := newHarness(t)
	h.login()
	keep := h.createProject("guarda-dados")
	rec := h.do("DELETE", "/api/v1/projects/"+keep.ID.String(), `{"confirm_slug":"guarda-dados"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatal(rec.Body)
	}
	if op := decode[domain.Operation](t, rec); op.Params["delete_data"] != false {
		t.Errorf("default must keep the data: %v", op.Params)
	}
	gone := h.createProject("apaga-dados")
	rec = h.do("DELETE", "/api/v1/projects/"+gone.ID.String(), `{"confirm_slug":"apaga-dados","delete_data":true}`)
	if op := decode[domain.Operation](t, rec); op.Params["delete_data"] != true {
		t.Errorf("delete_data must reach the operation: %v", op.Params)
	}
}
