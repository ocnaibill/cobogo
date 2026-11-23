package httpapi

import (
	"net/http"
	"strconv"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Logs de execução (DEC-46). Só leitura: quem captura é o worker, que redige os
// valores secretos antes de gravar.

type runtimeLogsView struct {
	Lines []domain.LogLine `json:"lines"`
	// HasOlder diz se há linhas guardadas antes da primeira devolvida; HasMore, numa
	// leitura por `after`, se há mais linhas depois da última (a próxima página).
	HasOlder bool `json:"has_older"`
	HasMore  bool `json:"has_more"`
	// RetentionHours e RetentionLines são o que a retenção guarda por projeto.
	RetentionHours int `json:"retention_hours"`
	RetentionLines int `json:"retention_lines"`
}

// cursorParam lê um cursor (id de linha) da consulta: vazio vale 0.
func cursorParam(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		writeError(w, http.StatusBadRequest, name+" deve ser o id de uma linha")
		return 0, false
	}
	return n, true
}

func (s *Server) handleRuntimeLogs(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	origin := r.URL.Query().Get("origin")
	switch origin {
	case "", domain.LogOriginApp, domain.LogOriginPostgres, domain.LogOriginMySQL, "database":
	default:
		writeError(w, http.StatusBadRequest, "origin deve ser app, database, postgres ou mysql")
		return
	}
	after, ok := cursorParam(w, r, "after")
	if !ok {
		return
	}
	before, ok := cursorParam(w, r, "before")
	if !ok {
		return
	}
	if after > 0 && before > 0 {
		writeError(w, http.StatusBadRequest, "use after ou before, não os dois")
		return
	}
	limit, ok := intParam(w, r, "limit", domain.LogPageDefault, 1, domain.LogPageMax)
	if !ok {
		return
	}
	page, err := s.store.ListRuntimeLogsFor(r.Context(), p.ID, user.ID, origin, after, before, limit)
	if err != nil {
		s.log.Error("ler logs de execução", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, runtimeLogsView{
		Lines: page.Lines, HasOlder: page.HasOlder, HasMore: page.HasMore,
		RetentionHours: int(domain.LogRetention.Hours()), RetentionLines: domain.LogRetentionMax,
	})
}
