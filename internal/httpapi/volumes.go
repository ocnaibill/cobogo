package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

// Dados persistentes (DEC-41). Quem declara os caminhos é o usuário; quem cria e
// apaga volumes é o worker.

const codeVolumeInUse = "volume_in_use"

type persistentView struct {
	Paths   []string              `json:"paths"`
	Volumes []store.ProjectVolume `json:"volumes"`
	// Suggestions são pistas da última análise para caminhos que ainda não foram
	// declarados (DEC-41). Só sugerem: nada é aplicado sozinho.
	Suggestions []domain.PersistentSuggestion `json:"suggestions"`
}

// pendingSuggestions devolve as sugestões da última análise que o projeto ainda
// não cobre. Sem análise, ou num site estático, não há nenhuma.
func (s *Server) pendingSuggestions(r *http.Request, p domain.Project) []domain.PersistentSuggestion {
	out := []domain.PersistentSuggestion{}
	analysis, err := s.store.LatestAnalysis(r.Context(), p.ID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("buscar análise", "erro", err)
		}
		return out
	}
	if analysis.Result.Fields[domain.FieldProfile].Value == string(domain.ProfileStatic) {
		return out
	}
	for _, sug := range analysis.Result.PersistentSuggestions {
		covered := false
		for _, declared := range p.PersistentPaths {
			if sug.Path != "" && (sug.Path == declared || strings.HasPrefix(sug.Path, declared+"/")) {
				covered = true
			}
		}
		// Uma nota "usa SQLite, onde?" some quando o projeto já declarou algum caminho.
		if sug.Path == "" && len(p.PersistentPaths) > 0 {
			covered = true
		}
		if !covered {
			out = append(out, sug)
		}
	}
	return out
}

func (s *Server) persistentView(w http.ResponseWriter, r *http.Request, p domain.Project, user domain.User) (persistentView, bool) {
	vols, err := s.store.ListVolumesFor(r.Context(), p.ID, user.ID)
	if err != nil {
		s.log.Error("listar volumes", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return persistentView{}, false
	}
	paths := p.PersistentPaths
	if paths == nil {
		paths = []string{}
	}
	return persistentView{Paths: paths, Volumes: vols, Suggestions: s.pendingSuggestions(r, p)}, true
}

func (s *Server) handleGetPersistent(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	if v, ok := s.persistentView(w, r, p, user); ok {
		writeJSON(w, http.StatusOK, v)
	}
}

type putPersistentRequest struct {
	Paths []string `json:"paths"`
}

// handlePutPersistent substitui os caminhos persistentes do projeto. Vale a
// partir do próximo deploy; um caminho tirado da lista deixa o volume dele como
// "volume sem projeto", até alguém apagá-lo.
func (s *Server) handlePutPersistent(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	var req putPersistentRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	if req.Paths == nil {
		writeError(w, http.StatusBadRequest, "paths é obrigatório (use [] para não persistir nada)")
		return
	}
	paths, err := s.store.SetPersistentPaths(r.Context(), p.ID, user.ID, req.Paths)
	switch {
	case errors.Is(err, store.ErrInvalidPaths):
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), store.ErrInvalidPaths.Error()+": "))
		return
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "projeto não encontrado")
		return
	case err != nil:
		s.log.Error("gravar caminhos persistentes", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.auditDetail(r, &user, "project.persistent_paths", p.ID.String(), "ok", map[string]any{"names": paths})
	p.PersistentPaths = paths
	if v, ok := s.persistentView(w, r, p, user); ok {
		writeJSON(w, http.StatusOK, v)
	}
}

// handleListOrphanVolumes lista os volumes sem projeto: de projetos excluídos e
// de caminhos que deixaram de ser persistentes.
func (s *Server) handleListOrphanVolumes(w http.ResponseWriter, r *http.Request, user domain.User) {
	vols, err := s.store.ListOrphanVolumesFor(r.Context(), user.ID)
	if err != nil {
		s.log.Error("listar volumes sem projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"volumes": vols})
}

type deleteVolumeRequest struct {
	ConfirmName string `json:"confirm_name"`
}

// handleDeleteVolume pede ao worker que apague um volume sem projeto. A
// confirmação é o nome do volume, e a ação é irreversível.
func (s *Server) handleDeleteVolume(w http.ResponseWriter, r *http.Request, user domain.User) {
	name := r.PathValue("name")
	var req deleteVolumeRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	op, err := s.store.RequestDeleteVolume(r.Context(), user.ID, name, req.ConfirmName)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "volume não encontrado")
	case errors.Is(err, store.ErrConfirmationMismatch):
		s.audit(r, &user, "volume.delete", name, "refused")
		writeCode(w, http.StatusBadRequest, codeConfirmationMismatch, "o nome de confirmação não é o do volume")
	case errors.Is(err, store.ErrVolumeNotOrphan):
		s.audit(r, &user, "volume.delete", name, "refused")
		writeCode(w, http.StatusConflict, codeVolumeInUse, "o volume ainda é usado por um caminho persistente do projeto; tire o caminho da lista primeiro")
	case err != nil:
		s.log.Error("pedir exclusão de volume", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.audit(r, &user, "volume.delete", name, "ok")
		writeJSON(w, http.StatusAccepted, op)
	}
}
