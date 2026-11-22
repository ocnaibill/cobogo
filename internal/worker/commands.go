package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/planner"
	"github.com/ocnaibill/cobogo/internal/redact"
)

// Comandos pontuais (DEC-45). Cada comando roda num contêiner descartável, da
// imagem da release ativa, na jaula da aplicação. Há um laço próprio, fora do dos
// deploys: um comando de minutos não segura um build, e um build não segura um
// comando.

const (
	// maxConcurrentCommands é o teto de comandos ao mesmo tempo na máquina (DEC-45 item 3).
	maxConcurrentCommands = 3
	commandPoll           = 2 * time.Second
	commandTick           = time.Second
	commandOutputEvery    = 2 * time.Second
	// commandGrace é o prazo entre o pedido de término e a morte à força.
	commandGrace = 10

	commandMemoryMB = 512
	commandCPUs     = 1.0
	commandPids     = 128
)

// runCommands é o laço dos comandos. Ao subir, o que ficou "rodando" de um worker
// que caiu vira "interrompido" e os contêineres sobrados saem.
func (w *Worker) runCommands(ctx context.Context) {
	if n, err := w.store.InterruptRunningCommands(ctx); err != nil {
		w.log.Warn("interromper os comandos que ficaram rodando", "erro", err)
	} else if n > 0 {
		w.log.Warn("comandos interrompidos pelo reinício do worker", "quantos", n)
	}
	if n, err := w.runtime.RemoveCommandContainers(ctx, ""); err != nil {
		w.log.Warn("remover contêineres de comando sobrados", "erro", err)
	} else if n > 0 {
		w.log.Info("contêineres de comando sobrados removidos", "quantos", n)
	}

	sem := make(chan struct{}, maxConcurrentCommands)
	for {
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}
		cmd, err := w.store.ClaimNextCommand(ctx)
		if err != nil || cmd == nil {
			<-sem
			if err != nil && ctx.Err() == nil {
				w.log.Error("reivindicar comando", "erro", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(commandPoll):
			}
			continue
		}
		go func() {
			defer func() { <-sem }()
			w.executeCommand(ctx, cmd)
		}()
	}
}

// commandPlan lê o plano do deploy da release, para saber o perfil e a porta.
func (w *Worker) commandPlan(ctx context.Context, active domain.Release) (domain.Plan, error) {
	dep, err := w.store.GetDeployment(ctx, active.DeploymentID)
	if err != nil {
		return domain.Plan{}, err
	}
	if dep.AnalysisID == nil {
		return domain.Plan{}, errors.New("o deploy da release não tem análise")
	}
	a, err := w.store.GetAnalysis(ctx, *dep.AnalysisID)
	if err != nil {
		return domain.Plan{}, err
	}
	return planner.FromAnalysis(a.Result)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// executeCommand roda um comando reivindicado até o desfecho e o grava. Toda
// saída passa pela redação dos valores de variável do projeto.
func (w *Worker) executeCommand(ctx context.Context, cmd *domain.Command) {
	var secrets []string
	finish := func(state string, exit *int, output string, truncated bool, msg string) {
		save, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
		defer done()
		clean := func(s string) string { return redact.Values(redact.Clean(s), secrets) }
		if err := w.store.FinishCommand(save, cmd.ID, state, exit, clean(cmd.Command), clean(output), truncated, clean(msg)); err != nil {
			w.log.Error("gravar o desfecho do comando", "comando", cmd.ID, "erro", err)
		}
		w.log.Info("comando concluído", "comando", cmd.ID, "projeto", cmd.ProjectID, "estado", state)
	}
	fail := func(msg string) { finish(domain.CommandFailed, nil, "", false, msg) }

	if cancel, _ := w.store.CommandCancelRequested(ctx, cmd.ID); cancel {
		finish(domain.CommandCanceled, nil, "", false, "cancelado antes de iniciar")
		return
	}
	project, err := w.store.GetProject(ctx, cmd.ProjectID)
	if err != nil || project.DeletedAt != nil {
		fail("o projeto não existe mais")
		return
	}
	active, err := w.store.ActiveRelease(ctx, project.ID)
	if err != nil {
		fail("o projeto não tem release no ar")
		return
	}
	plan, err := w.commandPlan(ctx, active)
	if err != nil {
		fail("não foi possível ler o plano do deploy: " + err.Error())
		return
	}
	if plan.Profile == domain.ProfileStatic {
		fail("um site estático não tem ambiente para comandos: a imagem é só o servidor web")
		return
	}
	limits, err := w.settings.Limits(ctx, project.Limits)
	if err != nil {
		fail("ler os limites do projeto: " + err.Error())
		return
	}
	rt := w.runtime.WithLimits(limits)

	values, _, err := w.store.RevealEnvValues(ctx, project.ID)
	if err != nil {
		fail("carregar as variáveis do projeto: " + err.Error())
		return
	}
	env := make(map[string]string, len(values)+3)
	defined := make(map[string]string, len(values))
	for k, v := range values {
		env[k] = v
		defined[k] = ""
		secrets = append(secrets, v)
	}
	env["PORT"] = fmt.Sprint(plan.Port)

	spec := dockerrt.CommandSpec{
		ProjectID: project.ID.String(), CommandID: cmd.ID.String(), Image: active.ImageID, Command: cmd.Command,
		Env: env, Network: dockerrt.ProjectNetwork(project.ID.String()),
		MemoryMB: minInt(limits.AppMemoryMB, commandMemoryMB), Pids: minInt(limits.AppPids, commandPids),
		CPUs: commandCPUs,
	}
	if limits.AppCPUs > 0 && limits.AppCPUs < spec.CPUs {
		spec.CPUs = limits.AppCPUs
	}

	// O banco gerenciado: a CA entra como na aplicação (DEC-44). Sem ela o comando
	// ainda roda (nem todo comando usa o banco): só se registra o aviso.
	if svc, err := w.store.GetService(ctx, project.ID, domain.ServicePostgres); err == nil && svc.Mode == domain.ServiceManaged && svc.TLS && svc.Status != domain.ServiceDeleting {
		if files, tlsEnv, err := w.databaseTrust(ctx, rt, project, svc, defined); err != nil {
			w.log.Warn("CA do banco indisponível para o comando", "projeto", project.Slug, "erro", err)
		} else {
			spec.Files = files
			for k, v := range tlsEnv {
				spec.Env[k] = v
			}
		}
	}

	if err := rt.EnsureNetwork(ctx, spec.Network, map[string]string{dockerrt.LabelProject: project.ID.String()}); err != nil {
		fail(err.Error())
		return
	}
	// Os volumes da aplicação, os mesmos do deploy: um volume que ainda não existe
	// é criado e entregue ao usuário da imagem, como num deploy (DEC-41).
	if len(project.PersistentPaths) > 0 {
		volumes, _, err := w.prepareVolumes(ctx, rt, project, active.ImageID)
		if err != nil {
			fail(err.Error())
			return
		}
		spec.Volumes = volumes
	}

	// A opção de pausar a aplicação (DEC-45 item 7): para, roda e volta a iniciar.
	// Se já estava parada, continua parada.
	if cmd.PauseApp && active.StoppedAt == nil {
		op := &domain.Operation{ProjectID: &project.ID}
		if _, err := w.stopApp(ctx, op); err != nil {
			fail("não foi possível pausar a aplicação: " + err.Error())
			return
		}
		defer func() {
			resume, done := context.WithTimeout(context.WithoutCancel(ctx), opTimeout)
			defer done()
			if _, err := w.startApp(resume, op); err != nil {
				w.log.Error("reiniciar a aplicação depois do comando", "projeto", project.Slug, "erro", err)
			}
		}()
	}

	id, err := rt.StartCommand(ctx, spec)
	if err != nil {
		fail(err.Error())
		return
	}
	defer func() {
		rm, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
		defer done()
		if err := rt.RemoveCommand(rm, id); err != nil {
			w.log.Warn("remover o contêiner do comando", "comando", cmd.ID, "erro", err)
		}
	}()

	deadline := time.Now().Add(time.Duration(cmd.TimeoutSeconds) * time.Second)
	lastOutput := time.Now()
	state, msg := "", ""
	for state == "" {
		select {
		case <-ctx.Done():
			// O worker está encerrando: o comando não termina sozinho.
			_ = rt.StopCommand(context.WithoutCancel(ctx), id, 2)
			state, msg = domain.CommandInterrupted, "o worker foi encerrado durante o comando"
			continue
		case <-time.After(commandTick):
		}
		running, code, oom, err := rt.CommandState(ctx, id)
		switch {
		case err != nil:
			state, msg = domain.CommandFailed, "acompanhar o comando: "+err.Error()
		case !running:
			c := code
			out, cut, _ := rt.CommandOutput(ctx, id, domain.CommandMaxOutput)
			switch {
			case oom:
				finish(domain.CommandFailed, &c, out, cut, fmt.Sprintf("o comando foi morto por falta de memória (limite de %d MiB)", spec.MemoryMB))
			case code == 0:
				finish(domain.CommandSucceeded, &c, out, cut, "")
			default:
				finish(domain.CommandFailed, &c, out, cut, "")
			}
			return
		}
		if state != "" {
			break
		}
		if cancel, _ := w.store.CommandCancelRequested(ctx, cmd.ID); cancel {
			_ = rt.StopCommand(ctx, id, commandGrace)
			state, msg = domain.CommandCanceled, "cancelado pelo dono"
			break
		}
		if time.Now().After(deadline) {
			_ = rt.StopCommand(ctx, id, commandGrace)
			state, msg = domain.CommandTimedOut, fmt.Sprintf("o comando passou do limite de %d s e foi encerrado", cmd.TimeoutSeconds)
			break
		}
		if time.Since(lastOutput) >= commandOutputEvery {
			lastOutput = time.Now()
			if out, cut, err := rt.CommandOutput(ctx, id, domain.CommandMaxOutput); err == nil {
				_ = w.store.UpdateCommandOutput(ctx, cmd.ID, redact.Values(redact.Clean(out), secrets), cut)
			}
		}
	}

	// Saída final dos desfechos que vieram do laço (cancelar, prazo, queda).
	read, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer done()
	out, cut, _ := rt.CommandOutput(read, id, domain.CommandMaxOutput)
	var exit *int
	if _, code, _, err := rt.CommandState(read, id); err == nil {
		exit = &code
	}
	finish(state, exit, out, cut, msg)
}
