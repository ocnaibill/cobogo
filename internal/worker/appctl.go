package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/planner"
	"github.com/ocnaibill/cobogo/internal/store"
)

// Parar, iniciar e reiniciar a release ativa (DEC-40). O estado vem do banco
// (`stopped_at`) e vale sobre qualquer coisa que o Docker diga depois de um
// reinício da pilha.

// errStartNotReady: a aplicação iniciou mas não ficou pronta no prazo. Tentar de
// novo igual não ajuda, então a operação falha de vez, com o motivo.
var errStartNotReady = errors.New("a aplicação não ficou pronta depois de iniciar")

// activeFor lê o projeto e a release ativa de uma operação. Sem release no ar,
// o desfecho é definitivo: não adianta tentar de novo.
func (w *Worker) activeFor(ctx context.Context, op *domain.Operation) (domain.Project, domain.Release, error) {
	projectID, err := opProject(op)
	if err != nil {
		return domain.Project{}, domain.Release{}, err
	}
	project, err := w.store.GetProject(ctx, projectID)
	if err != nil {
		return domain.Project{}, domain.Release{}, err
	}
	if project.DeletedAt != nil {
		return domain.Project{}, domain.Release{}, errors.New("o projeto foi excluído")
	}
	active, err := w.store.ActiveRelease(ctx, projectID)
	if errors.Is(err, store.ErrNoActiveRelease) {
		return domain.Project{}, domain.Release{}, errNoActiveRelease
	}
	if err != nil {
		return domain.Project{}, domain.Release{}, err
	}
	return project, active, nil
}

// stopApp marca a aplicação como parada (a rota passa a mostrar a página de
// inativo) e para o contêiner, mantendo-o. Se o Docker falhar, desfaz a marca:
// o painel não pode dizer "parado" de uma aplicação que segue no ar.
func (w *Worker) stopApp(ctx context.Context, op *domain.Operation) (map[string]any, error) {
	_, active, err := w.activeFor(ctx, op)
	if err != nil {
		return nil, err
	}
	if err := w.store.SetAppStopped(ctx, active.ID, true); err != nil {
		return nil, err
	}
	if err := w.runtime.StopContainer(ctx, active.ContainerID); err != nil {
		if rerr := w.store.SetAppStopped(context.WithoutCancel(ctx), active.ID, false); rerr != nil {
			w.log.Warn("desfazer a marca de parada", "release", active.ID, "erro", rerr)
		}
		return nil, err
	}
	w.event(ctx, active, "stopped")
	return map[string]any{"container": shortContainer(active.ContainerID), "state": domain.AppStopped}, nil
}

// startApp inicia o contêiner mantido, espera a prontidão como um deploy faria
// e só então tira a marca de parada (a rota volta). Se não ficar pronto no
// prazo, o projeto continua parado e a operação falha, dizendo por quê.
func (w *Worker) startApp(ctx context.Context, op *domain.Operation) (map[string]any, error) {
	project, active, err := w.activeFor(ctx, op)
	if err != nil {
		return nil, err
	}
	port, healthPath := w.portAndHealthOf(ctx, active)

	started, err := w.runtime.StartContainer(ctx, active.ContainerID, port)
	if err != nil {
		return nil, err
	}
	if err := w.waitReady(ctx, project, active, started, port, healthPath); err != nil {
		// Não deixa a aplicação meio no ar: o projeto continua parado.
		if serr := w.runtime.StopContainer(context.WithoutCancel(ctx), active.ContainerID); serr != nil {
			w.log.Warn("parar a aplicação que não ficou pronta", "release", active.ID, "erro", serr)
		}
		w.event(ctx, active, "start_failed")
		return map[string]any{"container": shortContainer(active.ContainerID), "state": domain.AppStopped}, fmt.Errorf("%w: %v", errStartNotReady, err)
	}
	if started.HostPort != active.HostPort {
		if err := w.store.UpdateReleaseHostPort(ctx, active.ID, started.HostPort); err != nil {
			return nil, err
		}
	}
	if err := w.store.SetAppStopped(ctx, active.ID, false); err != nil {
		return nil, err
	}
	w.event(ctx, active, "started")
	return map[string]any{"container": shortContainer(active.ContainerID), "state": domain.AppRunning, "host_port": started.HostPort}, nil
}

// restartApp é parar e iniciar. Não relê variáveis nem limites: o contêiner é o
// mesmo (um novo deploy relê as variáveis; `apply_limits` muda os limites).
func (w *Worker) restartApp(ctx context.Context, op *domain.Operation) (map[string]any, error) {
	if _, err := w.stopApp(ctx, op); err != nil {
		return nil, err
	}
	return w.startApp(ctx, op)
}

// portAndHealthOf lê a porta e a rota de saúde do plano do deploy da release.
// Sem plano legível, usa a porta de roteamento da release e a raiz.
func (w *Worker) portAndHealthOf(ctx context.Context, active domain.Release) (int, string) {
	port, health := 0, "/"
	if dep, err := w.store.GetDeployment(ctx, active.DeploymentID); err == nil && dep.AnalysisID != nil {
		if a, err := w.store.GetAnalysis(ctx, *dep.AnalysisID); err == nil {
			if plan, err := planner.FromAnalysis(a.Result); err == nil {
				port, health = plan.Port, plan.HealthPath
			}
		}
	}
	return port, health
}

// waitReady espera a aplicação responder, pela mesma sonda do deploy.
func (w *Worker) waitReady(ctx context.Context, project domain.Project, active domain.Release, started dockerrt.Started, port int, healthPath string) error {
	alive := func(c context.Context) (bool, int, error) { return w.runtime.State(c, active.ContainerID) }
	if !w.probeNetwork {
		_, err := dockerrt.ProbeReady(ctx, started.HostPort, healthPath, readinessTimeout, alive)
		return err
	}
	// Dentro de um contêiner o worker só alcança a aplicação pela rede do
	// projeto, e fica ligado a ela apenas enquanto dura a sonda.
	self, err := dockerrt.SelfID()
	if err != nil {
		return fmt.Errorf("identificar o contêiner do worker: %w", err)
	}
	net := dockerrt.ProjectNetwork(project.ID.String())
	if err := w.runtime.ConnectContainer(ctx, self, net); err != nil {
		return err
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
		defer done()
		if err := w.runtime.DisconnectContainer(cleanup, self, net); err != nil {
			w.log.Warn("desconectar o worker da rede do projeto", "rede", net, "erro", err)
		}
	}()
	_, err = dockerrt.ProbeReadyAt(ctx, started.Name, port, healthPath, readinessTimeout, alive)
	return err
}

func (w *Worker) event(ctx context.Context, active domain.Release, kind string) {
	if err := w.store.AppendEvent(ctx, active.DeploymentID, "lifecycle", kind, map[string]any{"container": shortContainer(active.ContainerID)}); err != nil {
		w.log.Warn("registrar a operação no histórico do deploy", "deploy", active.DeploymentID, "erro", err)
	}
}

func shortContainer(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
