package httpapi

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ocnaibill/cobogo/internal/backup"
	"github.com/ocnaibill/cobogo/internal/domain"
)

// Backups da plataforma (DEC-47, parte A). A API só lê o volume de backups (montado
// somente leitura) e entrega os arquivos; quem os faz é o serviço backup. São o
// banco interno **com os segredos de todos, cifrados**: só o administrador baixa, e
// só com a senha digitada de novo. Hoje o único usuário é o administrador (o setup
// cria um só); quando houver mais de um, esta rota passa a exigir o papel.

const downloadStall = 60 * time.Second

type backupsView struct {
	// Configured diz se o painel enxerga o volume de backups. Sem ele, o resto vem vazio.
	Configured bool          `json:"configured"`
	Status     backup.Status `json:"status"`
	Backups    []backup.File `json:"backups"`
	// Stale avisa que o último backup bem-sucedido tem mais de 36 horas, ou que não há nenhum.
	Stale   bool   `json:"stale"`
	StaleAt string `json:"stale_after"`
	// TotalBytes soma os arquivos.
	TotalBytes int64 `json:"total_bytes"`
}

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request, user domain.User) {
	view := backupsView{Backups: []backup.File{}, StaleAt: backup.StaleAfter.String()}
	if s.backupDir != "" {
		if _, err := os.Stat(s.backupDir); err == nil {
			view.Configured = true
		}
	}
	if view.Configured {
		files, err := backup.List(s.backupDir)
		if err != nil {
			s.log.Error("listar backups", "erro", err)
			writeError(w, http.StatusInternalServerError, "erro interno")
			return
		}
		view.Backups = files
		for _, f := range files {
			view.TotalBytes += f.Size
		}
		if view.Status, err = backup.ReadStatus(s.backupDir); err != nil {
			s.log.Warn("ler o estado do backup", "erro", err)
		}
	}
	// Sem volume ou sem backup bom: o aviso vale (um painel que não vê o backup não pode dizer que está tudo certo).
	view.Stale = view.Status.Stale(time.Now())
	if view.Configured && len(view.Backups) > 0 && view.Status.LastOKAt == nil {
		// Há arquivos mas o estado sumiu: confia no mais novo.
		newest := view.Backups[0].At
		view.Stale = time.Since(newest) > backup.StaleAfter
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, view)
}

// progressWriter renova o prazo de escrita a cada pedaço: a API tem um
// WriteTimeout de 60 s para conexões lentas, e um backup grande por um enlace
// lento passa disso. O prazo vale por **falta de progresso**, não pelo total.
type progressWriter struct {
	w  io.Writer
	rc *http.ResponseController
}

func (p progressWriter) Write(b []byte) (int, error) {
	_ = p.rc.SetWriteDeadline(time.Now().Add(downloadStall))
	return p.w.Write(b)
}

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request, user domain.User) {
	name := r.PathValue("name")
	sess, _, ok := s.sessionOf(r)
	if !ok {
		writeCode(w, http.StatusUnauthorized, "unauthenticated", "sessão ausente ou expirada; entre novamente")
		return
	}
	if !reauthActive(sess) {
		s.audit(r, &user, "backup.download", name, "refused")
		writeCode(w, http.StatusForbidden, codeReauthRequired, "digite a sua senha de novo para baixar um backup")
		return
	}
	if s.backupDir == "" || !backup.ValidName(name) {
		writeError(w, http.StatusNotFound, "backup não encontrado")
		return
	}
	path := filepath.Join(s.backupDir, name)
	// Só arquivo comum, sem seguir link: o nome já é fechado, e isto fecha o resto.
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "backup não encontrado")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		s.log.Error("abrir backup", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	defer f.Close()
	files, _ := backup.List(s.backupDir)
	for _, bf := range files {
		if bf.Name == name && bf.SHA256 != "" {
			w.Header().Set("X-Content-SHA256", bf.SHA256)
		}
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Cache-Control", "no-store")
	s.audit(r, &user, "backup.download", name, "ok")
	rc := http.NewResponseController(w)
	if _, err := io.Copy(progressWriter{w: w, rc: rc}, f); err != nil {
		s.log.Warn("baixar backup interrompido", "backup", name, "erro", err)
	}
}
