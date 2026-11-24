package store_test

import (
	"errors"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// O projeto é o repositório (DEC-52): aplicações do mesmo repositório do dono entram
// no mesmo projeto, e o projeto lista o frontend antes do backend.
func TestStackGroupsTheAppsOfARepository(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	const repo = "https://github.com/exemplo/fec"

	back, err := st.CreateProject(ctx, own.ID, "fec", repo, "")
	if err != nil {
		t.Fatal(err)
	}
	front, err := st.CreateProject(ctx, own.ID, "fec-frontend", repo, "frontend")
	if err != nil {
		t.Fatal(err)
	}
	if back.StackID == nil || front.StackID == nil || *back.StackID != *front.StackID {
		t.Fatalf("o mesmo repositório deve ser um projeto só: %v %v", back.StackID, front.StackID)
	}
	if err := st.SetProjectRole(ctx, back.ID, domain.CompBackend); err != nil {
		t.Fatal(err)
	}
	if err := st.SetProjectRole(ctx, front.ID, domain.CompFrontend); err != nil {
		t.Fatal(err)
	}
	stack, err := st.GetStackFor(ctx, *back.StackID, own.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stack.Slug != "fec" || len(stack.Apps) != 2 || stack.Apps[0].Slug != "fec-frontend" || stack.Apps[1].Role != domain.CompBackend {
		t.Fatalf("stack = %+v", stack)
	}
	if bySlug, err := st.GetStackBySlugFor(ctx, "fec", own.ID); err != nil || bySlug.ID != stack.ID {
		t.Errorf("pelo slug: %+v %v", bySlug, err)
	}
	list, err := st.ListStacksFor(ctx, own.ID)
	if err != nil || len(list) != 1 || len(list[0].Apps) != 2 {
		t.Errorf("lista = %+v %v", list, err)
	}
	// Outro repositório é outro projeto.
	other, err := st.CreateProject(ctx, own.ID, "caderno", "https://github.com/exemplo/caderno", "")
	if err != nil || *other.StackID == stack.ID {
		t.Errorf("outro repositório: %+v %v", other, err)
	}
}

// Colar o link: o projeto nasce lendo o repositório, a proposta é gravada e as
// aplicações confirmadas entram nele.
func TestCreateStackScanAndApps(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	stack, err := st.CreateStack(ctx, own.ID, "loja", "https://github.com/exemplo/loja")
	if err != nil {
		t.Fatal(err)
	}
	if stack.ScanState != domain.ScanScanning || len(stack.Apps) != 0 {
		t.Fatalf("stack = %+v", stack)
	}
	op, err := st.ClaimNextOperation(ctx, testLease, testAttempts)
	if err != nil || op == nil || op.Kind != domain.OpScanStack || op.Params["stack_id"] != stack.ID.String() {
		t.Fatalf("a leitura vai para a fila: %+v %v", op, err)
	}
	if _, err := st.RequestStackScan(ctx, stack.ID, own.ID); !errors.Is(err, store.ErrScanBusy) {
		t.Errorf("duas leituras ao mesmo tempo: %v", err)
	}
	if _, err := st.CreateStack(ctx, own.ID, "loja", "https://github.com/exemplo/outra"); !errors.Is(err, store.ErrSlugTaken) {
		t.Errorf("nome repetido: %v", err)
	}
	scan := domain.StackScan{CommitSHA: "abc", Apps: []domain.StackScanApp{{RootDirectory: "", Role: domain.CompBackend, Slug: "loja-api"}, {RootDirectory: "web", Role: domain.CompFrontend, Slug: "loja"}}}
	if err := st.SaveStackScan(ctx, stack.ID, scan); err != nil {
		t.Fatal(err)
	}
	apps, err := st.CreateApps(ctx, stack.ID, own.ID, []store.NewApp{{RootDirectory: "", Slug: "loja-api", Role: domain.CompBackend}, {RootDirectory: "web", Slug: "loja", Role: domain.CompFrontend}})
	if err != nil || len(apps) != 2 {
		t.Fatalf("apps = %+v %v", apps, err)
	}
	// A mesma pasta de novo é ignorada; um endereço em uso é recusado sem criar nada.
	again, err := st.CreateApps(ctx, stack.ID, own.ID, []store.NewApp{{RootDirectory: "web", Slug: "loja-web", Role: domain.CompFrontend}})
	if err != nil || len(again) != 0 {
		t.Errorf("pasta repetida: %+v %v", again, err)
	}
	if _, err := st.CreateApps(ctx, stack.ID, own.ID, []store.NewApp{{RootDirectory: "admin", Slug: "loja", Role: domain.CompApp}}); !errors.Is(err, store.ErrSlugTaken) {
		t.Errorf("endereço em uso: %v", err)
	}
	got, err := st.GetStackFor(ctx, stack.ID, own.ID)
	if err != nil || got.ScanState != domain.ScanDone || got.Scan == nil || len(got.Scan.Apps) != 2 || len(got.Apps) != 2 || got.Apps[0].Slug != "loja" {
		t.Fatalf("stack = %+v %v", got, err)
	}
	// Excluir: só some quando não resta aplicação.
	if err := st.MarkStackDeletedIfEmpty(ctx, stack.ID, own.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetStackFor(ctx, stack.ID, own.ID); err != nil {
		t.Errorf("com aplicações, o projeto fica: %v", err)
	}
	for _, a := range got.Apps {
		if _, err := st.DeleteProject(ctx, a.ID, own.ID, a.Slug, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.MarkStackDeletedIfEmpty(ctx, stack.ID, own.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetStackFor(ctx, stack.ID, own.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("sem aplicações, some: %v", err)
	}
	// O nome fica livre.
	if _, err := st.CreateStack(ctx, own.ID, "loja", "https://github.com/exemplo/loja"); err != nil {
		t.Errorf("reusar o nome: %v", err)
	}
}

// DEC-53 F: duas aplicações dividem a pasta quando cada uma fixa um ecossistema, e o
// ecossistema vai em todo deploy dela.
func TestTwoAppsShareAFolderByEcosystem(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	stack, err := st.CreateStack(ctx, own.ID, "loja", "https://github.com/exemplo/loja")
	if err != nil {
		t.Fatal(err)
	}
	apps, err := st.CreateApps(ctx, stack.ID, own.ID, []store.NewApp{
		{RootDirectory: "", Slug: "loja-api", Role: domain.CompBackend, Ecosystem: domain.EcosystemPython},
		{RootDirectory: "", Slug: "loja", Role: domain.CompFrontend, Ecosystem: domain.EcosystemJavaScript},
	})
	if err != nil || len(apps) != 2 {
		t.Fatalf("duas aplicações na mesma pasta: %+v %v", apps, err)
	}
	// A mesma pasta com o mesmo ecossistema de novo é ignorada.
	again, err := st.CreateApps(ctx, stack.ID, own.ID, []store.NewApp{{RootDirectory: "", Slug: "loja-api-2", Role: domain.CompBackend, Ecosystem: domain.EcosystemPython}})
	if err != nil || len(again) != 0 {
		t.Errorf("repetida: %+v %v", again, err)
	}
	for _, a := range apps {
		dep, _, err := st.CreateDeploymentWithJob(ctx, a.ID, "k-"+a.Slug, nil)
		if err != nil {
			t.Fatal(err)
		}
		if dep.Overrides[domain.FieldEcosystem] != a.Ecosystem {
			t.Errorf("%s: o deploy deve carregar o ecossistema %q, tem %v", a.Slug, a.Ecosystem, dep.Overrides)
		}
	}
	// Uma resposta explícita do pedido vale mais.
	dep, _, err := st.CreateDeploymentWithJob(ctx, apps[0].ID, "k-explicit", map[string]string{domain.FieldEcosystem: domain.EcosystemJavaScript, "port": "9000"})
	if err != nil || dep.Overrides[domain.FieldEcosystem] != domain.EcosystemJavaScript || dep.Overrides["port"] != "9000" {
		t.Errorf("override explícito: %+v %v", dep.Overrides, err)
	}
	// Uma aplicação sem ecossistema fixo não ganha override nenhum.
	plain, err := st.CreateApps(ctx, stack.ID, own.ID, []store.NewApp{{RootDirectory: "admin", Slug: "loja-admin", Role: domain.CompApp}})
	if err != nil || len(plain) != 1 {
		t.Fatal(err)
	}
	if dep, _, _ = st.CreateDeploymentWithJob(ctx, plain[0].ID, "k-plain", nil); len(dep.Overrides) != 0 {
		t.Errorf("sem ecossistema fixo, sem override: %v", dep.Overrides)
	}
	got, err := st.GetStackFor(ctx, stack.ID, own.ID)
	if err != nil {
		t.Fatal(err)
	}
	pinned := 0
	for _, a := range got.Apps {
		if a.Ecosystem != "" {
			pinned++
		}
	}
	if pinned != 2 {
		t.Errorf("o ecossistema fixado deve aparecer na listagem: %+v", got.Apps)
	}
}
