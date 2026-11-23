package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

// Ciclo de vida (DEC-38): aposentar releases, excluir projeto, aplicar limites
// e varrer recursos órfãos. Tudo o que o worker remove tem o label do Cobogó.

const (
	opLease       = 10 * time.Minute
	opMaxAttempts = 3
	opBackoff     = 30 * time.Second
	// A faxina roda de tempos em tempos e logo depois de o worker subir.
	sweepInterval   = 5 * time.Minute
	firstSweepAfter = 30 * time.Second
	opTimeout       = 5 * time.Minute
)

// retirable diz se um contêiner gerenciado pode ser removido agora, e por quê.
// É a regra única da aposentadoria e da faxina (DEC-38 itens 2 e 4):
//
//   - projeto excluído: sai na hora;
//   - release ativa do projeto: nunca;
//   - deploy ainda em curso (a candidata): nunca;
//   - substituído: só depois do período de espera, contado desde que a release
//     nova ficou ativa (o Traefik leva até um ciclo para trocar a rota);
//   - deploy terminado sem release ativa: depois do período de espera desde que
//     o deploy terminou.
func retirable(c dockerrt.ManagedContainer, f store.ContainerFact, now time.Time, grace time.Duration) (bool, string) {
	if f.ProjectDeleted {
		return true, "projeto excluído"
	}
	if c.ID == f.ActiveContainer {
		return false, ""
	}
	// Os processos (worker, agendador) da release ativa saem com ela, nunca antes (DEC-51).
	if f.ActiveDeployment != uuid.Nil && c.DeploymentID == f.ActiveDeployment.String() {
		return false, ""
	}
	if !f.DeploymentState.IsTerminal() {
		return false, ""
	}
	if f.ActiveContainer != "" && f.ActiveSince != nil {
		if now.Sub(*f.ActiveSince) < grace {
			return false, ""
		}
		return true, "substituída por uma release mais nova"
	}
	if now.Sub(f.DeploymentUpdate) < grace {
		return false, ""
	}
	return true, "deploy terminado sem release ativa"
}

// containersWithFacts lê os contêineres gerenciados e o que o banco sabe de cada
// um. Contêiner de um deploy que o banco não conhece nunca é tocado: pode ser
// de outra instalação do Cobogó no mesmo Docker.
func (w *Worker) containersWithFacts(ctx context.Context, projectID string) ([]dockerrt.ManagedContainer, map[uuid.UUID]store.ContainerFact, int, error) {
	containers, err := w.runtime.ListManaged(ctx, projectID)
	if err != nil {
		return nil, nil, 0, err
	}
	var ids []uuid.UUID
	for _, c := range containers {
		if id, err := uuid.Parse(c.DeploymentID); err == nil {
			ids = append(ids, id)
		}
	}
	facts, err := w.store.ContainerFacts(ctx, ids)
	if err != nil {
		return nil, nil, 0, err
	}
	return containers, facts, len(ids), nil
}

// retired descreve um contêiner que saiu, para o resultado da operação.
type retired struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// retireEligible remove os contêineres elegíveis de uma lista e registra um
// evento no histórico de cada deploy. Devolve o que saiu e os IDs.
func (w *Worker) retireEligible(ctx context.Context, containers []dockerrt.ManagedContainer, facts map[uuid.UUID]store.ContainerFact, grace time.Duration) ([]retired, []string, error) {
	now := time.Now()
	var out []retired
	var ids []string
	var errs []error
	for _, c := range containers {
		depID, err := uuid.Parse(c.DeploymentID)
		if err != nil {
			continue
		}
		f, known := facts[depID]
		if !known {
			continue
		}
		ok, reason := retirable(c, f, now, grace)
		if !ok {
			continue
		}
		if err := w.runtime.RetireContainer(ctx, c.ID); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.Name, err))
			continue
		}
		w.log.Info("contêiner removido", "contêiner", c.Name, "projeto", f.ProjectSlug, "motivo", reason)
		out = append(out, retired{Name: c.Name, Reason: reason})
		ids = append(ids, c.ID)
		if err := w.store.AppendEvent(ctx, depID, "lifecycle", "retired", map[string]any{"container": c.Name, "reason": reason}); err != nil {
			w.log.Warn("registrar a aposentadoria no histórico do deploy", "deploy", depID, "erro", err)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, ids, errors.Join(errs...)
}

// pruneImages remove as imagens do projeto além da cota de retenção. A imagem
// da release ativa nunca sai; uma imagem em uso fica.
func (w *Worker) pruneImages(ctx context.Context, projectID string, keep int, activeImageID string) ([]string, error) {
	images, err := w.runtime.ListProjectImages(ctx, projectID)
	if err != nil {
		return nil, err
	}
	protected := map[string]bool{}
	if activeImageID != "" {
		protected[activeImageID] = true
	}
	var removed []string
	var errs []error
	for _, img := range dockerrt.SelectPrunable(images, keep, protected) {
		switch err := w.runtime.RemoveImage(ctx, img); {
		case err == nil:
			removed = append(removed, firstTag(img))
		case errors.Is(err, dockerrt.ErrImageInUse):
		default:
			errs = append(errs, err)
		}
	}
	return removed, errors.Join(errs...)
}

func firstTag(img dockerrt.ProjectImage) string {
	if len(img.Tags) > 0 {
		return img.Tags[0]
	}
	return img.ID
}

// runOperation executa uma operação reivindicada e grava o resultado.
func (w *Worker) runOperation(ctx context.Context, op *domain.Operation) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()

	var result map[string]any
	var err error
	permanent := false
	switch op.Kind {
	case domain.OpRetireReleases:
		result, err = w.retireReleases(ctx, op)
	case domain.OpDeleteProject:
		result, err = w.deleteProject(ctx, op)
	case domain.OpApplyLimits:
		result, err = w.applyLimits(ctx, op)
		var below *dockerrt.ErrBelowUsage
		permanent = errors.Is(err, errNoActiveRelease) || errors.As(err, &below)
	case domain.OpDeleteService:
		result, err = w.deleteService(ctx, op)
	case domain.OpDeleteVolume:
		result, err = w.deleteVolume(ctx, op)
		permanent = errors.Is(err, errVolumeInUse)
	case domain.OpStopApp:
		result, err = w.stopApp(ctx, op)
		permanent = errors.Is(err, errNoActiveRelease)
	case domain.OpStartApp:
		result, err = w.startApp(ctx, op)
		permanent = errors.Is(err, errNoActiveRelease) || errors.Is(err, errStartNotReady)
	case domain.OpRestartApp:
		result, err = w.restartApp(ctx, op)
		permanent = errors.Is(err, errNoActiveRelease) || errors.Is(err, errStartNotReady)
	default:
		err, permanent = fmt.Errorf("tipo de operação desconhecido: %s", op.Kind), true
	}

	// O contexto da operação pode ter vencido; gravar o desfecho não.
	save, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer done()
	if err != nil {
		w.log.Warn("operação falhou", "operação", op.ID, "tipo", op.Kind, "tentativa", op.Attempts, "erro", err)
		if ferr := w.store.FailOperation(save, op.ID, err.Error(), !permanent, opMaxAttempts, opBackoff, result); ferr != nil {
			w.log.Error("registrar a falha da operação", "operação", op.ID, "erro", ferr)
		}
		return
	}
	if ferr := w.store.FinishOperation(save, op.ID, result); ferr != nil {
		w.log.Error("concluir a operação", "operação", op.ID, "erro", ferr)
	}
}

var errNoActiveRelease = errors.New("o projeto não tem release no ar")

func opProject(op *domain.Operation) (uuid.UUID, error) {
	if op.ProjectID == nil {
		return uuid.Nil, errors.New("operação sem projeto")
	}
	return *op.ProjectID, nil
}

// retireReleases remove as releases substituídas do projeto e poda as imagens
// além da cota (DEC-38 itens 2 e 3).
func (w *Worker) retireReleases(ctx context.Context, op *domain.Operation) (map[string]any, error) {
	projectID, err := opProject(op)
	if err != nil {
		return nil, err
	}
	policy, err := w.settings.Lifecycle(ctx)
	if err != nil {
		return nil, err
	}
	containers, facts, _, err := w.containersWithFacts(ctx, projectID.String())
	if err != nil {
		return nil, err
	}
	gone, ids, retireErr := w.retireEligible(ctx, containers, facts, time.Duration(policy.GraceSeconds)*time.Second)
	if err := w.store.MarkReleasesRetired(ctx, ids); err != nil {
		return nil, err
	}

	activeImage := ""
	if active, err := w.store.ActiveRelease(ctx, projectID); err == nil {
		activeImage = active.ImageID
	}
	pruned, pruneErr := w.pruneImages(ctx, projectID.String(), policy.ImagesKept, activeImage)

	result := map[string]any{"containers_removed": gone, "images_removed": pruned, "images_kept": policy.ImagesKept}
	return result, errors.Join(retireErr, pruneErr)
}

// deleteProject remove o que o projeto excluído deixou no Docker: contêineres,
// rede e imagens. O histórico e os volumes ficam (DEC-38 item 5).
func (w *Worker) deleteProject(ctx context.Context, op *domain.Operation) (map[string]any, error) {
	projectID, err := opProject(op)
	if err != nil {
		return nil, err
	}
	project, err := w.store.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project.DeletedAt == nil {
		// Defesa em profundidade: a API só cria esta operação depois de marcar
		// o projeto, e remover o que é de um projeto vivo seria um desastre.
		return nil, errors.New("o projeto não está marcado como excluído")
	}

	containers, err := w.runtime.ListManaged(ctx, projectID.String())
	if err != nil {
		return nil, err
	}
	var gone []string
	var errs []error
	for _, c := range containers {
		if err := w.runtime.RetireContainer(ctx, c.ID); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.Name, err))
			continue
		}
		gone = append(gone, c.Name)
	}
	if err := w.runtime.RemoveProjectNetwork(ctx, projectID.String()); err != nil {
		errs = append(errs, err)
	}
	images, err := w.runtime.ListProjectImages(ctx, projectID.String())
	if err != nil {
		errs = append(errs, err)
	}
	var removedImages []string
	for _, img := range images {
		switch err := w.runtime.RemoveImage(ctx, img); {
		case err == nil:
			removedImages = append(removedImages, firstTag(img))
		default:
			errs = append(errs, err)
		}
	}
	if err := w.store.MarkProjectReleasesRemoved(ctx, projectID); err != nil {
		errs = append(errs, err)
	}
	// Os dados (DEC-41 item 5): só saem se o usuário marcou, na confirmação.
	volumesRemoved, volumesKept, volumeErrs := w.deleteProjectData(ctx, project, op)
	errs = append(errs, volumeErrs...)
	sort.Strings(gone)
	w.log.Info("projeto excluído: recursos removidos", "projeto", project.Slug, "contêineres", len(gone), "imagens", len(removedImages))
	result := map[string]any{"slug": project.Slug, "containers_removed": gone, "images_removed": removedImages}
	if len(volumesRemoved) > 0 {
		result["volumes_removed"] = volumesRemoved
	}
	if len(volumesKept) > 0 {
		result["volumes_kept"] = volumesKept
	}
	return result, errors.Join(errs...)
}

// applyLimits aplica os limites efetivos de hoje ao contêiner da release ativa
// (#33). Recusar reduzir abaixo do uso é um desfecho definitivo, não uma falha
// a tentar de novo.
func (w *Worker) applyLimits(ctx context.Context, op *domain.Operation) (map[string]any, error) {
	projectID, err := opProject(op)
	if err != nil {
		return nil, err
	}
	project, err := w.store.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if project.DeletedAt != nil {
		return nil, errors.New("o projeto foi excluído")
	}
	active, err := w.store.ActiveRelease(ctx, projectID)
	if errors.Is(err, store.ErrNoActiveRelease) {
		return nil, errNoActiveRelease
	}
	if err != nil {
		return nil, err
	}
	limits, err := w.settings.Limits(ctx, project.Limits)
	if err != nil {
		return nil, err
	}
	before, after, err := w.runtime.UpdateLimits(ctx, active.ContainerID, limits)
	result := map[string]any{"container": active.ContainerID[:12], "before": before, "requested": map[string]any{
		"memory_mb": limits.AppMemoryMB, "cpus": limits.AppCPUs, "pids": limits.AppPids,
	}}
	if err != nil {
		return result, err
	}
	result["after"] = after
	result["changed"] = before != after
	// Os processos da release têm os mesmos limites do web.
	if procs, perr := w.runtime.ListProcesses(ctx, active.DeploymentID.String()); perr == nil {
		for _, c := range procs {
			if _, _, uerr := w.runtime.UpdateLimits(ctx, c.ID, limits); uerr != nil {
				w.log.Warn("aplicar limites ao processo", "processo", c.Process, "erro", uerr)
			}
		}
	}
	if err := w.store.AppendEvent(ctx, active.DeploymentID, "lifecycle", "limits_applied", map[string]any{"before": before, "after": after}); err != nil {
		w.log.Warn("registrar os limites no histórico do deploy", "deploy", active.DeploymentID, "erro", err)
	}
	return result, nil
}

// sweepOnce varre os contêineres gerenciados de todos os projetos e remove os
// elegíveis (DEC-38 item 4). Só grava uma operação quando removeu algo. Também
// mede os volumes (DEC-41).
func (w *Worker) sweepOnce(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	w.measureVolumes(ctx)

	policy, err := w.settings.Lifecycle(ctx)
	if err != nil {
		w.log.Warn("faxina: ler a política", "erro", err)
		return
	}
	containers, facts, withDeployment, err := w.containersWithFacts(ctx, "")
	if err != nil {
		w.log.Warn("faxina: listar contêineres", "erro", err)
		return
	}
	gone, ids, retireErr := w.retireEligible(ctx, containers, facts, time.Duration(policy.GraceSeconds)*time.Second)
	if retireErr != nil {
		w.log.Warn("faxina: remoção parcial", "erro", retireErr)
	}
	if err := w.store.MarkReleasesRetired(ctx, ids); err != nil {
		w.log.Warn("faxina: marcar releases aposentadas", "erro", err)
	}
	if len(gone) == 0 {
		return
	}
	result := map[string]any{"containers_removed": gone, "scanned": len(containers), "with_known_deployment": len(facts), "with_deployment_label": withDeployment}
	if err := w.store.RecordOperation(ctx, nil, domain.OpSweep, result); err != nil {
		w.log.Warn("faxina: registrar a operação", "erro", err)
	}
}

// scheduleRetire agenda a aposentadoria das releases substituídas, para depois
// do período de espera. Uma falha aqui não derruba o deploy: a faxina periódica
// é a rede de segurança.
func (w *Worker) scheduleRetire(ctx context.Context, projectID uuid.UUID) {
	policy, err := w.settings.Lifecycle(ctx)
	if err != nil {
		w.log.Warn("agendar aposentadoria: ler a política", "erro", err)
		return
	}
	at := time.Now().UTC().Add(time.Duration(policy.GraceSeconds) * time.Second)
	if _, err := w.store.EnqueueOperation(ctx, &projectID, domain.OpRetireReleases, nil, at, nil); err != nil {
		w.log.Warn("agendar aposentadoria das releases", "projeto", projectID, "erro", err)
	}
}
