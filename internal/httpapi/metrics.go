package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

// Métricas dos contêineres (DEC-43). Só leitura: quem coleta é o coletor.

// handleMetricsOverview devolve o consumo das cargas geridas e o estado do
// coletor, só com leituras recentes.
func (s *Server) handleMetricsOverview(w http.ResponseWriter, r *http.Request, user domain.User) {
	ov, err := s.store.MetricsOverview(r.Context(), time.Now().UTC())
	if err != nil {
		s.log.Error("métricas: visão geral", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, ov)
}

// handleProjectMetrics devolve, por contêiner do projeto, a identidade, o estado,
// a última leitura (com a idade) e a série da janela, mais os marcadores de
// deploy e de reinício e a sonda de rota. Nunca devolve o objeto de inspeção do
// Docker.
func (s *Server) handleProjectMetrics(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "1h"
	}
	m, err := s.store.ProjectMetrics(r.Context(), p.ID, window, time.Now().UTC())
	switch {
	case errors.Is(err, store.ErrBadWindow):
		writeError(w, http.StatusBadRequest, "window deve ser 15m, 1h, 6h, 24h ou 7d")
	case err != nil:
		s.log.Error("métricas do projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, m)
	}
}
