package worker

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

// PostgreSQL auxiliar (DEC-42): a pergunta antes do build, a criação no estágio
// de provisionamento e a exclusão.

const databaseReadyTimeout = 3 * time.Minute

// databaseFinding devolve a pendência do destino do banco (HEU-06.6) quando o
// código usa PostgreSQL com evidência forte, o projeto não escolheu e a variável
// de conexão ainda não tem valor. Quem já definiu a variável conectou o banco por
// conta própria: não há pergunta. Nada é criado aqui.
func (w *Worker) databaseFinding(ctx context.Context, project domain.Project, result domain.AnalysisResult) (*domain.Finding, error) {
	var need *domain.ServiceNeed
	for i := range result.ServiceNeeds {
		n := result.ServiceNeeds[i]
		if n.Engine == domain.ServicePostgres && n.Level == domain.NeedRequired {
			need = &n
			break
		}
	}
	if need == nil {
		return nil, nil
	}
	vars, err := w.store.ListEnvVariables(ctx, project.ID)
	if err != nil {
		return nil, err
	}
	for _, v := range vars {
		if v.Name == need.Variable {
			return nil, nil
		}
	}
	svc, err := w.store.GetService(ctx, project.ID, domain.ServicePostgres)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if err == nil && svc.Mode == domain.ServiceNone {
		return nil, nil
	}

	target, how := need.Variable, "a URL de conexão"
	if need.Style == domain.StyleParts {
		target, how = connectionNames(need), "os dados de conexão"
	}
	message := fmt.Sprintf("O código usa PostgreSQL (%s) e a conexão não está definida (%s). Onde está o banco?", firstEvidence(need.Evidence), target)
	if err == nil && svc.Mode == domain.ServiceExternal {
		message = fmt.Sprintf("Você escolheu usar um banco que já tem, mas a conexão não está definida (%s). Informe %s na aba Variáveis, ou escolha outra opção.", target, how)
	}
	return &domain.Finding{
		Rule: "HEU-06.6", Field: domain.FieldDatabase, Message: message,
		Options: []domain.Option{
			{Value: domain.ServiceManaged, Note: "O Cobogó cria um PostgreSQL só deste projeto, com os dados em volume, e liga " + target + " a ele."},
			{Value: domain.ServiceExternal, Note: "Você informa " + how + " de um banco que já tem, como segredo, em " + target + "."},
			{Value: domain.ServiceNone, Note: "O projeto não precisa de banco; o deploy segue sem perguntar de novo."},
		},
	}, nil
}

func firstEvidence(ev []string) string {
	if len(ev) == 0 {
		return "sem arquivo"
	}
	return ev[0]
}

// provisionPostgres garante o PostgreSQL gerenciado do projeto e espera o banco
// ficar saudável, antes de a aplicação subir. Criar de novo é seguro: o que existe
// é reaproveitado. Devolve se criou o contêiner agora.
func (w *Worker) provisionPostgres(ctx context.Context, rt *dockerrt.Runtime, project domain.Project, svc domain.Service) (created bool, err error) {
	fail := func(err error) (bool, error) {
		_ = w.store.SetServiceStatus(context.WithoutCancel(ctx), svc.ID, domain.ServiceFailed, err.Error())
		return false, err
	}
	pass, err := w.databasePassword(ctx, project, svc)
	if err != nil {
		return fail(err)
	}

	net := dockerrt.ProjectNetwork(project.ID.String())
	if err := rt.EnsureNetwork(ctx, net, map[string]string{dockerrt.LabelProject: project.ID.String()}); err != nil {
		return fail(err)
	}
	created, err = rt.EnsureService(ctx, dockerrt.ServiceSpec{
		Engine: svc.Engine, ProjectID: project.ID.String(), Container: svc.ContainerName, Network: net, Volume: svc.VolumeName, Password: pass, TLS: svc.TLS,
	})
	if err != nil {
		return fail(err)
	}
	eng, err := dockerrt.EngineFor(svc.Engine)
	if err != nil {
		return fail(err)
	}
	if err := w.store.UpsertServiceVolume(ctx, svc.VolumeName, project.ID, eng.DataPath, svc.Engine); err != nil {
		return fail(err)
	}
	if err := rt.WaitHealthy(ctx, svc.ContainerName, databaseReadyTimeout); err != nil {
		return fail(err)
	}
	if err := w.store.SetServiceStatus(ctx, svc.ID, domain.ServiceReady, ""); err != nil {
		return false, err
	}
	return created, nil
}

// deleteService apaga o PostgreSQL gerenciado e os dados (DEC-42 item 6). O
// pedido da API já marcou o serviço e tirou a variável vinculada.
func (w *Worker) deleteService(ctx context.Context, op *domain.Operation) (map[string]any, error) {
	projectID, err := opProject(op)
	if err != nil {
		return nil, err
	}
	svc, err := w.store.GetServiceForWorker(ctx, projectID, domain.ServicePostgres)
	if errors.Is(err, store.ErrNotFound) {
		return map[string]any{"already_gone": true}, nil
	}
	if err != nil {
		return nil, err
	}
	if svc.Mode != domain.ServiceManaged || svc.Status != domain.ServiceDeleting {
		return nil, errors.New("o serviço não está marcado para exclusão")
	}
	if err := w.runtime.RemoveService(ctx, projectID.String(), svc.ContainerName, svc.VolumeName); err != nil {
		return nil, err
	}
	if err := w.store.MarkVolumeDeleted(ctx, svc.VolumeName); err != nil {
		return nil, err
	}
	if err := w.store.MarkServiceDeleted(ctx, svc.ID); err != nil {
		return nil, err
	}
	return map[string]any{"container": svc.ContainerName, "volume": svc.VolumeName}, nil
}

// databasePassword lê a senha do banco da variável vinculada: a da senha (estilo
// partes) ou a URL de conexão (estilo URL, e os bancos criados antes da DEC-44).
func (w *Worker) databasePassword(ctx context.Context, project domain.Project, svc domain.Service) (string, error) {
	values, _, err := w.store.RevealEnvValues(ctx, project.ID)
	if err != nil {
		return "", fmt.Errorf("carregar a conexão do banco: %w", err)
	}
	if name := svc.Variables[domain.RolePassword]; name != "" {
		pass := values[name]
		if pass == "" {
			return "", fmt.Errorf("a variável %s do vínculo com o banco não existe ou está vazia; escolha o banco de novo na aba Banco", name)
		}
		return pass, nil
	}
	name := svc.Variables[domain.RoleURL]
	if name == "" {
		name = svc.Variable
	}
	raw, ok := values[name]
	if !ok {
		return "", fmt.Errorf("a variável %s do vínculo com o banco não existe; escolha o banco de novo na aba Banco", name)
	}
	u, perr := url.Parse(raw)
	pass := ""
	if perr == nil && u.User != nil {
		pass, _ = u.User.Password()
	}
	if pass == "" {
		return "", fmt.Errorf("a variável %s não tem a senha do banco; escolha o banco de novo na aba Banco", name)
	}
	return pass, nil
}

// databaseTrust devolve o arquivo da CA do banco e as variáveis que apontam
// para ele (DEC-44 item 5): NODE_EXTRA_CA_CERTS e PGSSLROOTCERT, e a variável de
// CA que o código lê, se há. Uma variável que o usuário definiu não é trocada.
func (w *Worker) databaseTrust(ctx context.Context, rt *dockerrt.Runtime, project domain.Project, svc domain.Service, defined map[string]string) (map[string][]byte, map[string]string, error) {
	ca, err := rt.ReadFile(ctx, project.ID.String(), svc.ContainerName, dockerrt.PostgresTLSDir+"/server.crt")
	if err != nil {
		return nil, nil, fmt.Errorf("ler o certificado do banco: %w", err)
	}
	env := map[string]string{}
	for _, name := range []string{"NODE_EXTRA_CA_CERTS", "PGSSLROOTCERT"} {
		if _, own := defined[name]; !own {
			env[name] = domain.DBCAPath
		}
	}
	if name := svc.Variables[domain.RoleCA]; name != "" {
		env[name] = string(ca)
	}
	return map[string][]byte{domain.DBCAPath: ca}, env, nil
}

// connectionNames lista os nomes de variável de uma necessidade em partes, na
// ordem em que a conexão é lida.
func connectionNames(need *domain.ServiceNeed) string {
	var names []string
	for _, role := range []string{domain.RoleHost, domain.RolePort, domain.RoleUser, domain.RolePassword, domain.RoleDatabase} {
		if n := need.Variables[role]; n != "" {
			names = append(names, n)
		}
	}
	return strings.Join(names, ", ")
}
