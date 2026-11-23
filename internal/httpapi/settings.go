package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/settings"
	"github.com/ocnaibill/cobogo/internal/store"
)

type settingsList struct {
	Settings []settings.Item `json:"settings"`
}

// handleListSettings lista a política operacional editável, com o valor
// efetivo e a origem de cada chave (DEC-30 item 8).
func (s *Server) handleListSettings(w http.ResponseWriter, r *http.Request, user domain.User) {
	items, err := s.settings.List(r.Context())
	if err != nil {
		s.log.Error("listar configurações", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, settingsList{Settings: items})
}

// settingItem devolve o item efetivo de uma chave.
func (s *Server) settingItem(r *http.Request, key string) (settings.Item, error) {
	items, err := s.settings.List(r.Context())
	if err != nil {
		return settings.Item{}, err
	}
	for _, it := range items {
		if it.Key == key {
			return it, nil
		}
	}
	return settings.Item{}, errors.New("httpapi: chave de configuração sem item")
}

type putSettingRequest struct {
	Value json.RawMessage `json:"value"`
}

func (s *Server) handlePutSetting(w http.ResponseWriter, r *http.Request, user domain.User) {
	key := r.PathValue("key")
	def, ok := settings.Lookup(key)
	if !ok {
		writeError(w, http.StatusNotFound, "configuração desconhecida")
		return
	}
	var req putSettingRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	if len(req.Value) == 0 || string(req.Value) == "null" {
		writeError(w, http.StatusBadRequest, "value é obrigatório")
		return
	}
	value, err := def.FromJSON(req.Value)
	if err != nil {
		writeError(w, http.StatusBadRequest, key+": "+err.Error())
		return
	}
	if err := s.store.PutSetting(r.Context(), key, value, user.ID); err != nil {
		s.log.Error("gravar configuração", "chave", key, "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.audit(r, &user, "setting.update", key, "ok")
	s.afterRoutingChange(r, user, key)
	s.writeSetting(w, r, key)
}

// handleResetSetting devolve a chave ao ambiente ou ao padrão. É idempotente.
func (s *Server) handleResetSetting(w http.ResponseWriter, r *http.Request, user domain.User) {
	key := r.PathValue("key")
	if _, ok := settings.Lookup(key); !ok {
		writeError(w, http.StatusNotFound, "configuração desconhecida")
		return
	}
	if _, err := s.store.DeleteSetting(r.Context(), key); err != nil {
		s.log.Error("restaurar configuração", "chave", key, "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.audit(r, &user, "setting.reset", key, "ok")
	s.afterRoutingChange(r, user, key)
	s.writeSetting(w, r, key)
}

func (s *Server) writeSetting(w http.ResponseWriter, r *http.Request, key string) {
	item, err := s.settingItem(r, key)
	if err != nil {
		s.log.Error("ler configuração", "chave", key, "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

type limitsView struct {
	Overrides domain.Limits                 `json:"overrides"`
	Effective map[string]settings.Effective `json:"effective"`
}

func (s *Server) writeLimits(w http.ResponseWriter, r *http.Request, p domain.Project) {
	eff, err := s.settings.EffectiveLimits(r.Context(), p.Limits)
	if err != nil {
		s.log.Error("limites efetivos", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, limitsView{Overrides: p.Limits, Effective: eff})
}

func (s *Server) projectOf(w http.ResponseWriter, r *http.Request, user domain.User) (domain.Project, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id de projeto inválido")
		return domain.Project{}, false
	}
	p, err := s.store.GetProjectFor(r.Context(), id, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "projeto não encontrado")
		return domain.Project{}, false
	}
	if err != nil {
		s.log.Error("buscar projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return domain.Project{}, false
	}
	return p, true
}

func (s *Server) handleGetProjectLimits(w http.ResponseWriter, r *http.Request, user domain.User) {
	if p, ok := s.projectOf(w, r, user); ok {
		s.writeLimits(w, r, p)
	}
}

// handlePutProjectLimits substitui as sobreposições do projeto; campo ausente
// herda, `{}` limpa tudo (DEC-30 item 7).
func (s *Server) handlePutProjectLimits(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	var limits domain.Limits
	if !readJSON(w, r, &limits, 0) {
		return
	}
	if err := settings.ValidateLimits(limits); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := s.store.SetProjectLimits(r.Context(), p.ID, user.ID, limits)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "projeto não encontrado")
		return
	}
	if err != nil {
		s.log.Error("gravar limites do projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.audit(r, &user, "project.limits", p.ID.String(), "ok")
	s.writeLimits(w, r, updated)
}
