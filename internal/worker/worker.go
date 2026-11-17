// Package worker conduz o pipeline de deploy (§15 do mestre): aquisição,
// análise, geração, build, início e verificação. É o componente mais
// privilegiado do sistema (§17.2) — só ele fala com o Docker.
//
// Posse e retomada (DEC-26): o worker mantém o lease do job com heartbeat, só
// grava sobre o deploy enquanto for o dono, devolve o job à fila ao ser
// desligado e retoma, do início e sobre o mesmo SHA, o que um worker anterior
// deixou pela metade.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ocnaibill/cobogo/internal/analyzer"
	"github.com/ocnaibill/cobogo/internal/diagnosis"
	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/gitsource"
	"github.com/ocnaibill/cobogo/internal/planner"
	"github.com/ocnaibill/cobogo/internal/redact"
	"github.com/ocnaibill/cobogo/internal/settings"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/validate"

	"github.com/google/uuid"
)

const (
	leaseDuration       = 60 * time.Second // RNF-07
	heartbeatInterval   = 15 * time.Second // RNF-07
	maxAttempts         = 3
	buildTimeout        = 10 * time.Minute  // RNF-05
	readinessTimeout    = 120 * time.Second // RNF-05
	logTailLines        = 50
	logTailBytes        = 8 << 10
	defaultPollInterval = 2 * time.Second
	releaseTimeout      = 10 * time.Second
	warmUpTimeout       = 30 * time.Minute
)

type Worker struct {
	store   *store.Store
	runtime *dockerrt.Runtime
	log     *slog.Logger
	// settings resolve a política operacional (limites e hosts de repositório)
	// a cada deploy, então uma mudança no painel vale sem reiniciar (DEC-30).
	settings *settings.Resolver
}

// New monta o worker. O resolvedor de configurações pode ser nil: sem ele só
// valem o ambiente vazio e os padrões de fábrica.
func New(st *store.Store, rt *dockerrt.Runtime, log *slog.Logger, cfg *settings.Resolver) *Worker {
	if cfg == nil {
		cfg = settings.NewResolver(st, settings.Env{})
	}
	return &Worker{store: st, runtime: rt, log: log, settings: cfg}
}

// Run bloqueia reivindicando e processando um job por vez (RNF-05: um build
// simultâneo em M1), até ctx ser cancelado.
func (w *Worker) Run(ctx context.Context) error {
	go w.warmUp(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		job, dep, err := w.store.ClaimNextJob(ctx, leaseDuration, maxAttempts)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
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

// warmUp baixa e atualiza as imagens do catálogo em segundo plano, para o
// primeiro build de cada versão não gastar o prazo no download e para as
// correções das imagens base chegarem sem depender do registry a cada build
// (DEC-26). Falhas só são registradas: o build usa o que já está no cache.
func (w *Worker) warmUp(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, warmUpTimeout)
	defer cancel()
	for _, ref := range planner.CatalogImages() {
		if err := w.runtime.PullImage(ctx, ref); err != nil {
			if ctx.Err() != nil {
				return
			}
			w.log.Warn("baixar imagem do catálogo", "imagem", ref, "erro", err)
		}
	}
	w.log.Info("imagens do catálogo prontas")
}

// containerNameFor dá a cada deploy um nome próprio de contêiner. Com slug e SHA
// apenas, dois deploys do mesmo projeto e commit colidiriam; com o deploy no
// nome, uma release anterior convive com a candidata até a atualização segura
// (RF-15) decidir o que aposentar.
func containerNameFor(slug string, deploymentID uuid.UUID) string {
	return fmt.Sprintf("cobogo-%s-%s", slug, deploymentID.String()[:8])
}

// stageError liga uma falha ao estágio do pipeline em que ocorreu.
type stageError struct {
	stage string
	err   error
}

func (e *stageError) Error() string { return e.err.Error() }
func (e *stageError) Unwrap() error { return e.err }

func at(stage string, err error) error {
	if err == nil {
		return nil
	}
	return &stageError{stage: stage, err: err}
}

// keepLease renova o lease a cada interval enquanto ctx não terminar. Se a
// renovação revelar que a posse foi perdida, cancela o pipeline com essa
// causa; falhas transitórias só são registradas e tentadas de novo. A função
// devolvida para o heartbeat e espera a goroutine encerrar.
func keepLease(ctx context.Context, cancel context.CancelCauseFunc, interval time.Duration, renew func(context.Context) error, log *slog.Logger) (stop func()) {
	hbCtx, stopCtx := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				switch err := renew(hbCtx); {
				case err == nil:
				case errors.Is(err, store.ErrLeaseLost):
					cancel(store.ErrLeaseLost)
					return
				case hbCtx.Err() == nil:
					log.Warn("renovar lease", "erro", err)
				}
			}
		}
	}()
	return func() {
		stopCtx()
		<-done
	}
}

// emit grava um evento. Só a perda de posse interrompe o pipeline; outras
// falhas de gravação do evento são registradas em log.
func (w *Worker) emit(ctx context.Context, o *store.Owned, deploymentID uuid.UUID, stage, eventType string, payload map[string]any) error {
	err := o.AppendEvent(ctx, deploymentID, stage, eventType, payload)
	if errors.Is(err, store.ErrLeaseLost) {
		return err
	}
	if err != nil && ctx.Err() == nil {
		w.log.Error("gravar evento", "estagio", stage, "erro", err)
	}
	return nil
}

// process executa um job reivindicado e decide o destino dele: concluído,
// falho, devolvido à fila (desligamento) ou abandonado (posse perdida).
func (w *Worker) process(ctx context.Context, job *domain.Job, dep *domain.Deployment) {
	owned := w.store.Owned(store.LeaseOf(job))

	pctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := keepLease(pctx, cancel, heartbeatInterval, func(c context.Context) error {
		return owned.Renew(c, leaseDuration)
	}, w.log)

	err := w.pipeline(pctx, owned, job, dep)
	stop()

	// Gravações finais não podem depender de ctx: no desligamento ele já foi
	// cancelado, e é justamente quando o job precisa voltar à fila.
	final, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer done()

	switch {
	case errors.Is(err, store.ErrLeaseLost), errors.Is(context.Cause(pctx), store.ErrLeaseLost):
		// Outro worker é o dono agora: nada mais é gravado por aqui.
		w.log.Warn("posse do job perdida; abandonando sem gravar", "deployment", dep.ID)
	case err == nil:
		w.finish(final, owned)
	case ctx.Err() != nil:
		// Desligamento: o job volta à fila sem contar a tentativa.
		w.log.Info("desligando: devolvendo o job à fila", "deployment", dep.ID)
		if rerr := owned.Release(final); rerr != nil && !errors.Is(rerr, store.ErrLeaseLost) {
			w.log.Error("devolver job à fila", "erro", rerr)
		}
	default:
		w.fail(final, owned, dep, err)
		w.finish(final, owned)
	}
}

func (w *Worker) finish(ctx context.Context, o *store.Owned) {
	if err := o.Finish(ctx); err != nil && !errors.Is(err, store.ErrLeaseLost) {
		w.log.Error("concluir job", "erro", err)
	}
}

// failureDetail é o que se sabe sobre uma aplicação que não subiu: o final do
// log (já redigido), o código de saída e o diagnóstico (DEC-26 itens 11 e 12).
type failureDetail struct {
	LogTail   string
	ExitCode  *int
	OOMKilled bool
	Diagnosis diagnosis.Diagnosis
}

// detailedError é uma falha de estágio acompanhada de failureDetail.
type detailedError struct {
	err    error
	detail failureDetail
}

func (e *detailedError) Error() string { return e.err.Error() }
func (e *detailedError) Unwrap() error { return e.err }

// investigate junta log e diagnóstico de um contêiner que não ficou no ar.
// projectValues são os valores das variáveis do projeto, removidos do log antes
// de qualquer gravação (RN-11); inventory e have vêm da análise e do projeto.
func (w *Worker) investigate(ctx context.Context, rt *dockerrt.Runtime, limits domain.Limits, containerID string, cause error, projectValues []string, inventory []domain.ConfigVariable, have map[string]string) failureDetail {
	ictx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()

	var detail failureDetail
	var exited *dockerrt.ExitedError
	if errors.As(cause, &exited) {
		code := exited.ExitCode
		detail.ExitCode = &code
	}

	raw, err := w.runtime.LogTail(ictx, containerID, logTailLines)
	if err != nil {
		w.log.Warn("ler log do contêiner", "erro", err)
	}
	redacted := redact.Values(redact.Clean(raw), projectValues)
	detail.LogTail = redact.Tail(redacted, logTailBytes)
	detail.Diagnosis = diagnosis.Diagnose(redacted, diagnosis.Unset(inventory, have), detail.ExitCode != nil)

	// Falta de memória é a causa mais exata de todas: o daemon a informa, e ela
	// prevalece sobre qualquer palpite pelo log (DEC-30 item 11).
	if oom, err := rt.OOMKilled(ictx, containerID); err != nil {
		w.log.Warn("ler se o contêiner morreu por memória", "erro", err)
	} else if oom {
		detail.OOMKilled = true
		detail.Diagnosis = diagnosis.Diagnosis{
			Confidence: diagnosis.Confirmed,
			Message: fmt.Sprintf("a aplicação foi encerrada por exceder o limite de memória (%d MiB); aumente-o nas configurações do projeto ou nas globais",
				limits.AppMemoryMB),
		}
	}
	return detail
}

// fail registra a falha terminal do deploy. Quando há detalhe (log e
// diagnóstico), ele vai no evento e a causa provável entra no motivo.
func (w *Worker) fail(ctx context.Context, o *store.Owned, dep *domain.Deployment, cause error) {
	stage := "PIPELINE"
	var se *stageError
	if errors.As(cause, &se) {
		stage = se.stage
	}
	payload := map[string]any{"error": cause.Error()}
	reason := cause.Error()

	var de *detailedError
	if errors.As(cause, &de) {
		payload["log_tail"] = de.detail.LogTail
		if de.detail.ExitCode != nil {
			payload["exit_code"] = *de.detail.ExitCode
		}
		payload["diagnosis"] = de.detail.Diagnosis
		if de.detail.OOMKilled {
			payload["oom_killed"] = true
		}
		if de.detail.Diagnosis.Message != "" {
			reason += " — " + de.detail.Diagnosis.Message
		}
	}

	w.log.Error("deploy falhou", "deployment", dep.ID, "estagio", stage, "erro", cause)
	if err := o.Transition(ctx, dep.ID, domain.DeploymentFailed, reason); err != nil {
		w.log.Error("registrar falha", "erro", err)
		return
	}
	_ = w.emit(ctx, o, dep.ID, stage, "failed", payload)
}

// pipeline percorre os estágios do §15. Devolve nil quando o deploy chegou a
// um estado final ou de espera (inclusive NEEDS_INPUT e UNSUPPORTED) e um erro
// quando falhou ou foi interrompido.
func (w *Worker) pipeline(ctx context.Context, o *store.Owned, job *domain.Job, dep *domain.Deployment) error {
	// Um job reivindicado depois de o deploy já ter terminado ou estar
	// esperando resposta (o worker anterior caiu antes de concluí-lo) não tem
	// o que executar.
	if dep.State.IsTerminal() || dep.State == domain.DeploymentNeedsInput {
		return nil
	}

	if err := w.emit(ctx, o, dep.ID, "pipeline", "attempt", map[string]any{
		"number": job.AttemptCount, "resumed": job.AttemptCount > 1,
	}); err != nil {
		return err
	}

	project, err := w.store.GetProject(ctx, dep.ProjectID)
	if err != nil {
		return at("FETCHING", fmt.Errorf("carregar projeto: %w", err))
	}

	// --- FETCHING ---
	if err := o.Transition(ctx, dep.ID, domain.DeploymentFetching, ""); err != nil {
		return at("FETCHING", err)
	}
	if err := w.emit(ctx, o, dep.ID, "fetching", "started", nil); err != nil {
		return err
	}

	// Um deploy já com SHA fixado (resposta a uma pendência, RF-08, ou
	// retomada) reutiliza o mesmo commit, mesmo que a branch tenha avançado
	// (RN-04).
	// O worker valida de novo a URL antes de clonar, mesmo que a API já tenha
	// validado (defesa em profundidade, DEC-27 item 13), com a lista de hosts
	// vigente agora.
	gitHosts, err := w.settings.GitHosts(ctx)
	if err != nil {
		return at("FETCHING", err)
	}
	limits, err := w.settings.Limits(ctx, project.Limits)
	if err != nil {
		return at("FETCHING", err)
	}
	rt := w.runtime.WithLimits(limits)
	repositoryURL, err := validate.RepositoryURL(project.RepositoryURL, gitHosts)
	if err != nil {
		return at("FETCHING", fmt.Errorf("repositório não permitido: %w", err))
	}
	root, err := validate.RootDirectory(project.RootDirectory)
	if err != nil {
		return at("FETCHING", fmt.Errorf("diretório do projeto inválido: %w", err))
	}
	ws, err := gitsource.Fetch(ctx, repositoryURL, gitsource.Options{Ref: dep.CommitSHA})
	if err != nil {
		return at("FETCHING", err)
	}
	defer ws.Cleanup()

	if err := o.SetCommitSHA(ctx, dep.ID, ws.CommitSHA); err != nil {
		return at("FETCHING", err)
	}
	dep.CommitSHA = ws.CommitSHA
	if err := w.emit(ctx, o, dep.ID, "fetching", "completed", map[string]any{"commit_sha": ws.CommitSHA}); err != nil {
		return err
	}

	// --- ANALYZING ---
	if err := o.Transition(ctx, dep.ID, domain.DeploymentAnalyzing, ""); err != nil {
		return at("ANALYZING", err)
	}
	// O diretório do projeto precisa continuar dentro do clone depois de
	// resolver links simbólicos: o repositório não escolhe o que o analisador
	// lê nem o que o build empacota (DEC-27 item 12).
	rootDir, err := gitsource.ResolveRoot(ws.Dir, root)
	if err != nil {
		return at("ANALYZING", err)
	}

	result, err := analyzer.Analyze(os.DirFS(rootDir), analyzer.Input{Overrides: dep.Overrides})
	if err != nil {
		return at("ANALYZING", err)
	}
	if _, err := o.SaveAnalysis(ctx, dep.ID, dep.ProjectID, ws.CommitSHA, result); err != nil {
		return at("ANALYZING", err)
	}
	if err := w.emit(ctx, o, dep.ID, "analyzing", "completed", map[string]any{
		"outcome":  result.Outcome,
		"analysis": result,
	}); err != nil {
		return err
	}

	switch result.Outcome {
	case domain.AnalysisUnsupported:
		return at("ANALYZING", o.Transition(ctx, dep.ID, domain.DeploymentUnsupported, ""))
	case domain.AnalysisNeedsInput:
		// RF-08: a pendência não ocupa o worker. A resposta chega por
		// POST /deployments/{id}/answers, que reenfileira o mesmo deploy
		// para o SHA já fixado.
		return at("ANALYZING", o.Transition(ctx, dep.ID, domain.DeploymentNeedsInput, ""))
	}

	// --- GENERATING ---
	if err := o.Transition(ctx, dep.ID, domain.DeploymentGenerating, ""); err != nil {
		return at("GENERATING", err)
	}
	plan, err := planner.FromAnalysis(result)
	if err != nil {
		return at("GENERATING", err)
	}
	if err := w.emit(ctx, o, dep.ID, "generating", "completed", map[string]any{
		"profile":         plan.Profile,
		"runtime":         plan.Runtime,
		"build_image":     plan.BuildImage,
		"node_version":    plan.NodeVersion,
		"install_command": plan.InstallCommand,
		"build_command":   plan.BuildCommand,
		"start_command":   plan.StartCommand,
		"execution_mode":  plan.ExecutionMode,
		"port":            plan.Port,
		"port_origin":     plan.PortOrigin,
		"dockerfile":      plan.Dockerfile,
		"limits":          limits,
	}); err != nil {
		return err
	}

	shortSHA := ws.CommitSHA
	if len(shortSHA) > 7 {
		shortSHA = shortSHA[:7]
	}
	imageTag := fmt.Sprintf("cobogo/%s:%s", project.Slug, shortSHA)
	containerName := containerNameFor(project.Slug, dep.ID)

	// --- BUILDING ---
	if err := o.Transition(ctx, dep.ID, domain.DeploymentBuilding, ""); err != nil {
		return at("BUILDING", err)
	}
	buildCtx, cancelBuild := context.WithTimeout(ctx, buildTimeout)
	for _, ref := range []string{plan.BuildImage, plan.Runtime} {
		if err := w.runtime.EnsureImage(buildCtx, ref); err != nil {
			cancelBuild()
			return at("BUILDING", err)
		}
	}
	buildResult, err := rt.Build(buildCtx, rootDir, plan.Dockerfile, imageTag, plan.Runtime, func(line string) {
		_ = w.emit(ctx, o, dep.ID, "building", "log", map[string]any{"line": line})
	})
	cancelBuild()
	if err != nil {
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			err = fmt.Errorf("o build excedeu o limite de %s: %w", buildTimeout, err)
		}
		return at("BUILDING", err)
	}
	if err := w.emit(ctx, o, dep.ID, "building", "completed", map[string]any{"image_id": buildResult.ImageID, "image_tag": buildResult.ImageTag}); err != nil {
		return err
	}

	// --- PROVISIONING --- (sem serviços auxiliares em M1)
	if err := o.Transition(ctx, dep.ID, domain.DeploymentProvisioning, ""); err != nil {
		return at("PROVISIONING", err)
	}
	if err := w.emit(ctx, o, dep.ID, "provisioning", "completed", map[string]any{"services": []string{}}); err != nil {
		return err
	}

	// --- STARTING ---
	if err := o.Transition(ctx, dep.ID, domain.DeploymentStarting, ""); err != nil {
		return at("STARTING", err)
	}
	// Variáveis do projeto (DEC-24): decifradas só aqui e entregues ao
	// contêiner. Valores nunca entram em evento ou log; só nome e versão.
	// PORT é da plataforma e vale sobre qualquer outro valor.
	envValues, envMeta, err := w.store.RevealEnvValues(ctx, project.ID)
	if err != nil {
		return at("STARTING", fmt.Errorf("carregar variáveis do projeto: %w", err))
	}
	// Valores que um log de falha precisa redigir: os do projeto, sem PORT
	// (da plataforma, não é segredo e aparece em qualquer log).
	projectValues := make([]string, 0, len(envValues))
	for _, v := range envValues {
		projectValues = append(projectValues, v)
	}
	have := make(map[string]string, len(envValues))
	for name := range envValues {
		have[name] = ""
	}
	envValues["PORT"] = fmt.Sprint(plan.Port)
	delivered := make([]map[string]any, 0, len(envMeta))
	for _, v := range envMeta {
		delivered = append(delivered, map[string]any{"name": v.Name, "version": v.Version})
	}

	// Efeito idempotente (DEC-26): remove o que uma tentativa anterior deste
	// deploy deixou, e confere a posse imediatamente antes de criar o contêiner.
	if err := w.runtime.RemoveByDeployment(ctx, dep.ID.String()); err != nil {
		return at("STARTING", err)
	}
	if err := o.Check(ctx); err != nil {
		return at("STARTING", err)
	}
	runResult, err := rt.Run(ctx, imageTag, containerName, plan.Port, envValues, dep.ID.String())

	// A partir do create existe uma candidata: qualquer saída sem sucesso a
	// remove, pelo ID (se a posse foi perdida, o novo dono pode ter a dele, com
	// o mesmo label de deploy).
	succeeded := false
	if runResult.ContainerID != "" {
		defer func() {
			if !succeeded {
				cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
				defer done()
				if err := w.runtime.Remove(cleanup, runResult.ContainerID); err != nil {
					w.log.Warn("remover candidata", "deployment", dep.ID, "erro", err)
				}
			}
		}()
	}
	if err != nil {
		if runResult.ContainerID != "" && ctx.Err() == nil {
			// O contêiner existe mas não ficou no ar (por exemplo, caiu no boot
			// por falta de uma variável): o log e o diagnóstico explicam.
			err = &detailedError{err: err, detail: w.investigate(ctx, rt, limits, runResult.ContainerID, err, projectValues, result.ConfigVariables, have)}
		}
		return at("STARTING", err)
	}
	if err := w.emit(ctx, o, dep.ID, "starting", "completed", map[string]any{"container_id": runResult.ContainerID, "host_port": runResult.HostPort, "env": delivered}); err != nil {
		return err
	}

	// --- VERIFYING ---
	if err := o.Transition(ctx, dep.ID, domain.DeploymentVerifying, ""); err != nil {
		return at("VERIFYING", err)
	}
	probe, err := dockerrt.ProbeReady(ctx, runResult.HostPort, plan.HealthPath, readinessTimeout, func(c context.Context) (bool, int, error) {
		return w.runtime.State(c, runResult.ContainerID)
	})
	if err != nil {
		if ctx.Err() == nil {
			err = &detailedError{err: err, detail: w.investigate(ctx, rt, limits, runResult.ContainerID, err, projectValues, result.ConfigVariables, have)}
		}
		return at("VERIFYING", err)
	}
	probePayload := map[string]any{"path": probe.Path, "status": probe.Status, "healthy": probe.Healthy()}
	if !probe.Healthy() {
		probePayload["warning"] = fmt.Sprintf("a aplicação respondeu %d em %s: está no ar, mas não respondeu 2xx; se a rota certa for outra, informe health_path", probe.Status, probe.Path)
	}
	if err := w.emit(ctx, o, dep.ID, "verifying", "probe", probePayload); err != nil {
		return err
	}

	if _, err := o.CreateRelease(ctx, dep.ID, buildResult.ImageID, runResult.ContainerID, "active", "healthy", runResult.HostPort); err != nil {
		return at("VERIFYING", err)
	}
	if err := o.Transition(ctx, dep.ID, domain.DeploymentSucceeded, ""); err != nil {
		return at("VERIFYING", err)
	}
	succeeded = true
	return w.emit(ctx, o, dep.ID, "verifying", "succeeded", map[string]any{
		"url": fmt.Sprintf("http://127.0.0.1:%d/", runResult.HostPort),
	})
}
