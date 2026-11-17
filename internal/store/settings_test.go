package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

func TestSettingsRoundTrip(t *testing.T) {
	st := storetest.New(t)
	me := owner(t, st)

	if m, err := st.ListSettings(ctx); err != nil || len(m) != 0 {
		t.Fatalf("fresh database = %v, %v", m, err)
	}
	if err := st.PutSetting(ctx, "app_memory_mb", "2048", me.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSetting(ctx, "app_memory_mb", "3072", me.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSetting(ctx, "extra_git_hosts", "", me.ID); err != nil {
		t.Fatal(err)
	}
	m, err := st.ListSettings(ctx)
	if err != nil || m["app_memory_mb"] != "3072" || len(m) != 2 {
		t.Fatalf("settings = %v, %v", m, err)
	}
	if v, ok := m["extra_git_hosts"]; !ok || v != "" {
		t.Errorf("an empty stored list must be kept as an explicit override: %v", m)
	}

	if existed, err := st.DeleteSetting(ctx, "app_memory_mb"); err != nil || !existed {
		t.Errorf("delete = %v, %v", existed, err)
	}
	if existed, err := st.DeleteSetting(ctx, "app_memory_mb"); err != nil || existed {
		t.Errorf("deleting twice must be harmless: %v, %v", existed, err)
	}
	if m, _ := st.ListSettings(ctx); len(m) != 1 {
		t.Errorf("after delete = %v", m)
	}
}

func TestProjectLimitsRoundTripAndOwnerScoping(t *testing.T) {
	st := storetest.New(t)
	me := owner(t, st)
	p := newProject(t, st)

	got, err := st.GetProject(ctx, p.ID)
	if err != nil || got.Limits != (domain.Limits{}) {
		t.Fatalf("a new project inherits everything: %+v, %v", got.Limits, err)
	}

	want := domain.Limits{AppMemoryMB: 2048, AppCPUs: 0.5, BuildMemoryMB: 8192}
	updated, err := st.SetProjectLimits(ctx, p.ID, me.ID, want)
	if err != nil || updated.Limits != want {
		t.Fatalf("SetProjectLimits = %+v, %v", updated.Limits, err)
	}
	if got, _ := st.GetProjectFor(ctx, p.ID, me.ID); got.Limits != want {
		t.Errorf("stored = %+v", got.Limits)
	}
	items, _, err := st.ListProjectsFor(ctx, me.ID, 10, nil)
	if err != nil || len(items) != 1 || items[0].Limits != want {
		t.Errorf("the list must carry the limits: %+v, %v", items, err)
	}

	// A troca substitui tudo: um objeto vazio limpa.
	cleared, err := st.SetProjectLimits(ctx, p.ID, me.ID, domain.Limits{})
	if err != nil || cleared.Limits != (domain.Limits{}) {
		t.Errorf("cleared = %+v, %v", cleared.Limits, err)
	}

	// Outro dono não mexe, e o erro não revela que o projeto existe.
	if _, err := st.SetProjectLimits(ctx, p.ID, uuid.New(), want); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another owner: %v, want ErrNotFound", err)
	}
	if got, _ := st.GetProject(ctx, p.ID); got.Limits != (domain.Limits{}) {
		t.Errorf("a refused update must change nothing: %+v", got.Limits)
	}
}
