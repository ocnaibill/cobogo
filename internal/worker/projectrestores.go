package worker

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/ocnaibill/cobogo/internal/backup"
	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
)

// Restauração dos dados de um projeto (DEC-47, parte C). Roda no mesmo laço dos backups
// (um por vez na máquina), só depois de o backup de segurança terminar bem. Confere o
// backup antes de tocar em qualquer coisa; pausa a aplicação; troca cada volume por um
// caminho que não passa por estado pela metade e o banco numa transação só; e inicia a
// aplicação de novo se ela estava no ar. Nunca cria um volume que o projeto não declara nem
// apaga volumes.

const restoreTimeout = 45 * time.Minute

// executeRestore faz uma restauração reivindicada até o desfecho.
func (w *Worker) executeRestore(ctx context.Context, r *domain.ProjectRestore) {
	ctx, cancel := context.WithTimeout(ctx, restoreTimeout)
	defer cancel()
	var result domain.RestoreResult
	finish := func(state, msg string) {
		save, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
		defer done()
		if result.Volumes == nil {
			result.Volumes = []string{}
		}
		if err := w.store.FinishRestore(save, r.ID, state, result, msg); err != nil {
			w.log.Error("gravar o desfecho da restauração", "restauração", r.ID, "erro", err)
		}
		w.log.Info("restauração concluída", "restauração", r.ID, "projeto", r.ProjectID, "estado", state)
	}
	step := func(s string) {
		if err := w.store.SetRestoreStep(ctx, r.ID, s); err != nil {
			w.log.Warn("registrar o passo da restauração", "erro", err)
		}
	}
	// Depois do primeiro volume tocado, uma falha deixa o projeto em estado misto: a mensagem
	// aponta o backup de segurança, que tem o estado de antes.
	safetyHint := fmt.Sprintf(" O estado de antes está no backup de segurança %s (aba Dados): restaure-o para voltar.", r.SafetyBackupID)

	if w.backupDir == "" || w.backupVolume == "" {
		finish(domain.RestoreFailed, "o backup dos dados não está configurado neste worker")
		return
	}
	project, err := w.store.GetProject(ctx, r.ProjectID)
	if err != nil || project.DeletedAt != nil {
		finish(domain.RestoreFailed, "o projeto não existe mais")
		return
	}
	b, err := w.store.ProjectBackupByID(ctx, r.BackupID)
	if err != nil || b.State != domain.BackupSucceeded || b.DeleteRequested {
		finish(domain.RestoreFailed, "o backup a restaurar não está mais disponível")
		return
	}
	dir := backup.ProjectDir(w.backupDir, project.ID, b.ID)
	m, err := backup.ReadManifest(dir)
	if err != nil {
		finish(domain.RestoreFailed, "os arquivos do backup não foram encontrados na VM: "+err.Error()+". Nada foi alterado.")
		return
	}

	// 1. Conferir o backup inteiro antes de tocar em qualquer coisa.
	step("conferindo o backup")
	if m.Version != backup.ManifestVersion {
		finish(domain.RestoreFailed, fmt.Sprintf("este backup tem o formato %d e esta versão do Cobogó restaura o formato %d. Nada foi alterado.", m.Version, backup.ManifestVersion))
		return
	}
	for _, it := range m.Items {
		if !backup.ValidArtifact(it.File) {
			finish(domain.RestoreFailed, "o manifest do backup cita um arquivo inválido. Nada foi alterado.")
			return
		}
		size, sum, err := backup.FileInfo(filepath.Join(dir, it.File))
		if err != nil || size != it.Size || sum != it.SHA256 {
			finish(domain.RestoreFailed, fmt.Sprintf("o arquivo %s do backup está ausente ou corrompido (o tamanho ou a soma SHA-256 não conferem). Nada foi alterado.", it.File))
			return
		}
	}

	// 2. O que dá para restaurar neste projeto, hoje.
	var svc *domain.Service
	if s, err := w.store.GetDatabaseService(ctx, project.ID); err == nil && s.Mode == domain.ServiceManaged && s.Status != domain.ServiceDeleting {
		svc = &s
	}
	var volumes []domain.BackupItem
	var dump *domain.BackupItem
	for i := range m.Items {
		it := m.Items[i]
		switch it.Kind {
		case domain.BackupItemVolume:
			if slices.Contains(project.PersistentPaths, it.Path) {
				volumes = append(volumes, it)
			} else {
				result.Skipped = append(result.Skipped, it.Path+": o projeto não declara mais este caminho")
			}
		case domain.BackupItemPostgres, domain.BackupItemMySQL:
			switch {
			case svc == nil:
				result.Skipped = append(result.Skipped, "banco: há um dump, mas o projeto não tem banco gerenciado (crie-o na aba Banco e restaure de novo)")
			case svc.Engine != domain.EngineOfBackupItem(it.Kind):
				finish(domain.RestoreFailed, fmt.Sprintf("o dump do banco é de %s e o banco gerenciado do projeto é %s. Nada foi alterado.", domain.EngineOfBackupItem(it.Kind), svc.Engine))
				return
			case it.Kind == domain.BackupItemMySQL && m.MySQL != "" && m.MySQL != backupMySQLVersion:
				finish(domain.RestoreFailed, fmt.Sprintf("o dump do banco é do MySQL %s e o banco gerenciado é o %s. Nada foi alterado.", m.MySQL, backupMySQLVersion))
				return
			case it.Kind == domain.BackupItemPostgres && m.Postgres != "" && m.Postgres != backupPostgresMajor:
				finish(domain.RestoreFailed, fmt.Sprintf("o dump do banco é do PostgreSQL %s e o banco gerenciado é o %s. Nada foi alterado.", m.Postgres, backupPostgresMajor))
				return
			default:
				dump = &it
			}
		}
	}
	if len(volumes) == 0 && dump == nil {
		finish(domain.RestoreFailed, "nada a restaurar neste projeto: "+joinSkipped(result.Skipped)+". Nada foi alterado.")
		return
	}

	// 3. Pausar a aplicação (se estava no ar) e restaurar.
	var op *domain.Operation
	wasRunning := false
	if active, err := w.store.ActiveRelease(ctx, project.ID); err == nil && active.StoppedAt == nil {
		wasRunning = true
		op = &domain.Operation{ProjectID: &project.ID}
		step("pausando a aplicação")
		if _, err := w.stopApp(ctx, op); err != nil {
			finish(domain.RestoreFailed, "não foi possível pausar a aplicação: "+err.Error()+". Nada foi alterado.")
			return
		}
	}
	restart := func() {
		if !wasRunning {
			return
		}
		step("iniciando a aplicação")
		resume, done := context.WithTimeout(context.WithoutCancel(ctx), opTimeout)
		defer done()
		if _, err := w.startApp(resume, op); err != nil {
			result.Warnings = append(result.Warnings, "a aplicação não ficou pronta depois da restauração: "+err.Error())
			return
		}
		result.AppRestarted = true
	}

	rt := w.runtime
	pid, bid := project.ID.String(), b.ID.String()
	sub := filepath.ToSlash(filepath.Join(backup.ProjectsDir, pid, bid))
	touched := false
	fail := func(msg string) {
		if touched {
			// Dados em estado misto: a aplicação continua parada, para não rodar sobre eles.
			if wasRunning {
				msg += ". A aplicação continua parada para não rodar sobre dados misturados"
			}
			finish(domain.RestoreFailed, msg+"."+safetyHint)
			return
		}
		restart()
		finish(domain.RestoreFailed, msg+". Nada foi alterado.")
	}

	for _, it := range volumes {
		step("restaurando o volume " + it.Path)
		name := dockerrt.VolumeName(pid, it.Path)
		if _, err := rt.EnsureVolume(ctx, name, pid, it.Path); err != nil {
			fail(fmt.Sprintf("preparar o volume %s: %v", it.Path, err))
			return
		}
		if err := rt.RestoreVolume(ctx, pid, bid, name, w.backupVolume, sub, it.File, copyTimeout); err != nil {
			// Uma extração que falha não toca no conteúdo; ainda assim, se um volume anterior
			// já foi trocado, o estado é misto.
			fail(fmt.Sprintf("restaurar o volume %s: %v", it.Path, err))
			return
		}
		touched = true
		result.Volumes = append(result.Volumes, it.Path)
	}
	if dump != nil {
		step("restaurando o banco")
		pass, err := w.databasePassword(ctx, project, *svc)
		if err != nil {
			fail("ler a senha do banco: " + err.Error())
			return
		}
		if err := rt.RestoreService(ctx, svc.Engine, pid, bid, svc.ContainerName, pass, dockerrt.ProjectNetwork(pid), w.backupVolume, sub, dump.File, copyTimeout); err != nil {
			fail("restaurar o banco: " + err.Error())
			return
		}
		touched = true
		result.Database = true
	}

	restart()
	finish(domain.RestoreSucceeded, "")
}
