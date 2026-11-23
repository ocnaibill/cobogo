package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/analyzer"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/secrets"
	"github.com/ocnaibill/cobogo/internal/store"
)

const (
	maxEnvValueBytes = 32 << 10
	maxEnvBodyBytes  = maxEnvValueBytes + 4<<10
	generatedBytes   = 32
)

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// envResponse nunca contém o valor: nem o enviado, nem o gerado (RF-10).
// RedeployRequired lembra que um deploy em execução continua com o valor
// antigo até um novo deploy (RF-23).
type envResponse struct {
	domain.EnvVariable
	RedeployRequired bool `json:"redeploy_required"`
}

func newEnvResponse(v domain.EnvVariable) envResponse {
	return envResponse{EnvVariable: v, RedeployRequired: v.Version > 1}
}

// envTarget valida projeto e nome da variável da rota. Devolve ok=false depois
// de já ter respondido o erro.
func (s *Server) envTarget(w http.ResponseWriter, r *http.Request, user domain.User) (projectID uuid.UUID, name string, ok bool) {
	projectID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id de projeto inválido")
		return uuid.Nil, "", false
	}
	if _, err := s.store.GetProjectFor(r.Context(), projectID, user.ID); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "projeto não encontrado")
		return uuid.Nil, "", false
	} else if err != nil {
		s.log.Error("buscar projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return uuid.Nil, "", false
	}

	name = r.PathValue("name")
	if name == "" {
		return projectID, "", true // listagem
	}
	if !envNamePattern.MatchString(name) {
		writeError(w, http.StatusBadRequest, "nome de variável inválido: use letras, números e _, sem começar por número")
		return uuid.Nil, "", false
	}
	if analyzer.ClassifyVariable(name) == domain.ClassPlatform {
		writeError(w, http.StatusBadRequest, name+" é fornecida pela plataforma e não pode ser definida")
		return uuid.Nil, "", false
	}
	return projectID, name, true
}

func (s *Server) handleListEnv(w http.ResponseWriter, r *http.Request, user domain.User) {
	projectID, _, ok := s.envTarget(w, r, user)
	if !ok {
		return
	}
	vars, err := s.store.ListEnvVariables(r.Context(), projectID)
	if err != nil {
		s.log.Error("listar variáveis", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	// Os valores que a plataforma calculou são endereços públicos, não segredos: o
	// painel os mostra com a regra (DEC-52 item 3). Só os que ainda são da plataforma.
	recs, err := s.store.PlatformEnv(r.Context(), projectID)
	if err != nil {
		s.log.Error("ler variáveis da plataforma", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	platform := []store.PlatformRecord{}
	for _, v := range vars {
		if rec, ok := recs[v.Name]; ok && v.Source == domain.EnvSourcePlatform {
			platform = append(platform, rec)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"variables": vars, "platform": platform})
}

type putEnvRequest struct {
	Value *string `json:"value"`
}

// handlePutEnv grava (ou substitui) o valor de uma variável. Não registra o
// corpo da requisição em lugar nenhum.
func (s *Server) handlePutEnv(w http.ResponseWriter, r *http.Request, user domain.User) {
	projectID, name, ok := s.envTarget(w, r, user)
	if !ok {
		return
	}
	var req putEnvRequest
	if !readJSON(w, r, &req, maxEnvBodyBytes) {
		return
	}
	switch {
	case req.Value == nil || *req.Value == "":
		writeError(w, http.StatusBadRequest, "value é obrigatório e não pode ser vazio; para desfazer a variável, remova-a")
		return
	case len(*req.Value) > maxEnvValueBytes:
		writeError(w, http.StatusBadRequest, "value excede o limite de 32 KiB")
		return
	case strings.ContainsRune(*req.Value, 0):
		writeError(w, http.StatusBadRequest, "value não pode conter o caractere nulo")
		return
	}

	if s.refuseLinked(w, r, projectID, name) {
		return
	}
	v, err := s.store.PutEnvVariable(r.Context(), projectID, name, *req.Value, domain.EnvSourceUser)
	if err != nil {
		s.log.Error("gravar variável", "variavel", name, "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.auditDetail(r, &user, "env.set", projectID.String()+"/"+name, "ok", map[string]any{"version": v.Version, "source": string(v.Source)})
	writeJSON(w, http.StatusOK, newEnvResponse(v))
}

func (s *Server) handleDeleteEnv(w http.ResponseWriter, r *http.Request, user domain.User) {
	projectID, name, ok := s.envTarget(w, r, user)
	if !ok {
		return
	}
	if s.refuseLinked(w, r, projectID, name) {
		return
	}
	switch err := s.store.DeleteEnvVariable(r.Context(), projectID, name); {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "variável não encontrada")
	case err != nil:
		s.log.Error("remover variável", "variavel", name, "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.audit(r, &user, "env.delete", projectID.String()+"/"+name, "ok")
		w.WriteHeader(http.StatusNoContent)
	}
}

type generateEnvRequest struct {
	Replace bool `json:"replace"`
}

// handleGenerateEnv gera um valor aleatório para um segredo que o próprio app
// inventa (DEC-24 item 9). É ação explícita do usuário; o valor é guardado e
// reutilizado, e nunca é devolvido. Substituir um valor existente exige
// `replace`, porque invalidaria sessões e dados assinados com o anterior.
func (s *Server) handleGenerateEnv(w http.ResponseWriter, r *http.Request, user domain.User) {
	projectID, name, ok := s.envTarget(w, r, user)
	if !ok {
		return
	}
	if analyzer.ClassifyVariable(name) != domain.ClassGenerable {
		writeError(w, http.StatusBadRequest, "o Cobogó só gera segredos que o próprio app inventa (como JWT_SECRET ou SESSION_SECRET); "+name+" deve ser informada por você")
		return
	}
	var req generateEnvRequest
	if r.ContentLength != 0 && !readJSON(w, r, &req, 1<<10) {
		return
	}

	if s.refuseLinked(w, r, projectID, name) {
		return
	}
	if _, err := s.store.GetEnvVariable(r.Context(), projectID, name); err == nil && !req.Replace {
		writeError(w, http.StatusConflict, name+" já tem valor; gerar outro invalida o que foi assinado com o anterior. Envie {\"replace\": true} para substituir")
		return
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("buscar variável", "variavel", name, "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	value, err := secrets.Generate(generatedBytes)
	if err != nil {
		s.log.Error("gerar segredo", "variavel", name, "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	v, err := s.store.PutEnvVariable(r.Context(), projectID, name, value, domain.EnvSourceGenerated)
	if err != nil {
		s.log.Error("gravar variável gerada", "variavel", name, "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.auditDetail(r, &user, "env.generate", projectID.String()+"/"+name, "ok", map[string]any{"version": v.Version, "source": string(v.Source)})
	writeJSON(w, http.StatusOK, newEnvResponse(v))
}

// refuseLinked recusa mexer numa variável vinculada a um serviço do Cobogó
// (DEC-42 item 5): ela só sai pelo desvínculo (apagar o banco). Devolve true,
// depois de responder, quando recusou.
func (s *Server) refuseLinked(w http.ResponseWriter, r *http.Request, projectID uuid.UUID, name string) bool {
	v, err := s.store.GetEnvVariable(r.Context(), projectID, name)
	if err != nil || v.Source != domain.EnvSourceLinked {
		return false
	}
	writeCode(w, http.StatusConflict, codeVariableLinked, name+" é a conexão do PostgreSQL do Cobogó deste projeto e não se edita aqui; para trocá-la, apague o banco na aba Banco")
	return true
}
