package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/planner"
)

// Processos além do web (DEC-51): worker e agendador da mesma imagem. Um processo não tem HTTP para
// sondar: ficar de pé por uma janela curta é o sinal de que subiu.

// processStableWindow é por quanto tempo um processo tem de continuar rodando para valer como "subiu".
var processStableWindow = 10 * time.Second

// stopProcesses para os processos da release (mantendo os contêineres, como o web, DEC-40).
func (w *Worker) stopProcesses(ctx context.Context, active domain.Release) error {
	list, err := w.runtime.ListProcesses(ctx, active.DeploymentID.String())
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range list {
		if err := w.runtime.StopContainer(ctx, c.ID); err != nil {
			errs = append(errs, fmt.Errorf("parar o processo %s: %w", c.Process, err))
		}
	}
	return errors.Join(errs...)
}

// startProcesses inicia os processos da release e espera cada um ficar de pé pela janela de estabilidade.
func (w *Worker) startProcesses(ctx context.Context, active domain.Release) error {
	list, err := w.runtime.ListProcesses(ctx, active.DeploymentID.String())
	if err != nil {
		return err
	}
	for _, c := range list {
		if err := w.runtime.StartProcess(ctx, c.ID); err != nil {
			return err
		}
	}
	for _, c := range list {
		if err := w.runtime.WaitStable(ctx, c.ID, processStableWindow); err != nil {
			return fmt.Errorf("o processo %s não ficou de pé: %w", c.Process, err)
		}
	}
	return nil
}

// processCommand é o comando de um processo do plano, com `exec` na frente quando é seguro, para o processo
// receber o SIGTERM (a mesma regra do web).
func processCommand(p domain.PlanProcess) string { return planner.ShellStart(p.Command) }
