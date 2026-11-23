package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/analyzer"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/secrets"
	"github.com/ocnaibill/cobogo/internal/settings"
	"github.com/ocnaibill/cobogo/internal/store"
)

// O que a plataforma já sabe vira configuração (DEC-52 parte B): os endereços do
// projeto nas variáveis que o código lê, os segredos que a aplicação inventa e os
// serviços que o código usa com evidência forte. Quem grava é a API, porque só ela
// cifra valores (DEC-33); o worker só os lê ao subir a aplicação.

// appKnowledge é o que se sabe do código de uma aplicação: a última análise ou, antes
// do primeiro deploy, a leitura do repositório.
type appKnowledge struct {
	role     string
	vars     []domain.ConfigVariable
	needs    []domain.ServiceNeed
	apiPaths map[string]string
}

func (s *Server) knowledgeOf(ctx context.Context, app domain.Project, scan *domain.StackScan) (appKnowledge, bool) {
	if a, err := s.store.LatestAnalysis(ctx, app.ID); err == nil {
		return appKnowledge{componentRole(a.Result, "."), a.Result.ConfigVariables, a.Result.ServiceNeeds, analyzer.APIPaths(a.Result)}, true
	}
	if scan != nil {
		for _, x := range scan.Apps {
			if x.RootDirectory == app.RootDirectory {
				return appKnowledge{x.Role, x.Variables, x.ServiceNeeds, x.APIPaths}, true
			}
		}
	}
	return appKnowledge{}, false
}

func trimURL(routing settings.Routing, slug string) string {
	return strings.TrimRight(routing.URL(slug), "/")
}

// desiredPlatformEnv calcula os valores que a plataforma quer nas variáveis que o
// código de app lê, a partir dos endereços das aplicações do projeto. Catálogo
// fechado (DEC-49 item 3): só nomes conhecidos, e um nome que o código não lê não entra.
func desiredPlatformEnv(app domain.Project, apps []domain.Project, k appKnowledge, routing settings.Routing) []store.PlatformValue {
	own := trimURL(routing, app.Slug)
	host := routing.Host(app.Slug)
	var fronts []string
	back, backSlug := "", ""
	for _, a := range apps {
		if a.ID == app.ID {
			continue
		}
		switch a.Role {
		case domain.CompFrontend:
			fronts = append(fronts, trimURL(routing, a.Slug))
		case domain.CompBackend, domain.CompFullstack:
			if back == "" {
				back, backSlug = trimURL(routing, a.Slug), a.Slug
			}
		}
	}
	isBackend := app.Role == domain.CompBackend || app.Role == domain.CompFullstack
	ownNames := suggestableNames(app.Slug)
	out := []store.PlatformValue{}
	seen := map[string]bool{}
	for _, cv := range k.vars {
		name := strings.ToUpper(cv.Name)
		if cv.Class == domain.ClassPlatform || seen[cv.Name] {
			continue
		}
		v := store.PlatformValue{Name: cv.Name, BuildTime: cv.Class == domain.ClassPublic}
		switch {
		case ownHostNames[name]:
			v.Value, v.Reason = host, "o endereço desta aplicação (sem esquema nem porta), que o Django compara com o Host do pedido"
		case ownOriginNames[name]:
			origins := append([]string{own}, fronts...)
			v.Value = strings.Join(origins, ",")
			v.Reason = "a URL desta aplicação"
			if len(fronts) > 0 {
				v.Reason += " e a do frontend do projeto, de onde vêm o login e os formulários"
			}
		case frontendURLNames[name] && !(app.Role == domain.CompFrontend) && len(fronts) > 0:
			if strings.HasPrefix(name, "CORS_") {
				v.Value, v.Reason = strings.Join(fronts, ","), "a URL do frontend do projeto, que chama esta API"
			} else {
				v.Value, v.Reason = fronts[0], "a URL do frontend do projeto"
			}
		case backendURLNames[name] && !isBackend && back != "":
			v.Value, v.Reason = back+k.apiPaths[cv.Name], "a URL do backend do projeto ("+backSlug+")"
			if p := k.apiPaths[cv.Name]; p != "" {
				v.Reason += ", com o caminho " + p + " que o código usa como padrão"
			}
		case ownNames[name]:
			v.Value, v.Reason = own, "o endereço público desta aplicação"
		default:
			continue
		}
		seen[cv.Name] = true
		out = append(out, v)
	}
	return out
}

// autoManagedNeeds são os serviços que o Cobogó cria sem perguntar (DEC-52 item 6): uso
// com evidência forte, o endereço lido de variável (não escrito no código) e uma só
// engine relacional. O resto continua como pergunta no deploy.
func autoManagedNeeds(needs []domain.ServiceNeed) []domain.ServiceNeed {
	var relational []domain.ServiceNeed
	var out []domain.ServiceNeed
	for _, n := range needs {
		if n.Level != domain.NeedRequired || !domain.IsManagedEngine(n.Engine) || len(n.FixedAt) > 0 {
			continue
		}
		if domain.IsRelationalEngine(n.Engine) {
			relational = append(relational, n)
			continue
		}
		out = append(out, n)
	}
	if len(relational) == 1 {
		out = append([]domain.ServiceNeed{relational[0]}, out...)
	}
	return out
}

// prepareReport diz o que a plataforma fez numa aplicação.
type prepareReport struct {
	App       string                `json:"app"`
	Variables []store.PlatformValue `json:"variables"`
	Generated []string              `json:"generated"`
	Services  []string              `json:"services"`
}

// prepareStack aplica o que a plataforma sabe a cada aplicação do projeto: as
// variáveis da plataforma, os segredos geráveis que faltam e os serviços sem pergunta.
// Nada que a pessoa definiu é tocado. Uma falha numa aplicação não impede as outras.
func (s *Server) prepareStack(ctx context.Context, stackID, ownerID uuid.UUID) ([]prepareReport, error) {
	st, err := s.store.GetStackFor(ctx, stackID, ownerID)
	if err != nil {
		return nil, err
	}
	routing, err := s.settings.Apps(ctx)
	if err != nil {
		return nil, err
	}
	// O papel vem da aplicação (gravado pelo worker) ou, antes da primeira análise
	// dela, do que se sabe do código.
	apps := make([]domain.Project, 0, len(st.Apps))
	known := map[uuid.UUID]appKnowledge{}
	for _, a := range st.Apps {
		app := a.Project
		if k, ok := s.knowledgeOf(ctx, app, st.Scan); ok {
			known[app.ID] = k
			if app.Role == "" {
				app.Role = k.role
			}
		}
		apps = append(apps, app)
	}
	var reports []prepareReport
	var errs []error
	for _, app := range apps {
		k, ok := known[app.ID]
		if !ok {
			continue
		}
		rep := prepareReport{App: app.Slug, Variables: []store.PlatformValue{}, Generated: []string{}, Services: []string{}}
		// Serviços primeiro: as variáveis de conexão ficam vinculadas a eles, e o
		// catálogo de endereços nunca as disputa.
		for _, need := range autoManagedNeeds(k.needs) {
			created, err := s.autoManagedService(ctx, app, ownerID, need)
			if err != nil {
				errs = append(errs, err)
			} else if created {
				rep.Services = append(rep.Services, need.Engine)
			}
		}
		for _, cv := range k.vars {
			if cv.Class != domain.ClassGenerable {
				continue
			}
			made, err := s.store.PutGeneratedIfMissing(ctx, app.ID, cv.Name, func() (string, error) { return secrets.Generate(generatedBytes) })
			if err != nil {
				errs = append(errs, err)
			} else if made {
				rep.Generated = append(rep.Generated, cv.Name)
			}
		}
		if routing.Enabled() {
			changed, err := s.store.ApplyPlatformEnv(ctx, app.ID, desiredPlatformEnv(app, apps, k, routing))
			if err != nil {
				errs = append(errs, err)
			}
			rep.Variables = changed
		}
		if len(rep.Variables)+len(rep.Generated)+len(rep.Services) > 0 {
			reports = append(reports, rep)
		}
	}
	return reports, errors.Join(errs...)
}

// autoManagedService cria o serviço gerenciado de uma necessidade, se o projeto ainda não
// escolheu nada para a engine e nenhuma variável de conexão tem valor. Devolve se criou.
func (s *Server) autoManagedService(ctx context.Context, app domain.Project, ownerID uuid.UUID, need domain.ServiceNeed) (bool, error) {
	// Já houve uma escolha (inclusive "não precisa", ou um serviço apagado de propósito).
	if had, err := s.store.EverHadService(ctx, app.ID, need.Engine); err != nil || had {
		return false, err
	}
	vars := need.Variables
	if len(vars) == 0 && need.Variable != "" {
		vars = map[string]string{domain.RoleURL: need.Variable}
	}
	if len(vars) == 0 {
		return false, nil
	}
	defined, err := s.store.ListEnvVariables(ctx, app.ID)
	if err != nil {
		return false, err
	}
	for _, v := range defined {
		for _, n := range vars {
			if v.Name == n {
				return false, nil // a pessoa já conectou o serviço por conta própria
			}
		}
	}
	pass, err := secrets.Generate(databasePasswordLen)
	if err != nil {
		return false, err
	}
	values := connectionValues(need.Engine, vars, domain.ServiceContainerNameFor(app.ID.String(), need.Engine), pass)
	if _, err := s.store.ChooseDatabaseFor(ctx, need.Engine, app.ID, ownerID, domain.ServiceManaged, vars, values); err != nil {
		if errors.Is(err, store.ErrVariableDefined) || errors.Is(err, store.ErrManagedExists) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// prepareAndAudit roda prepareStack e registra o que mudou; uma falha não impede o
// deploy (a pergunta do deploy e as variáveis faltantes continuam valendo).
func (s *Server) prepareAndAudit(ctx context.Context, stackID uuid.UUID, user domain.User, auditFn func(action, target string, detail map[string]any)) []prepareReport {
	reports, err := s.prepareStack(ctx, stackID, user.ID)
	if err != nil {
		s.log.Warn("preparar as aplicações do projeto", "projeto", stackID, "erro", err)
	}
	for _, rep := range reports {
		names := []string{}
		for _, v := range rep.Variables {
			names = append(names, v.Name)
		}
		auditFn("project.platform_env", rep.App, map[string]any{"variables": names, "generated": rep.Generated, "services": rep.Services})
	}
	return reports
}

// prepareFor prepara o projeto no contexto de um pedido HTTP, com a auditoria dele.
func (s *Server) prepareFor(r *http.Request, user domain.User, stackID uuid.UUID) []prepareReport {
	return s.prepareAndAudit(r.Context(), stackID, user, func(action, target string, detail map[string]any) {
		s.auditDetail(r, &user, action, target, "ok", detail)
	})
}

// afterRoutingChange recalcula as variáveis da plataforma de todos os projetos do dono
// quando o domínio ou o esquema das aplicações muda (DEC-52 item 3).
func (s *Server) afterRoutingChange(r *http.Request, user domain.User, key string) {
	if key != settings.AppsDomain && key != settings.AppsScheme {
		return
	}
	stacks, err := s.store.ListStacksFor(r.Context(), user.ID)
	if err != nil {
		s.log.Warn("listar projetos para recalcular as variáveis", "erro", err)
		return
	}
	for _, st := range stacks {
		s.prepareFor(r, user, st.ID)
	}
}
