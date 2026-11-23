package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/backup"
	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
)

// Backup dos dados de um projeto (DEC-47, parte B). Um laço próprio, fora do dos
// deploys: um backup de minutos não segura um build. Um por vez na máquina. A cópia
// é feita por contêineres descartáveis (um `tar` somente leitura por volume e um
// `pg_dump` pela rede do projeto); o worker só monta as pastas, confere o espaço,
// grava o manifest e aplica a retenção.

const (
	projectBackupPoll    = 2 * time.Second
	projectBackupTimeout = 45 * time.Minute
	copyTimeout          = 20 * time.Minute
	// backupPostgresMajor é a versão do PostgreSQL gerenciado (DEC-42).
	backupPostgresMajor = "16"
)

// runProjectBackups é o laço dos backups de projeto.
func (w *Worker) runProjectBackups(ctx context.Context) {
	if n, err := w.store.InterruptRunningProjectBackups(ctx); err != nil {
		w.log.Warn("interromper os backups que ficaram rodando", "erro", err)
	} else if n > 0 {
		w.log.Warn("backups interrompidos pelo reinício do worker", "quantos", n)
	}
	if n, err := w.store.InterruptRunningRestores(ctx); err != nil {
		w.log.Warn("interromper as restaurações que ficaram rodando", "erro", err)
	} else if n > 0 {
		w.log.Warn("restaurações interrompidas pelo reinício do worker", "quantas", n)
	}
	if n, err := w.runtime.RemoveBackupCopyContainers(ctx); err != nil {
		w.log.Warn("remover contêineres de cópia sobrados", "erro", err)
	} else if n > 0 {
		w.log.Info("contêineres de cópia sobrados removidos", "quantos", n)
	}
	for {
		w.deleteMarkedBackups(ctx)
		// A restauração vai antes de um backup novo, depois que o de segurança dela terminou;
		// se o de segurança falhou, ela falha sem ter tocado em nada.
		if _, err := w.store.FailBlockedRestores(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn("encerrar restaurações bloqueadas", "erro", err)
		}
		if r, err := w.store.ClaimNextReadyRestore(ctx); err != nil && ctx.Err() == nil {
			w.log.Error("reivindicar restauração", "erro", err)
		} else if r != nil {
			w.executeRestore(ctx, r)
			continue
		}
		b, err := w.store.ClaimNextProjectBackup(ctx)
		if err != nil && ctx.Err() == nil {
			w.log.Error("reivindicar backup de projeto", "erro", err)
		}
		if b != nil {
			w.executeProjectBackup(ctx, b)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(projectBackupPoll):
		}
	}
}

// deleteMarkedBackups apaga os arquivos dos backups com o apagar pedido (pela pessoa
// ou pela retenção) e fecha o registro. Se a pasta não pôde sair, o registro fica e
// a próxima volta tenta de novo.
func (w *Worker) deleteMarkedBackups(ctx context.Context) {
	list, err := w.store.BackupsToDelete(ctx)
	if err != nil {
		if ctx.Err() == nil {
			w.log.Warn("listar backups a apagar", "erro", err)
		}
		return
	}
	for _, b := range list {
		if w.backupDir != "" {
			if err := backup.RemoveProjectDir(w.backupDir, b.ProjectID, b.ID); err != nil {
				w.log.Warn("apagar os arquivos do backup", "backup", b.ID, "erro", err)
				continue
			}
		}
		if err := w.store.MarkProjectBackupDeleted(ctx, b.ID); err != nil {
			w.log.Warn("fechar o registro do backup", "backup", b.ID, "erro", err)
		}
	}
}

// executeProjectBackup faz um backup reivindicado até o desfecho. Qualquer falha
// apaga o que já tinha sido copiado: um backup incompleto nunca fica parecendo bom.
func (w *Worker) executeProjectBackup(ctx context.Context, b *domain.ProjectBackup) {
	ctx, cancel := context.WithTimeout(ctx, projectBackupTimeout)
	defer cancel()
	manifest := domain.BackupManifest{ProjectID: b.ProjectID.String(), CreatedAt: time.Now().UTC(), PausedApp: false, Reason: b.Reason}
	finish := func(state string, size int64, msg string) {
		save, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
		defer done()
		if err := w.store.FinishProjectBackup(save, b.ID, state, size, manifest, msg); err != nil {
			w.log.Error("gravar o desfecho do backup", "backup", b.ID, "erro", err)
		}
		w.log.Info("backup de projeto concluído", "backup", b.ID, "projeto", b.ProjectID, "estado", state)
	}
	fail := func(msg string) {
		if w.backupDir != "" {
			_ = backup.RemoveProjectDir(w.backupDir, b.ProjectID, b.ID)
		}
		manifest.Items = nil
		finish(domain.BackupFailed, 0, msg)
	}

	if w.backupDir == "" || w.backupVolume == "" {
		fail("o backup dos dados não está configurado neste worker (COBOGO_BACKUP_DIR e COBOGO_BACKUP_VOLUME)")
		return
	}
	project, err := w.store.GetProject(ctx, b.ProjectID)
	if err != nil || project.DeletedAt != nil {
		fail("o projeto não existe mais")
		return
	}
	manifest.ProjectSlug = project.Slug

	// O que copiar: os caminhos declarados e o banco gerenciado.
	var svc *domain.Service
	if s, err := w.store.GetService(ctx, project.ID, domain.ServicePostgres); err == nil && s.Mode == domain.ServiceManaged && s.Status != domain.ServiceDeleting {
		svc = &s
	}
	paths := append([]string(nil), project.PersistentPaths...)
	sort.Strings(paths)
	if len(paths) == 0 && svc == nil {
		fail("o projeto não tem dados para copiar")
		return
	}

	// Espaço: o que se vai copiar mais a folga que tem de sobrar no disco.
	var estimate int64
	if project.OwnerID != nil {
		if vols, err := w.store.ListVolumesFor(ctx, project.ID, *project.OwnerID); err == nil {
			for _, v := range vols {
				if v.SizeBytes != nil {
					estimate += *v.SizeBytes
				}
			}
		}
	}
	free, err := backup.FreeBytes(w.backupDir)
	if err != nil {
		fail("não foi possível medir o espaço livre: " + err.Error())
		return
	}
	if free < uint64(estimate)+uint64(domain.BackupMinFreeBytes) {
		fail(fmt.Sprintf("espaço insuficiente: %s livres, o backup deve ocupar cerca de %s e precisam sobrar %s", humanBytes(int64(free)), humanBytes(estimate), humanBytes(domain.BackupMinFreeBytes)))
		return
	}

	// A pasta do backup, que o contêiner de cópia monta como subpasta e só ela.
	dir := backup.ProjectDir(w.backupDir, project.ID, b.ID)
	if err := mkdirOpen(w.backupDir, dir); err != nil {
		fail("criar a pasta do backup: " + err.Error())
		return
	}
	sub := filepath.ToSlash(filepath.Join(backup.ProjectsDir, project.ID.String(), b.ID.String()))
	rt := w.runtime
	pid, bid := project.ID.String(), b.ID.String()

	// Pausar a aplicação (opção da pessoa) só importa para os volumes: o banco sai por pg_dump,
	// consistente por construção. Se já estava parada, continua parada.
	if b.PauseApp && len(paths) > 0 {
		if active, err := w.store.ActiveRelease(ctx, project.ID); err == nil && active.StoppedAt == nil {
			op := &domain.Operation{ProjectID: &project.ID}
			if _, err := w.stopApp(ctx, op); err != nil {
				fail("não foi possível pausar a aplicação: " + err.Error())
				return
			}
			manifest.PausedApp = true
			defer func() {
				resume, done := context.WithTimeout(context.WithoutCancel(ctx), opTimeout)
				defer done()
				if _, err := w.startApp(resume, op); err != nil {
					w.log.Error("reiniciar a aplicação depois do backup", "projeto", project.Slug, "erro", err)
				}
			}()
		}
	}

	for _, path := range paths {
		name := dockerrt.VolumeName(pid, path)
		exists, err := rt.VolumeExists(ctx, name)
		if err != nil {
			fail(err.Error())
			return
		}
		if !exists {
			manifest.Skipped = append(manifest.Skipped, path+": o volume ainda não foi criado (nenhum deploy usou este caminho)")
			continue
		}
		file := backup.VolumeFileName(path)
		if err := rt.TarVolume(ctx, pid, bid, name, w.backupVolume, sub, file, copyTimeout); err != nil {
			fail(fmt.Sprintf("copiar %s: %v", path, err))
			return
		}
		size, sum, err := backup.FileInfo(filepath.Join(dir, file))
		if err != nil {
			fail(fmt.Sprintf("conferir a cópia de %s: %v", path, err))
			return
		}
		manifest.Items = append(manifest.Items, domain.BackupItem{Kind: domain.BackupItemVolume, Path: path, File: file, Size: size, SHA256: sum})
	}

	if svc != nil {
		pass, err := w.databasePassword(ctx, project, *svc)
		if err != nil {
			fail("ler a senha do banco: " + err.Error())
			return
		}
		if err := rt.DumpService(ctx, svc.Engine, pid, bid, svc.ContainerName, pass, dockerrt.ProjectNetwork(pid), w.backupVolume, sub, backup.DatabaseFile, copyTimeout); err != nil {
			fail("copiar o banco: " + err.Error())
			return
		}
		size, sum, err := backup.FileInfo(filepath.Join(dir, backup.DatabaseFile))
		if err != nil || size == 0 {
			fail("conferir a cópia do banco: o dump saiu vazio")
			return
		}
		manifest.Postgres = backupPostgresMajor
		manifest.Items = append(manifest.Items, domain.BackupItem{Kind: domain.BackupItemPostgres, File: backup.DatabaseFile, Size: size, SHA256: sum})
	}

	if len(manifest.Items) == 0 {
		fail("nada foi copiado: " + joinSkipped(manifest.Skipped))
		return
	}
	if err := backup.WriteManifest(dir, manifest); err != nil {
		fail("gravar o manifest: " + err.Error())
		return
	}
	var total int64
	for _, it := range manifest.Items {
		total += it.Size
	}
	finish(domain.BackupSucceeded, total, "")
	w.pruneProjectBackups(ctx, project.ID)
}

// pruneProjectBackups marca para apagar os backups que passam da retenção do projeto.
func (w *Worker) pruneProjectBackups(ctx context.Context, projectID uuid.UUID) {
	keep, err := w.settings.BackupProjectKeep(ctx)
	if err != nil || keep < 1 {
		return
	}
	all, err := w.store.SucceededProjectBackups(ctx, projectID)
	if err != nil {
		w.log.Warn("ler os backups para a retenção", "erro", err)
		return
	}
	protected, err := w.store.ProtectedBackupIDs(ctx, projectID)
	if err != nil {
		w.log.Warn("ler os backups protegidos", "erro", err)
		return
	}
	for _, id := range backup.PrunePlan(all, keep, protected) {
		if err := w.store.MarkProjectBackupForDelete(ctx, id); err != nil {
			w.log.Warn("marcar backup antigo para apagar", "backup", id, "erro", err)
		}
	}
}

// mkdirOpen cria a pasta do backup e as de cima, abertas para escrita: o contêiner de
// cópia roda com outro usuário e só enxerga esta subpasta.
func mkdirOpen(root, dir string) error {
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return err
	}
	// Só as pastas que o worker criou (projects/<projeto> e projects/<projeto>/<backup>): a
	// pasta `projects` é do serviço de backup, que a deixa aberta (e a que está acima dela, não).
	stop := filepath.Join(root, backup.ProjectsDir)
	for p := dir; p != stop && len(p) > len(stop); p = filepath.Dir(p) {
		if err := os.Chmod(p, 0o777); err != nil {
			return err
		}
	}
	return nil
}

func joinSkipped(s []string) string {
	if len(s) == 0 {
		return "sem caminhos"
	}
	out := s[0]
	for _, x := range s[1:] {
		out += "; " + x
	}
	return out
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}
