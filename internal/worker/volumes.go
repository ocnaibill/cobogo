package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

// Dados persistentes (DEC-41): criar os volumes dos caminhos declarados, trocar
// de release sem dois processos no mesmo volume e apagar o que o usuário pediu.

// prepareVolumes cria (se faltar) o volume de cada caminho persistente do
// projeto e o registra. Os recém-criados passam a ser do usuário da aplicação.
// Devolve caminho -> volume e os caminhos criados agora.
func (w *Worker) prepareVolumes(ctx context.Context, rt *dockerrt.Runtime, project domain.Project, imageTag string) (map[string]string, []string, error) {
	volumes := map[string]string{}
	var created []string
	fresh := map[string]string{}
	for _, path := range project.PersistentPaths {
		name := dockerrt.VolumeName(project.ID.String(), path)
		isNew, err := rt.EnsureVolume(ctx, name, project.ID.String(), path)
		if err != nil {
			return nil, nil, err
		}
		if err := w.store.UpsertProjectVolume(ctx, name, project.ID, path); err != nil {
			return nil, nil, err
		}
		volumes[path] = name
		if isNew {
			created = append(created, path)
			fresh[path] = name
		}
	}
	if len(fresh) > 0 {
		user, err := rt.ImageUser(ctx, imageTag)
		if err == nil {
			err = rt.ChownVolumes(ctx, imageTag, user, project.ID.String(), fresh)
		}
		if err != nil {
			// A aplicação ainda pode funcionar (por exemplo, se roda como root ou
			// a imagem já tinha o caminho): é um aviso, não uma falha do deploy.
			w.log.Warn("dar a posse dos volumes novos ao usuário da aplicação", "projeto", project.Slug, "erro", err)
		}
	}
	sort.Strings(created)
	return volumes, created, nil
}

// pausedRelease é a release ativa que parou para dar o volume à candidata.
type pausedRelease struct{ release domain.Release }

// pauseActive para a release ativa do projeto antes de a candidata subir (DEC-41
// item 4): dois processos no mesmo volume, com SQLite em WAL, corrompem dados. Se
// ela já estava parada ou não há release, não há o que pausar. Se não for
// possível pará-la, o deploy não segue.
func (w *Worker) pauseActive(ctx context.Context, project domain.Project) (*pausedRelease, error) {
	active, err := w.store.ActiveRelease(ctx, project.ID)
	if errors.Is(err, store.ErrNoActiveRelease) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if active.StoppedAt != nil {
		return nil, nil
	}
	if running, _, err := w.runtime.State(ctx, active.ContainerID); err == nil && !running {
		return nil, nil
	}
	if err := w.runtime.StopContainer(ctx, active.ContainerID); err != nil {
		return nil, fmt.Errorf("parar a release anterior antes de usar o volume: %w", err)
	}
	return &pausedRelease{release: active}, nil
}

// resumeActive inicia de novo a release que foi pausada, porque a candidata não
// subiu. Melhor esforço: o deploy já falhou e o motivo é o dele.
func (w *Worker) resumeActive(ctx context.Context, p *pausedRelease) error {
	port, _ := w.portAndHealthOf(ctx, p.release)
	_, err := w.runtime.StartContainer(ctx, p.release.ContainerID, port)
	return err
}

// deleteProjectData apaga os volumes de um projeto excluído, se o usuário pediu
// (DEC-41 item 5). Sem o pedido, eles ficam e aparecem como volumes sem projeto.
func (w *Worker) deleteProjectData(ctx context.Context, project domain.Project, op *domain.Operation) (removed, kept []string, errs []error) {
	names, err := w.store.ProjectVolumeNames(ctx, project.ID)
	if err != nil {
		return nil, nil, []error{err}
	}
	if del, _ := op.Params["delete_data"].(bool); !del {
		return nil, names, nil
	}
	for _, name := range names {
		if err := w.runtime.RemoveVolume(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		if err := w.store.MarkVolumeDeleted(ctx, name); err != nil {
			errs = append(errs, err)
			continue
		}
		removed = append(removed, name)
	}
	return removed, nil, errs
}

// deleteVolume apaga o volume sem projeto de uma operação `delete_volume`. A
// operação é revalidada aqui: um volume que voltou a ser usado não é apagado.
func (w *Worker) deleteVolume(ctx context.Context, op *domain.Operation) (map[string]any, error) {
	name, _ := op.Params["name"].(string)
	if name == "" {
		return nil, errors.New("operação sem nome de volume")
	}
	v, err := w.store.GetVolume(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return map[string]any{"volume": name, "already_gone": true}, nil
	}
	if err != nil {
		return nil, err
	}
	if !v.Orphan {
		return nil, errVolumeInUse
	}
	if err := w.runtime.RemoveVolume(ctx, name); err != nil {
		return nil, err
	}
	if err := w.store.MarkVolumeDeleted(ctx, name); err != nil {
		return nil, err
	}
	return map[string]any{"volume": name, "path": v.Path}, nil
}

var errVolumeInUse = errors.New("o volume voltou a ser usado por um caminho persistente do projeto")

// measureVolumes grava o tamanho dos volumes, para o painel mostrá-lo.
func (w *Worker) measureVolumes(ctx context.Context) {
	sizes, err := w.runtime.VolumeSizes(ctx)
	if err != nil {
		w.log.Warn("medir os volumes", "erro", err)
		return
	}
	if err := w.store.UpdateVolumeSizes(ctx, sizes); err != nil {
		w.log.Warn("gravar o tamanho dos volumes", "erro", err)
	}
}
