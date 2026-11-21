package worker

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

// A regra de elegibilidade decide o que o worker pode remover (DEC-38): cada
// linha é um caso em que errar apaga uma aplicação no ar ou deixa lixo.
func TestRetirable(t *testing.T) {
	now := time.Now()
	grace := 30 * time.Second
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	container := dockerrt.ManagedContainer{ID: "old-container"}

	tests := []struct {
		name string
		fact store.ContainerFact
		want bool
	}{
		{"the active release is never removed", store.ContainerFact{DeploymentState: domain.DeploymentSucceeded, ActiveContainer: "old-container", ActiveSince: ago(time.Hour)}, false},
		{"a superseded release inside the grace stays (the router is still switching)", store.ContainerFact{DeploymentState: domain.DeploymentSucceeded, ActiveContainer: "new-container", ActiveSince: ago(10 * time.Second)}, false},
		{"a superseded release after the grace goes", store.ContainerFact{DeploymentState: domain.DeploymentSucceeded, ActiveContainer: "new-container", ActiveSince: ago(time.Minute)}, true},
		{"a candidate of a deploy in progress is never removed", store.ContainerFact{DeploymentState: domain.DeploymentVerifying, ActiveContainer: "new-container", ActiveSince: ago(time.Hour)}, false},
		{"a deploy waiting in the queue is not terminal", store.ContainerFact{DeploymentState: domain.DeploymentQueued, ActiveContainer: "new-container", ActiveSince: ago(time.Hour)}, false},
		{"a failed deploy with no active release goes after the grace", store.ContainerFact{DeploymentState: domain.DeploymentFailed, DeploymentUpdate: now.Add(-time.Minute)}, true},
		{"a failed deploy that just ended stays (its worker is cleaning up)", store.ContainerFact{DeploymentState: domain.DeploymentFailed, DeploymentUpdate: now.Add(-5 * time.Second)}, false},
		{"a cancelled deploy is terminal too", store.ContainerFact{DeploymentState: domain.DeploymentCancelled, DeploymentUpdate: now.Add(-time.Hour)}, true},
		{"a deleted project goes at once", store.ContainerFact{DeploymentState: domain.DeploymentSucceeded, ProjectDeleted: true, ActiveContainer: "old-container", ActiveSince: ago(time.Second)}, true},
	}
	for _, tt := range tests {
		got, reason := retirable(container, tt.fact, now, grace)
		if got != tt.want {
			t.Errorf("%s: retirable = %v (%q), want %v", tt.name, got, reason, tt.want)
		}
		if got && reason == "" {
			t.Errorf("%s: a removal must say why", tt.name)
		}
	}
}

// Um contêiner com o label do Cobogó de um deploy que este banco não conhece é
// de outra instalação no mesmo Docker: nunca é tocado.
func TestRetireEligibleIgnoresWhatTheDatabaseDoesNotKnow(t *testing.T) {
	w := &Worker{}
	containers := []dockerrt.ManagedContainer{
		{ID: "foreign-install", Name: "cobogo-x", DeploymentID: uuid.New().String()},
		{ID: "malformed", Name: "cobogo-y", DeploymentID: "not-a-uuid"},
		{ID: "no-label-value", Name: "cobogo-z"},
	}
	gone, ids, err := w.retireEligible(context.Background(), containers, map[uuid.UUID]store.ContainerFact{}, 0)
	if err != nil || len(gone) != 0 || len(ids) != 0 {
		t.Errorf("gone = %v, ids = %v, err = %v; want nothing removed", gone, ids, err)
	}
}
