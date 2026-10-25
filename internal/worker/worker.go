// Package worker conduz o pipeline de deploy (§15 do mestre): aquisição,
// análise, geração, build, início e verificação. É o componente mais
// privilegiado do sistema (§17.2) — só ele fala com o Docker.
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/ocnaibill/cobogo/internal/analyzer"
	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/gitsource"
	"github.com/ocnaibill/cobogo/internal/store"

	"github.com/google/uuid"
)

const (
	defaultLease        = 5 * time.Minute
	defaultPollInterval = 2 * time.Second
	readinessTimeout    = 90 * time.Second
)

type Worker struct {
	store   *store.Store
	runtime *dockerrt.Runtime
	log     *slog.Logger
}

func New(st *store.Store, rt *dockerrt.Runtime, log *slog.Logger) *Worker {
	return &Worker{store: st, runtime: rt, log: log}
}

// Run bloqueia reivindicando e processando um job por vez (RNF-05: um build
// simultâneo em M1), até ctx ser cancelado.
func (w *Worker) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		job, dep, err := w.store.ClaimNextJob(ctx, defaultLease)
		if err != nil {
			w.log.Error("reivindicar job", "erro", err)
			time.Sleep(defaultPollInterval)
			continue
		}
		if job == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(defaultPollInterval):
			}
			continue
		}

		w.process(ctx, job, dep)
	}
}

func (w *Worker) emit(ctx context.Context, deploymentID uuid.UUID, stage, eventType string, payload map[string]any) {
	if err := w.store.AppendEvent(ctx, deploymentID, stage, eventType, payload); err != nil {
		w.log.Error("gravar evento", "estagio", stage, "erro", err)
	}
}

func (w *Worker) fail(ctx context.Context, dep *domain.Deployment, stage string, cause error) {
	w.log.Error("deploy falhou", "deployment", dep.ID, "estagio", stage, "erro", cause)
	if err := w.store.UpdateDeploymentState(ctx, dep.ID, domain.DeploymentFailed, cause.Error()); err != nil {
		w.log.Error("registrar falha", "erro", err)
	}
	w.emit(ctx, dep.ID, stage, "failed", map[string]any{"error": cause.Error()})
}

func (w *Worker) process(ctx context.Context, job *domain.Job, dep *domain.Deployment) {
	defer func() {
		if err := w.store.MarkJobDone(ctx, job.ID); err != nil {
			w.log.Error("concluir job", "erro", err)
		}
	}()

	project, err := w.store.GetProject(ctx, dep.ProjectID)
	if err != nil {
		w.fail(ctx, dep, "FETCHING", fmt.Errorf("carregar projeto: %w", err))
		return
	}

	// --- FETCHING ---
	w.store.UpdateDeploymentState(ctx, dep.ID, domain.DeploymentFetching, "")
	w.emit(ctx, dep.ID, "fetching", "started", nil)

	ws, err := gitsource.Fetch(ctx, project.RepositoryURL, gitsource.Options{})
	if err != nil {
		w.fail(ctx, dep, "FETCHING", err)
		return
	}
	defer ws.Cleanup()

	if err := w.store.SetDeploymentCommitSHA(ctx, dep.ID, ws.CommitSHA); err != nil {
		w.fail(ctx, dep, "FETCHING", err)
		return
	}
	dep.CommitSHA = ws.CommitSHA
	w.emit(ctx, dep.ID, "fetching", "completed", map[string]any{"commit_sha": ws.CommitSHA})

	// --- ANALYZING ---
	w.store.UpdateDeploymentState(ctx, dep.ID, domain.DeploymentAnalyzing, "")
	rootDir := filepath.Join(ws.Dir, project.RootDirectory)

	result, err := analyzer.Analyze(rootDir, dep.PortOverride)
	if err != nil {
		w.fail(ctx, dep, "ANALYZING", err)
		return
	}
	if _, err := w.store.SaveAnalysis(ctx, dep.ID, dep.ProjectID, ws.CommitSHA, result); err != nil {
		w.fail(ctx, dep, "ANALYZING", err)
		return
	}
	w.emit(ctx, dep.ID, "analyzing", "completed", map[string]any{
		"outcome":   result.Outcome,
		"evidences": result.Evidences,
		"pending":   result.Pending,
		"conflicts": result.Conflicts,
	})

	switch result.Outcome {
	case domain.AnalysisUnsupported:
		w.store.UpdateDeploymentState(ctx, dep.ID, domain.DeploymentUnsupported, "perfil não suportado pelas evidências disponíveis")
		return
	case domain.AnalysisNeedsInput:
		// RF-08: a pendência não ocupa o worker. M1 ainda não oferece
		// correção no mesmo SHA (RF-08 completo); é preciso criar um novo
		// deploy informando o campo pendente como override.
		w.store.UpdateDeploymentState(ctx, dep.ID, domain.DeploymentNeedsInput, "")
		return
	}

	plan := result.Plan
	if plan == nil {
		w.fail(ctx, dep, "GENERATING", fmt.Errorf("análise pronta sem plano, estado inconsistente"))
		return
	}

	// --- GENERATING ---
	w.store.UpdateDeploymentState(ctx, dep.ID, domain.DeploymentGenerating, "")
	w.emit(ctx, dep.ID, "generating", "completed", map[string]any{
		"profile":         plan.Profile,
		"runtime":         plan.Runtime,
		"install_command": plan.InstallCommand,
		"build_command":   plan.BuildCommand,
		"start_command":   plan.StartCommand,
		"port":            plan.Port,
		"port_origin":     plan.PortOrigin,
	})

	shortSHA := ws.CommitSHA
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	imageTag := fmt.Sprintf("cobogo/%s:%s", project.Slug, shortSHA)
	containerName := fmt.Sprintf("cobogo-%s-%s", project.Slug, shortSHA)

	// --- BUILDING ---
	w.store.UpdateDeploymentState(ctx, dep.ID, domain.DeploymentBuilding, "")
	buildResult, err := w.runtime.Build(ctx, rootDir, plan.Dockerfile, imageTag, plan.Runtime, func(line string) {
		w.emit(ctx, dep.ID, "building", "log", map[string]any{"line": line})
	})
	if err != nil {
		w.fail(ctx, dep, "BUILDING", err)
		return
	}
	w.emit(ctx, dep.ID, "building", "completed", map[string]any{"image_id": buildResult.ImageID, "image_tag": buildResult.ImageTag})

	// --- PROVISIONING --- (sem serviços auxiliares em M1)
	w.store.UpdateDeploymentState(ctx, dep.ID, domain.DeploymentProvisioning, "")
	w.emit(ctx, dep.ID, "provisioning", "completed", map[string]any{"services": []string{}})

	// --- STARTING ---
	w.store.UpdateDeploymentState(ctx, dep.ID, domain.DeploymentStarting, "")
	runResult, err := w.runtime.Run(ctx, imageTag, containerName, plan.Port, map[string]string{"PORT": fmt.Sprint(plan.Port)})
	if err != nil {
		w.fail(ctx, dep, "STARTING", err)
		return
	}
	w.emit(ctx, dep.ID, "starting", "completed", map[string]any{"container_id": runResult.ContainerID, "host_port": runResult.HostPort})

	// --- VERIFYING ---
	w.store.UpdateDeploymentState(ctx, dep.ID, domain.DeploymentVerifying, "")
	if err := dockerrt.ProbeReady(ctx, runResult.HostPort, "/", 200, readinessTimeout); err != nil {
		_ = w.runtime.Remove(ctx, runResult.ContainerID)
		w.fail(ctx, dep, "VERIFYING", err)
		return
	}

	if _, err := w.store.CreateRelease(ctx, dep.ID, buildResult.ImageID, runResult.ContainerID, "active", "healthy", runResult.HostPort); err != nil {
		w.fail(ctx, dep, "VERIFYING", err)
		return
	}
	w.store.UpdateDeploymentState(ctx, dep.ID, domain.DeploymentSucceeded, "")
	w.emit(ctx, dep.ID, "verifying", "succeeded", map[string]any{
		"url": fmt.Sprintf("http://127.0.0.1:%d/", runResult.HostPort),
	})
}
