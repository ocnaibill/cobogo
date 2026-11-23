package worker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/analyzer"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/gitsource"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/validate"
)

// scanStack lê o repositório de um projeto (DEC-52 item 2) e grava o que o Cobogó
// propõe criar: as aplicações, cada uma com a pasta, o papel, o slug e o que ela pede.
// Nada é criado aqui. Uma falha de leitura (repositório privado, link errado) fica no
// projeto, com o motivo, e não volta à fila: a pessoa pede de novo.
func (w *Worker) scanStack(ctx context.Context, op *domain.Operation) (map[string]any, error) {
	raw, _ := op.Params["stack_id"].(string)
	stackID, err := uuid.Parse(raw)
	if err != nil {
		return nil, errors.New("operação sem projeto")
	}
	st, err := w.store.GetStack(ctx, stackID)
	if errors.Is(err, store.ErrNotFound) {
		return map[string]any{"already_gone": true}, nil
	}
	if err != nil {
		return nil, err
	}
	if st.DeletedAt != nil {
		return map[string]any{"already_gone": true}, nil
	}
	scan, err := w.surveyStack(ctx, st)
	if err != nil {
		if ferr := w.store.FailStackScan(context.WithoutCancel(ctx), stackID, err.Error()); ferr != nil {
			return nil, ferr
		}
		return map[string]any{"failed": err.Error()}, nil
	}
	if err := w.store.SaveStackScan(ctx, stackID, scan); err != nil {
		return nil, err
	}
	return map[string]any{"apps": len(scan.Apps), "commit_sha": scan.CommitSHA}, nil
}

func (w *Worker) surveyStack(ctx context.Context, st domain.Stack) (domain.StackScan, error) {
	gitHosts, err := w.settings.GitHosts(ctx)
	if err != nil {
		return domain.StackScan{}, err
	}
	repositoryURL, err := validate.RepositoryURL(st.RepositoryURL, gitHosts)
	if err != nil {
		return domain.StackScan{}, fmt.Errorf("repositório não permitido: %w", err)
	}
	ws, err := gitsource.Fetch(ctx, repositoryURL, gitsource.Options{})
	if err != nil {
		return domain.StackScan{}, err
	}
	defer ws.Cleanup()
	rootDir, err := gitsource.ResolveRoot(ws.Dir, "")
	if err != nil {
		return domain.StackScan{}, err
	}
	// Cada pasta passa pela mesma conferência do deploy: não sai do clone (DEC-27 item 12).
	open := func(dir string) (fs.FS, error) {
		d, err := gitsource.ResolveRoot(ws.Dir, dir)
		if err != nil {
			return nil, err
		}
		return os.DirFS(d), nil
	}
	apps, err := analyzer.Survey(os.DirFS(rootDir), open)
	if err != nil {
		return domain.StackScan{}, err
	}

	// A pasta que já é aplicação do projeto mantém o slug dela; o resto não colide
	// com nenhuma aplicação viva.
	existing, err := w.store.StackApps(ctx, st.ID)
	if err != nil {
		return domain.StackScan{}, err
	}
	own := map[string]string{}
	for _, a := range existing {
		own[a.RootDirectory] = a.Slug
	}
	ownSlugs := map[string]bool{}
	for _, s := range own {
		ownSlugs[s] = true
	}
	var checkErr error
	taken := func(slug string) bool {
		if ownSlugs[slug] || validate.ReservedSlug(slug) {
			return true
		}
		inUse, err := w.store.ProjectSlugInUse(ctx, slug)
		if err != nil && checkErr == nil {
			checkErr = err
		}
		return inUse
	}
	var fresh []domain.StackScanApp
	var idx []int
	for i, a := range apps {
		if _, ok := own[a.RootDirectory]; !ok {
			fresh = append(fresh, a)
			idx = append(idx, i)
		}
	}
	if len(own) == 0 {
		domain.ProposeAppSlugs(st.Slug, fresh, taken)
	} else {
		domain.ProposeAddedAppSlugs(st.Slug, fresh, taken)
	}
	if checkErr != nil {
		return domain.StackScan{}, checkErr
	}
	for j, i := range idx {
		apps[i].Slug = fresh[j].Slug
	}
	for i := range apps {
		if s, ok := own[apps[i].RootDirectory]; ok {
			apps[i].Slug = s
		}
	}
	return domain.StackScan{CommitSHA: ws.CommitSHA, Apps: apps}, nil
}

// appRole é o papel da aplicação da própria pasta no mapa da análise (DEC-49), com o
// site estático como frontend.
func appRole(res domain.AnalysisResult) string {
	if res.Fields[domain.FieldProfile].Value == domain.ProfileStatic {
		return domain.CompFrontend
	}
	for _, c := range res.Components {
		if c.Dir == "." && !c.Sibling {
			switch c.Role {
			case domain.CompFrontend, domain.CompBackend, domain.CompFullstack, domain.CompApp:
				return c.Role
			}
		}
	}
	return ""
}
