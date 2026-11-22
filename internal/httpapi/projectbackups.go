package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/backup"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

// Backup dos dados de um projeto (DEC-47, parte B). A API registra o pedido, lê o
// volume de backups (somente leitura) para o download e marca o pedido de apagar;
// quem copia e apaga os arquivos é o worker. Cada dono baixa os backups dos seus
// projetos; o download exige a senha digitada de novo (os dados não são cifrados).

const (
	codeBackupActive    = "backup_active"
	codeNothingToBackUp = "nothing_to_back_up"
	codeBackupsOff      = "backups_unavailable"
	codeBackupBusy      = "backup_busy"
	codeRestoreActive   = "restore_active"
	codeNotUsable       = "backup_not_usable"
	codeNothingRestore  = "nothing_to_restore"
)

type projectBackupsView struct {
	Backups []domain.ProjectBackup `json:"backups"`
	// Configured diz se o painel enxerga o volume de backups (o download precisa).
	Configured bool `json:"configured"`
	// HasContent diz se o projeto tem o que copiar (caminho declarado ou banco gerenciado).
	HasContent bool `json:"has_content"`
	// Keep é quantos backups o projeto guarda.
	Keep int `json:"keep"`
	// Restores são as restaurações mais recentes (a em andamento, com o passo, primeiro).
	Restores []domain.ProjectRestore `json:"restores"`
}

func (s *Server) projectHasContent(r *http.Request, p domain.Project) (bool, error) {
	if len(p.PersistentPaths) > 0 {
		return true, nil
	}
	svc, err := s.store.GetService(r.Context(), p.ID, domain.ServicePostgres)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return svc.Mode == domain.ServiceManaged && svc.Status != domain.ServiceDeleting, nil
}

func (s *Server) handleListProjectBackups(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	list, err := s.store.ListProjectBackupsFor(r.Context(), p.ID, user.ID)
	if err != nil {
		s.log.Error("listar backups do projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	has, err := s.projectHasContent(r, p)
	if err != nil {
		s.log.Error("conferir o conteúdo do projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	restores, err := s.store.ListRestoresFor(r.Context(), p.ID, user.ID, 5)
	if err != nil {
		s.log.Error("listar restaurações", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	keep, _ := s.settings.BackupProjectKeep(r.Context())
	configured := false
	if s.backupDir != "" {
		if _, err := os.Stat(s.backupDir); err == nil {
			configured = true
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, projectBackupsView{Backups: list, Configured: configured, HasContent: has, Keep: keep, Restores: restores})
}

type createProjectBackupRequest struct {
	// PauseApp para a aplicação durante a cópia dos volumes. Padrão false.
	PauseApp bool `json:"pause_app"`
	// Reason: `manual` (padrão) ou `before_delete` (o pedido de "backup antes de apagar").
	Reason string `json:"reason"`
}

func (s *Server) handleCreateProjectBackup(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	var req createProjectBackupRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	switch req.Reason {
	case "":
		req.Reason = domain.BackupManual
	case domain.BackupManual, domain.BackupBeforeDelete:
	default:
		writeError(w, http.StatusBadRequest, "reason deve ser manual ou before_delete")
		return
	}
	if s.backupDir == "" {
		writeCode(w, http.StatusConflict, codeBackupsOff, "o volume de backups não está disponível nesta instalação")
		return
	}
	b, err := s.store.CreateProjectBackup(r.Context(), p.ID, user.ID, req.Reason, req.PauseApp)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "projeto não encontrado")
	case errors.Is(err, store.ErrNothingToBackUp):
		writeCode(w, http.StatusConflict, codeNothingToBackUp, "o projeto não tem dados para copiar: declare um caminho persistente na aba Dados ou crie o banco do Cobogó")
	case errors.Is(err, store.ErrBackupActive):
		writeCode(w, http.StatusConflict, codeBackupActive, "já há um backup deste projeto em andamento; espere ele terminar")
	case errors.Is(err, store.ErrRestoreActive):
		writeCode(w, http.StatusConflict, codeRestoreActive, "há uma restauração deste projeto em andamento; espere ela terminar")
	case err != nil:
		s.log.Error("criar backup do projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.auditDetail(r, &user, "project.backup", p.ID.String(), "ok", map[string]any{"names": []string{b.ID.String(), req.Reason}})
		writeJSON(w, http.StatusAccepted, b)
	}
}

func (s *Server) projectBackupRef(w http.ResponseWriter, r *http.Request, user domain.User) (domain.Project, uuid.UUID, bool) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return domain.Project{}, uuid.Nil, false
	}
	id, err := uuid.Parse(r.PathValue("bid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id de backup inválido")
		return domain.Project{}, uuid.Nil, false
	}
	return p, id, true
}

func (s *Server) handleDeleteProjectBackup(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, id, ok := s.projectBackupRef(w, r, user)
	if !ok {
		return
	}
	b, err := s.store.RequestDeleteProjectBackup(r.Context(), p.ID, id, user.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "backup não encontrado")
	case errors.Is(err, store.ErrBackupBusy):
		writeCode(w, http.StatusConflict, codeBackupBusy, "o backup ainda está em andamento; espere ele terminar para apagar")
	case err != nil:
		s.log.Error("pedir para apagar o backup", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.audit(r, &user, "project.backup.delete", p.ID.String(), "ok")
		writeJSON(w, http.StatusAccepted, b)
	}
}

func (s *Server) handleDownloadProjectBackup(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, id, ok := s.projectBackupRef(w, r, user)
	if !ok {
		return
	}
	sess, _, ok := s.sessionOf(r)
	if !ok {
		writeCode(w, http.StatusUnauthorized, "unauthenticated", "sessão ausente ou expirada; entre novamente")
		return
	}
	if !reauthActive(sess) {
		s.audit(r, &user, "project.backup.download", p.ID.String(), "refused")
		writeCode(w, http.StatusForbidden, codeReauthRequired, "digite a sua senha de novo para baixar um backup")
		return
	}
	b, err := s.store.GetProjectBackupFor(r.Context(), p.ID, id, user.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (b.State != domain.BackupSucceeded || b.DeleteRequested)) {
		writeError(w, http.StatusNotFound, "backup não encontrado")
		return
	}
	if err != nil {
		s.log.Error("buscar backup do projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if s.backupDir == "" {
		writeCode(w, http.StatusConflict, codeBackupsOff, "o volume de backups não está disponível nesta instalação")
		return
	}
	dir := backup.ProjectDir(s.backupDir, p.ID, b.ID)
	prefix := fmt.Sprintf("%s-backup-%s", p.Slug, b.CreatedAt.UTC().Format("20060102-150405"))
	size, err := backup.ArchiveSize(dir, prefix)
	if err != nil {
		s.log.Error("medir o arquivo do backup", "erro", err)
		writeError(w, http.StatusNotFound, "os arquivos do backup não foram encontrados")
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", prefix+".tar"))
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Cache-Control", "no-store")
	s.audit(r, &user, "project.backup.download", p.ID.String(), "ok")
	rc := newProgressWriter(w)
	if err := backup.WriteArchive(io.Writer(rc), dir, prefix); err != nil {
		s.log.Warn("baixar backup do projeto interrompido", "backup", b.ID, "erro", err)
	}
}

func newProgressWriter(w http.ResponseWriter) progressWriter {
	return progressWriter{w: w, rc: http.NewResponseController(w)}
}

type restoreRequest struct {
	// ConfirmSlug é o nome do projeto, digitado: restaurar substitui o conteúdo atual dos volumes e do banco.
	ConfirmSlug string `json:"confirm_slug"`
}

type restoreResponse struct {
	Restore domain.ProjectRestore `json:"restore"`
	// SafetyBackup é o backup do estado atual, feito antes de a restauração tocar em qualquer coisa.
	SafetyBackup domain.ProjectBackup `json:"safety_backup"`
}

// restorable diz o que da restauração vale hoje: os volumes do backup que o projeto ainda declara
// e o dump, se o projeto tem banco gerenciado.
func (s *Server) restorable(r *http.Request, p domain.Project, b domain.ProjectBackup) (int, error) {
	n := 0
	for _, it := range b.Manifest.Items {
		switch it.Kind {
		case domain.BackupItemVolume:
			for _, path := range p.PersistentPaths {
				if path == it.Path {
					n++
				}
			}
		case domain.BackupItemPostgres:
			if has, err := s.projectHasManagedDB(r, p); err != nil {
				return 0, err
			} else if has {
				n++
			}
		}
	}
	return n, nil
}

func (s *Server) projectHasManagedDB(r *http.Request, p domain.Project) (bool, error) {
	svc, err := s.store.GetService(r.Context(), p.ID, domain.ServicePostgres)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return svc.Mode == domain.ServiceManaged && svc.Status != domain.ServiceDeleting, nil
}

// handleRestoreProjectBackup pede a restauração de um backup do projeto (DEC-47, parte C).
// Exige a senha digitada de novo e o nome do projeto. O worker faz primeiro um backup de
// segurança do estado atual e só então restaura, pausando a aplicação durante a troca.
func (s *Server) handleRestoreProjectBackup(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, id, ok := s.projectBackupRef(w, r, user)
	if !ok {
		return
	}
	var req restoreRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	sess, _, ok := s.sessionOf(r)
	if !ok {
		writeCode(w, http.StatusUnauthorized, "unauthenticated", "sessão ausente ou expirada; entre novamente")
		return
	}
	if !reauthActive(sess) {
		s.audit(r, &user, "project.restore", p.ID.String(), "refused")
		writeCode(w, http.StatusForbidden, codeReauthRequired, "digite a sua senha de novo para restaurar um backup")
		return
	}
	if req.ConfirmSlug != p.Slug {
		s.audit(r, &user, "project.restore", p.ID.String(), "refused")
		writeCode(w, http.StatusBadRequest, codeConfirmationMismatch, "o nome de confirmação não é o do projeto")
		return
	}
	if s.backupDir == "" {
		writeCode(w, http.StatusConflict, codeBackupsOff, "o volume de backups não está disponível nesta instalação")
		return
	}
	b, err := s.store.GetProjectBackupFor(r.Context(), p.ID, id, user.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "backup não encontrado")
			return
		}
		s.log.Error("buscar backup do projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if n, err := s.restorable(r, p, b); err != nil {
		s.log.Error("conferir o que se restaura", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	} else if n == 0 {
		writeCode(w, http.StatusConflict, codeNothingRestore, "este backup não tem nada que o projeto possa receber hoje: os caminhos dele não estão mais declarados e o projeto não tem banco gerenciado")
		return
	}
	rs, safety, err := s.store.CreateRestore(r.Context(), p.ID, user.ID, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "backup não encontrado")
	case errors.Is(err, store.ErrBackupNotUsable):
		writeCode(w, http.StatusConflict, codeNotUsable, "só um backup concluído, que não está sendo apagado, pode ser restaurado")
	case errors.Is(err, store.ErrNothingToBackUp):
		writeCode(w, http.StatusConflict, codeNothingToBackUp, "o projeto não tem dados no momento (nenhum caminho persistente e nenhum banco gerenciado)")
	case errors.Is(err, store.ErrRestoreActive):
		writeCode(w, http.StatusConflict, codeRestoreActive, "já há uma restauração deste projeto em andamento")
	case errors.Is(err, store.ErrBackupActive):
		writeCode(w, http.StatusConflict, codeBackupActive, "já há um backup deste projeto em andamento; espere ele terminar")
	case err != nil:
		s.log.Error("criar restauração", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.auditDetail(r, &user, "project.restore", p.ID.String(), "ok", map[string]any{"names": []string{rs.ID.String(), b.ID.String(), safety.ID.String()}})
		writeJSON(w, http.StatusAccepted, restoreResponse{Restore: rs, SafetyBackup: safety})
	}
}
