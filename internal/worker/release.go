package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/redact"
)

// A fase de release (DEC-52 item 4): o comando do plano (as migrações) roda uma vez por
// deploy, num contêiner descartável da imagem nova, com as variáveis, a rede, os volumes
// e os arquivos da aplicação, antes de a aplicação nova subir. A release anterior segue
// no ar enquanto isso; se o comando falha, o deploy falha com a saída, e ela continua.

const (
	releaseTimeoutLimit = 10 * time.Minute
	releaseTick         = time.Second
	releaseOutputMax    = 64 << 10
	releaseTailShown    = 4000
)

type releaseResult struct {
	ExitCode int
	Output   string
	Seconds  int
}

func releaseContainerName(depID string) string {
	if len(depID) > 8 {
		depID = depID[:8]
	}
	return "cobogo-release-" + depID
}

// runRelease roda o comando e espera o fim. A saída volta já sem os valores das
// variáveis do projeto.
func (w *Worker) runRelease(ctx context.Context, rt *dockerrt.Runtime, project domain.Project, depID, image, command string,
	env map[string]string, network string, volumes map[string]string, files map[string][]byte, limits domain.Limits, secrets []string) (releaseResult, error) {
	name := releaseContainerName(depID)
	// Uma tentativa anterior deste deploy pode ter deixado o contêiner.
	_ = rt.RemoveCommand(ctx, name)
	spec := dockerrt.CommandSpec{
		ProjectID: project.ID.String(), CommandID: depID, Name: name, Role: dockerrt.RoleRelease,
		Image: image, Command: command, Env: env, Network: network, Volumes: volumes, Files: files,
		MemoryMB: limits.AppMemoryMB, CPUs: limits.AppCPUs, Pids: limits.AppPids,
	}
	if spec.CPUs <= 0 {
		spec.CPUs = commandCPUs
	}
	start := time.Now()
	id, err := rt.StartCommand(ctx, spec)
	if err != nil {
		return releaseResult{}, err
	}
	defer func() {
		rm, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
		defer done()
		_ = rt.RemoveCommand(rm, id)
	}()
	clean := func(s string) string { return redact.Values(redact.Clean(s), secrets) }
	deadline := start.Add(releaseTimeoutLimit)
	for {
		select {
		case <-ctx.Done():
			_ = rt.StopCommand(context.WithoutCancel(ctx), id, 2)
			return releaseResult{}, fmt.Errorf("a fase de release foi interrompida: %w", ctx.Err())
		case <-time.After(releaseTick):
		}
		running, code, oom, err := rt.CommandState(ctx, id)
		if err != nil {
			return releaseResult{}, fmt.Errorf("acompanhar a fase de release: %w", err)
		}
		if !running {
			out, _, _ := rt.CommandOutput(ctx, id, releaseOutputMax)
			res := releaseResult{ExitCode: code, Output: clean(out), Seconds: int(time.Since(start).Seconds())}
			if oom {
				return res, fmt.Errorf("a fase de release (%s) foi morta por falta de memória (limite de %d MiB)", command, spec.MemoryMB)
			}
			if code != 0 {
				return res, fmt.Errorf("a fase de release (%s) terminou com o código %d; a versão anterior continua no ar", command, code)
			}
			return res, nil
		}
		if time.Now().After(deadline) {
			_ = rt.StopCommand(context.WithoutCancel(ctx), id, 5)
			out, _, _ := rt.CommandOutput(ctx, id, releaseOutputMax)
			return releaseResult{ExitCode: -1, Output: clean(out), Seconds: int(time.Since(start).Seconds())},
				fmt.Errorf("a fase de release (%s) passou de %s e foi interrompida", command, releaseTimeoutLimit)
		}
	}
}

// tailOf devolve o fim de uma saída, para o evento.
func tailOf(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
