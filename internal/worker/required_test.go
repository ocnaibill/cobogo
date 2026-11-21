package worker

import (
	"errors"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/diagnosis"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// Variável obrigatória sem valor falha o deploy cedo (DEC-39 item 4), com a
// lista do que falta num diagnóstico confirmado e sem ler nenhum valor.
func TestCheckRequiredEnvFailsEarlyWithTheMissingNames(t *testing.T) {
	ctx := t.Context()
	st := storetest.New(t)
	admin, err := st.CreateAdmin(ctx, "dono", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, admin.ID, "exigente", "https://github.com/x/y", "")
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{store: st}

	if err := w.checkRequiredEnv(ctx, p.ID); err != nil {
		t.Fatalf("nothing required: %v", err)
	}
	if _, err := st.SetRequiredEnv(ctx, p.ID, admin.ID, []string{"DATABASE_URL", "API_KEY", "TOKEN"}); err != nil {
		t.Fatal(err)
	}
	const canary = "canario-obrigatoria-83d0"
	if _, err := st.PutEnvVariable(ctx, p.ID, "TOKEN", canary, domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}

	err = w.checkRequiredEnv(ctx, p.ID)
	var de *detailedError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v, want a detailedError", err)
	}
	if de.detail.Diagnosis.Confidence != diagnosis.Confirmed {
		t.Errorf("confidence = %q, want confirmed", de.detail.Diagnosis.Confidence)
	}
	if got := strings.Join(de.detail.Diagnosis.Variables, ","); got != "API_KEY,DATABASE_URL" {
		t.Errorf("missing = %q, want API_KEY,DATABASE_URL (TOKEN is set)", got)
	}
	if strings.Contains(err.Error(), canary) || strings.Contains(de.detail.Diagnosis.Message, canary) {
		t.Error("the failure repeats a stored value")
	}
	// O estágio é o da análise: o build nem começou.
	var se *stageError
	if !errors.As(at("ANALYZING", err), &se) || se.stage != "ANALYZING" {
		t.Errorf("stage = %#v", se)
	}

	for _, name := range []string{"DATABASE_URL", "API_KEY"} {
		if _, err := st.PutEnvVariable(ctx, p.ID, name, "x", domain.EnvSourceUser); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.checkRequiredEnv(ctx, p.ID); err != nil {
		t.Errorf("all defined: %v", err)
	}
}
