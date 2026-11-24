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
	"net"
	"os"
	"sort"
	"strings"
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
	// probeNetwork faz a sonda falar com o contêiner pelo nome, na rede do
	// projeto, em vez da porta publicada no loopback (DEC-32 item 5).
	probeNetwork bool
	// backupDir é onde o volume de backups está montado no worker, e backupVolume o nome
	// dele no Docker, para os contêineres de cópia o montarem (DEC-47). Vazios desligam o
	// backup dos dados dos projetos.
	backupDir, backupVolume string
}

// SetBackup liga o backup dos dados dos projetos: a pasta do volume de backups no
// worker e o nome do volume no Docker.
func (w *Worker) SetBackup(dir, volume string) { w.backupDir, w.backupVolume = dir, volume }

// SetProbeMode escolhe como a sonda alcança a aplicação: "host" (padrão) ou
// "network". Em "network" o worker se conecta à rede do projeto durante o deploy.
func (w *Worker) SetProbeMode(mode string) { w.probeNetwork = mode == "network" }

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
	go w.runCommands(ctx)
	go w.runLogCapture(ctx)
	go w.runProjectBackups(ctx)
	nextSweep := time.Now().Add(firstSweepAfter)

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
			// Sem deploy para fazer: operações de ciclo de vida e, de tempos em
			// tempos, a faxina (DEC-38). Um deploy sempre tem a vez primeiro.
			op, err := w.store.ClaimNextOperation(ctx, opLease, opMaxAttempts)
			if err != nil && ctx.Err() == nil {
				w.log.Error("reivindicar operação", "erro", err)
			}
			if op != nil {
				w.runOperation(ctx, op)
				continue
			}
			if time.Now().After(nextSweep) {
				w.sweepOnce(ctx)
				nextSweep = time.Now().Add(sweepInterval)
			}
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

// buildTailLines é quantas linhas do final do log do build o worker guarda para diagnosticar.
const buildTailLines = 120

// buildFailure acompanha a falha do build do final do log (sem valores secretos) e do
// diagnóstico de época, quando o log o permite (DEC-54 C). Sem assinatura conhecida, a falha
// segue como era.
func buildFailure(err error, lines []string, plan domain.Plan) error {
	if len(lines) == 0 {
		return err
	}
	tail := redact.Tail(redact.Clean(strings.Join(lines, "\n")), logTailBytes)
	d := diagnosis.Build(tail, plan.Profile, plan.PythonVersion)
	if d.Confidence == diagnosis.Unknown {
		return err
	}
	return &detailedError{err: err, detail: failureDetail{LogTail: tail, Diagnosis: d}}
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
	routing, err := w.settings.Apps(ctx)
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

	result, err := analyzer.Analyze(os.DirFS(rootDir), analyzer.Input{Overrides: dep.Overrides, Repo: os.DirFS(ws.Dir), Dir: root})
	if err != nil {
		return at("ANALYZING", err)
	}
	// Banco de dados (DEC-42): evidência forte sem escolha vira pendência, antes de
	// gastar o build. Nada é criado aqui.
	for _, ask := range []func(context.Context, domain.Project, domain.AnalysisResult) (*domain.Finding, error){w.databaseFinding, w.redisFinding} {
		if finding, err := ask(ctx, project, result); err != nil {
			return at("ANALYZING", err)
		} else if finding != nil && result.Outcome != domain.AnalysisUnsupported {
			result.Pending = append(result.Pending, *finding)
			result.Outcome = domain.AnalysisNeedsInput
		}
	}
	if _, err := o.SaveAnalysis(ctx, dep.ID, dep.ProjectID, ws.CommitSHA, result); err != nil {
		return at("ANALYZING", err)
	}
	// O papel da aplicação no projeto (DEC-52) é o que a análise viu na própria pasta.
	if role := appRole(result); role != "" && role != project.Role {
		if err := w.store.SetProjectRole(ctx, project.ID, role); err != nil {
			return at("ANALYZING", err)
		}
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

	// Variáveis que o usuário exigiu (DEC-39 item 4): falta de valor falha
	// cedo, antes do plano e do build, com a lista do que falta.
	if err := w.checkRequiredEnv(ctx, project.ID); err != nil {
		return at("ANALYZING", err)
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
		"bun_version":     plan.BunVersion,
		"python_version":  plan.PythonVersion,
		"app_target":      plan.AppTarget,
		"output_dir":      plan.OutputDir,
		"spa_fallback":    plan.SPAFallback,
		"build_args":      plan.BuildArgs,
		"install_command": plan.InstallCommand,
		"build_command":   plan.BuildCommand,
		"start_command":   plan.StartCommand,
		"processes":       plan.Processes,
		"release_command": plan.ReleaseCommand,
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
	// Configuração pública de build do site estático (DEC-37): só as variáveis
	// públicas que a análise encontrou e o projeto definiu.
	var buildArgs map[string]*string
	if len(plan.BuildArgs) > 0 {
		values, _, err := w.store.RevealEnvValues(buildCtx, project.ID)
		if err != nil {
			cancelBuild()
			return at("BUILDING", fmt.Errorf("carregar variáveis públicas do build: %w", err))
		}
		buildArgs = map[string]*string{}
		for _, name := range plan.BuildArgs {
			if v, ok := values[name]; ok {
				buildArgs[name] = &v
			}
		}
	}
	imageLabels := map[string]string{dockerrt.LabelManaged: "true", dockerrt.LabelProject: project.ID.String()}
	var buildTail []string // o final do log do build, para reconhecer uma falha de época (DEC-54 C)
	buildResult, err := rt.BuildWithArgs(buildCtx, rootDir, plan.Dockerfile, imageTag, plan.Runtime, buildArgs, imageLabels, func(line string) {
		buildTail = append(buildTail, line)
		if len(buildTail) > buildTailLines {
			buildTail = buildTail[len(buildTail)-buildTailLines:]
		}
		_ = w.emit(ctx, o, dep.ID, "building", "log", map[string]any{"line": line})
	})
	cancelBuild()
	if err != nil {
		if errors.Is(buildCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			err = fmt.Errorf("o build excedeu o limite de %s: %w", buildTimeout, err)
		}
		return at("BUILDING", buildFailure(err, buildTail, plan))
	}
	if err := w.emit(ctx, o, dep.ID, "building", "completed", map[string]any{"image_id": buildResult.ImageID, "image_tag": buildResult.ImageTag}); err != nil {
		return err
	}

	// --- PROVISIONING --- (sem serviços auxiliares em M1)
	if err := o.Transition(ctx, dep.ID, domain.DeploymentProvisioning, ""); err != nil {
		return at("PROVISIONING", err)
	}
	services := []string{}
	var managedDB *domain.Service
	if svc, serr := w.store.GetDatabaseService(ctx, project.ID); serr == nil && svc.Mode == domain.ServiceManaged && svc.Status != domain.ServiceDeleting {
		created, err := w.provisionService(ctx, rt, project, svc)
		if err != nil {
			return at("PROVISIONING", err)
		}
		services = append(services, svc.Engine)
		managedDB = &svc
		if err := w.emit(ctx, o, dep.ID, "provisioning", svc.Engine, map[string]any{"container": svc.ContainerName, "created": created, "variable": svc.Variable}); err != nil {
			return err
		}
	}
	// O Redis gerenciado (DEC-50 C): sobe antes da aplicação, como o banco.
	if rs, rerr := w.store.GetService(ctx, project.ID, domain.ServiceRedis); rerr == nil && rs.Mode == domain.ServiceManaged && rs.Status != domain.ServiceDeleting {
		created, err := w.provisionService(ctx, rt, project, rs)
		if err != nil {
			return at("PROVISIONING", err)
		}
		services = append(services, rs.Engine)
		if err := w.emit(ctx, o, dep.ID, "provisioning", rs.Engine, map[string]any{"container": rs.ContainerName, "created": created, "variable": rs.Variable}); err != nil {
			return err
		}
	}
	if err := w.emit(ctx, o, dep.ID, "provisioning", "completed", map[string]any{"services": services}); err != nil {
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
	// O site estático é servido pelo nginx da plataforma: não há processo do
	// projeto que use variáveis, então nenhuma é entregue (DEC-37).
	if plan.Profile == domain.ProfileStatic {
		envValues, envMeta = map[string]string{}, nil
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
	for k, v := range publicEnv(plan, routing, project.Slug) {
		envValues[k] = v
	}
	// Workers do gunicorn pelo limite de memória (DEC-48 item 6), se o projeto não
	// definiu o seu.
	if _, own := have["WEB_CONCURRENCY"]; !own && plan.Profile == domain.ProfilePython {
		envValues["WEB_CONCURRENCY"] = fmt.Sprint(WebConcurrency(limits.AppMemoryMB))
	}
	// TLS do banco gerenciado (DEC-44): a CA é injetada num arquivo do contêiner
	// e apontada por variáveis da plataforma, sem tocar no código do projeto.
	var runFiles map[string][]byte
	if managedDB != nil && managedDB.TLS && plan.Profile != domain.ProfileStatic {
		files, env, err := w.databaseTrust(ctx, rt, project, *managedDB, have)
		if err != nil {
			return at("STARTING", err)
		}
		runFiles = files
		for k, v := range env {
			envValues[k] = v
		}
	}
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
	// Rede do projeto e, se há domínio, a de ingress (DEC-31). O contêiner sobe
	// fora do ingress: só entra depois de validado.
	projectNet := dockerrt.ProjectNetwork(project.ID.String())
	if err := rt.EnsureNetwork(ctx, projectNet, map[string]string{dockerrt.LabelProject: project.ID.String()}); err != nil {
		return at("STARTING", err)
	}
	if routing.Enabled() {
		if err := rt.EnsureNetwork(ctx, dockerrt.IngressNetwork, nil); err != nil {
			return at("STARTING", err)
		}
	}
	if w.probeNetwork {
		// O worker só fica ligado à rede do projeto enquanto dura o deploy.
		self, err := dockerrt.SelfID()
		if err != nil {
			return at("STARTING", fmt.Errorf("identificar o contêiner do worker: %w", err))
		}
		if err := rt.ConnectContainer(ctx, self, projectNet); err != nil {
			return at("STARTING", err)
		}
		defer func() {
			cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
			defer done()
			if err := rt.DisconnectContainer(cleanup, self, projectNet); err != nil {
				w.log.Warn("desconectar o worker da rede do projeto", "rede", projectNet, "erro", err)
			}
		}()
	}
	// Dados persistentes (DEC-41): os volumes dos caminhos declarados e a troca
	// de release sem dois processos no mesmo volume. O site estático não usa.
	succeeded := false
	runOpts := []dockerrt.RunOption{
		dockerrt.WithNetwork(projectNet), dockerrt.WithLabels(map[string]string{dockerrt.LabelProject: project.ID.String()}),
	}
	if len(runFiles) > 0 {
		runOpts = append(runOpts, dockerrt.WithFiles(runFiles))
	}
	var appVolumes map[string]string
	if store.HasPersistentPaths(project) && plan.Profile != domain.ProfileStatic {
		volumes, created, err := w.prepareVolumes(ctx, rt, project, imageTag)
		if err != nil {
			return at("STARTING", err)
		}
		appVolumes = volumes
		runOpts = append(runOpts, dockerrt.WithVolumes(volumes))
		paths := make([]string, 0, len(volumes))
		for p := range volumes {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		if err := w.emit(ctx, o, dep.ID, "starting", "volumes", map[string]any{"paths": paths, "created": created}); err != nil {
			return err
		}
		paused, err := w.pauseActive(ctx, project)
		if err != nil {
			return at("STARTING", err)
		}
		if paused != nil {
			if err := w.emit(ctx, o, dep.ID, "starting", "previous_paused", map[string]any{"container": shortContainer(paused.release.ContainerID), "reason": "o volume só pode ser usado por uma release por vez"}); err != nil {
				return err
			}
			// Se a candidata não subir, a anterior volta (e o deploy falha sem tirar o projeto do ar).
			defer func() {
				if succeeded {
					return
				}
				cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
				defer done()
				if err := w.resumeActive(cleanup, paused); err != nil {
					w.log.Error("reiniciar a release anterior depois de a candidata falhar", "deployment", dep.ID, "erro", err)
					return
				}
				_ = w.emit(cleanup, o, dep.ID, "starting", "previous_restored", map[string]any{"container": shortContainer(paused.release.ContainerID)})
			}()
		}
	}
	// A fase de release (DEC-52 item 4): as migrações rodam da imagem nova, uma vez, antes
	// de a aplicação nova subir. A anterior segue no ar; se falha, o deploy falha aqui.
	if plan.ReleaseCommand != "" && plan.Profile != domain.ProfileStatic {
		if err := w.emit(ctx, o, dep.ID, "starting", "release", map[string]any{"command": plan.ReleaseCommand}); err != nil {
			return err
		}
		rel, err := w.runRelease(ctx, rt, project, dep.ID.String(), imageTag, plan.ReleaseCommand, envValues, projectNet, appVolumes, runFiles, limits, projectValues)
		if err != nil {
			code := rel.ExitCode
			return at("STARTING", &detailedError{err: err, detail: failureDetail{LogTail: tailOf(rel.Output, releaseTailShown), ExitCode: &code}})
		}
		if err := w.emit(ctx, o, dep.ID, "starting", "release_done", map[string]any{"command": plan.ReleaseCommand, "seconds": rel.Seconds, "output": tailOf(rel.Output, releaseTailShown)}); err != nil {
			return err
		}
	}
	runResult, err := rt.Run(ctx, imageTag, containerName, plan.Port, envValues, dep.ID.String(), runOpts...)

	// A partir do create existe uma candidata: qualquer saída sem sucesso a
	// remove, pelo ID (se a posse foi perdida, o novo dono pode ter a dele, com
	// o mesmo label de deploy).
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

	// Os processos além do web (DEC-51): worker e agendador, da mesma imagem, com as mesmas variáveis,
	// volumes e serviços, sem porta nem rota. Qualquer saída sem sucesso os remove junto com a candidata.
	var processIDs []string
	for _, pr := range plan.Processes {
		id, perr := rt.RunProcess(ctx, imageTag, containerName+"-"+pr.Name, processCommand(pr), envValues, dep.ID.String(), pr.Name, runOpts...)
		if id != "" {
			processIDs = append(processIDs, id)
			defer func(id string) {
				if !succeeded {
					cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
					defer done()
					if err := w.runtime.Remove(cleanup, id); err != nil {
						w.log.Warn("remover processo da candidata", "deployment", dep.ID, "erro", err)
					}
				}
			}(id)
		}
		if perr != nil {
			if id != "" && ctx.Err() == nil {
				perr = &detailedError{err: perr, detail: w.investigate(ctx, rt, limits, id, perr, projectValues, result.ConfigVariables, have)}
			}
			return at("STARTING", perr)
		}
		if err := w.emit(ctx, o, dep.ID, "starting", "process", map[string]any{"name": pr.Name, "container": shortContainer(id), "command": pr.Command}); err != nil {
			return err
		}
	}

	// --- VERIFYING ---
	if err := o.Transition(ctx, dep.ID, domain.DeploymentVerifying, ""); err != nil {
		return at("VERIFYING", err)
	}
	alive := func(c context.Context) (bool, int, error) {
		return w.runtime.State(c, runResult.ContainerID)
	}
	var probe dockerrt.ProbeResult
	// A sonda se apresenta com o endereço público do projeto, como o tráfego real (DEC-49).
	publicHost := ""
	if routing.Enabled() {
		publicHost = routing.Host(project.Slug)
	}
	if w.probeNetwork {
		probe, err = dockerrt.ProbeReadyAs(ctx, containerName, plan.Port, plan.HealthPath, publicHost, readinessTimeout, alive)
	} else {
		probe, err = dockerrt.ProbeReadyAs(ctx, "127.0.0.1", runResult.HostPort, plan.HealthPath, publicHost, readinessTimeout, alive)
	}
	if err != nil {
		if ctx.Err() == nil {
			detail := w.investigate(ctx, rt, limits, runResult.ContainerID, err, projectValues, result.ConfigVariables, have)
			// O app seguiu rodando, o log não aponta outra causa e a porta veio
			// do contrato de PORT: a suspeita é a porta (DEC-35 item 5).
			if detail.ExitCode == nil && !detail.OOMKilled && detail.Diagnosis.Confidence == diagnosis.Unknown &&
				plan.PortOrigin == string(domain.OriginPlatform) &&
				// No Python sem Procfile quem abre a porta é o servidor da plataforma.
				!(plan.Profile == domain.ProfilePython && result.Fields[domain.FieldStartCommand].Value == "") {
				detail.Diagnosis = diagnosis.PlatformPortIgnored(plan.Port, portSource(result))
			}
			err = &detailedError{err: err, detail: detail}
		}
		return at("VERIFYING", err)
	}
	probePayload := map[string]any{"path": probe.Path, "status": probe.Status, "healthy": probe.Healthy()}
	if !probe.Healthy() {
		probePayload["warning"] = fmt.Sprintf("a aplicação respondeu %d em %s: está no ar, mas não respondeu 2xx; se a rota certa for outra, informe health_path", probe.Status, probe.Path)
	}
	// Um 400 (ou 421) passa na sonda, mas a release fica marcada como degradada, e o
	// motivo conhecido é dito aqui (DEC-49 item 5). Um 404 ou 401 na raiz é comum em
	// APIs e não conta: o aviso de health_path já o cobre.
	releaseHealth := domain.ReleaseHealthy
	if probe.Status == 400 || probe.Status == 421 {
		releaseHealth = domain.ReleaseDegraded
		probePayload["degraded"] = true
		if cause := diagnosis.ProbeCause(probe.Status, result.Fields[domain.FieldPythonFramework].Value); cause != "" {
			probePayload["cause"] = cause
		}
	}
	if err := w.emit(ctx, o, dep.ID, "verifying", "probe", probePayload); err != nil {
		return err
	}

	// Um processo sem porta não tem o que sondar: ficar de pé pela janela de estabilidade é o sinal. Se um
	// sai, o deploy falha com o log dele, e a release anterior segue no ar.
	for i, id := range processIDs {
		if err := rt.WaitStable(ctx, id, processStableWindow); err != nil {
			err = fmt.Errorf("o processo %s não ficou de pé: %w", plan.Processes[i].Name, err)
			if ctx.Err() == nil {
				err = &detailedError{err: err, detail: w.investigate(ctx, rt, limits, id, err, projectValues, result.ConfigVariables, have)}
			}
			return at("VERIFYING", err)
		}
	}
	if len(processIDs) > 0 {
		if err := w.emit(ctx, o, dep.ID, "verifying", "processes", map[string]any{"count": len(processIDs), "stable_seconds": int(processStableWindow.Seconds())}); err != nil {
			return err
		}
	}

	// Promoção (DEC-31 item 10): a candidata entra no ingress; depois, numa só
	// transação, o deploy vai a SUCCEEDED e a release vira a ativa do projeto.
	// Uma falha antes disso remove a candidata e deixa a release anterior no ar.
	spec := store.ReleaseSpec{
		ImageID: buildResult.ImageID, ContainerID: runResult.ContainerID, Health: releaseHealth, HostPort: runResult.HostPort,
	}
	if routing.Enabled() {
		if err := o.Check(ctx); err != nil {
			return at("VERIFYING", err)
		}
		if err := rt.Promote(ctx, runResult.ContainerID, dockerrt.IngressNetwork); err != nil {
			return at("VERIFYING", err)
		}
		spec.URL = routing.URL(project.Slug)
		spec.RouteHost = routing.Host(project.Slug)
		spec.RouteTarget = fmt.Sprintf("http://%s:%d", containerName, plan.Port)
	}
	if _, err := o.PromoteRelease(ctx, dep.ID, spec); err != nil {
		return at("VERIFYING", err)
	}
	succeeded = true
	releaseURL := spec.URL

	// As releases anteriores ficam rodando até o período de espera acabar: o
	// Traefik leva até um ciclo de consulta para trocar a rota, e tirar a antiga
	// antes disso derrubaria pedidos em andamento. A retirada é uma operação
	// agendada, fora do pipeline (DEC-38 item 2).
	w.scheduleRetire(ctx, project.ID)
	if routing.Enabled() {
		if err := w.emit(ctx, o, dep.ID, "routing", "promoted", map[string]any{"url": releaseURL}); err != nil {
			return err
		}
	}

	finalURL := releaseURL
	if finalURL == "" {
		finalURL = fmt.Sprintf("http://127.0.0.1:%d/", runResult.HostPort)
	}
	return w.emit(ctx, o, dep.ID, "verifying", "succeeded", map[string]any{
		"url": finalURL, "routed": releaseURL != "",
	})
}

// portSource diz onde a análise viu o código ler PORT ("index.js:12"), para o
// diagnóstico da porta da plataforma.
func portSource(result domain.AnalysisResult) string {
	f, ok := result.Fields[domain.FieldPort]
	if !ok || len(f.EvidenceIDs) == 0 {
		return ""
	}
	for _, e := range result.Evidences {
		if e.ID == f.EvidenceIDs[0] {
			if e.Line > 0 {
				return fmt.Sprintf("%s:%d", e.Source, e.Line)
			}
			return e.Source
		}
	}
	return ""
}

// checkRequiredEnv falha o deploy quando alguma variável obrigatória do projeto
// ainda não tem valor. Só nomes: nada do que está guardado é lido.
func (w *Worker) checkRequiredEnv(ctx context.Context, projectID uuid.UUID) error {
	required, err := w.store.RequiredEnv(ctx, projectID)
	if err != nil {
		return fmt.Errorf("ler variáveis obrigatórias: %w", err)
	}
	if len(required) == 0 {
		return nil
	}
	defined, err := w.store.ListEnvVariables(ctx, projectID)
	if err != nil {
		return fmt.Errorf("listar variáveis do projeto: %w", err)
	}
	have := make(map[string]bool, len(defined))
	for _, v := range defined {
		have[v.Name] = true
	}
	var missing []string
	for _, name := range required {
		if !have[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	list := strings.Join(missing, ", ")
	return &detailedError{
		err: fmt.Errorf("faltam variáveis obrigatórias: %s", list),
		detail: failureDetail{Diagnosis: diagnosis.Diagnosis{
			Confidence: diagnosis.Confirmed,
			Variables:  missing,
			Message:    "defina o valor de " + list + " na aba de variáveis e faça um novo deploy; o build nem começou",
		}},
	}
}

// WebConcurrency é o número de workers do gunicorn para um limite de memória: um
// a cada 256 MB, de 1 a 4.
func WebConcurrency(memoryMB int) int {
	return max(1, min(4, memoryMB/256))
}

// publicEnv são as variáveis da plataforma com o endereço público do projeto, entregues
// quando o plano as usa (o módulo de ajuste do Django, DEC-49 item 6). O host vai sem porta.
func publicEnv(plan domain.Plan, routing settings.Routing, slug string) map[string]string {
	if !plan.DjangoPlatformHosts || !routing.Enabled() {
		return nil
	}
	host := routing.Host(slug)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return map[string]string{"COBOGO_PUBLIC_HOST": host, "COBOGO_PUBLIC_URL": strings.TrimRight(routing.URL(slug), "/")}
}
