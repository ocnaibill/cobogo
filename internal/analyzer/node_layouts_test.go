package analyzer

import (
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func analyzeNode(t *testing.T, files map[string]string, overrides map[string]string) domain.AnalysisResult {
	t.Helper()
	res, err := Analyze(toFS(files), Input{Overrides: overrides})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func hasMessage(findings []domain.Finding, part string) bool {
	for _, f := range findings {
		if strings.Contains(f.Message, part) {
			return true
		}
	}
	return false
}

// DEC-53 B: uma porta escrita no código, sem leitura de PORT, é declaração.
func TestListenPortWrittenInCode(t *testing.T) {
	cases := []struct {
		name     string
		source   string
		wantPort string // "" = continua perguntando
	}{
		{"literal", `const app = require("express")(); app.listen(8000);`, "8000"},
		{"constante no mesmo arquivo", "const PORT = 8000;\napp.listen(PORT, () => console.log('ok'));", "8000"},
		{"let com ponto e vírgula e comentário", "let port = 4321 // porta\nserver.listen(port)", "4321"},
		{"opções", `fastify.listen({ port: 3000, host: "0.0.0.0" })`, "3000"},
		{"serve com opções", `serve({ fetch: app.fetch, port: 3001 })`, "3001"},
		{"leitura de PORT vence (contrato, DEC-35)", "const PORT = process.env.PORT || 5000;\napp.listen(PORT);", "8080"},
		{"constante vinda de outro lugar", "const PORT = config.port;\napp.listen(PORT);", ""},
		{"porta calculada", "app.listen(Number(process.env.X) + 1);", ""},
		{"duas portas diferentes", "a.listen(3000);\nb.listen(4000);", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := analyzeNode(t, nodeRepo(`{"scripts":{"start":"node index.js"}}`, map[string]string{"index.js": c.source}), nil)
			got := res.Fields[domain.FieldPort]
			if c.wantPort == "" {
				if got.Value != "" || !hasMessage(res.Pending, "porta") {
					t.Fatalf("port = %+v pending = %+v, want a question", got, res.Pending)
				}
				return
			}
			if got.Value != c.wantPort {
				t.Fatalf("port = %q (%s), want %q; pending=%+v", got.Value, got.Origin, c.wantPort, res.Pending)
			}
		})
	}
}

func TestListenPortIgnoresTestFiles(t *testing.T) {
	res := analyzeNode(t, nodeRepo(`{"scripts":{"start":"node index.js"}}`, map[string]string{
		"index.js":       `startServer();`,
		"test/server.js": `app.listen(9999);`,
	}), nil)
	if v := res.Fields[domain.FieldPort].Value; v != "" {
		t.Fatalf("a port written only in test code must not be adopted, got %q", v)
	}
}

func TestListenPortIsADeclarationNotAFallback(t *testing.T) {
	res := analyzeNode(t, nodeRepo(`{"scripts":{"start":"node index.js"}}`, map[string]string{"index.js": `app.listen(8000)`}), nil)
	if f := res.Fields[domain.FieldPort]; f.Origin != domain.OriginDeclaration || f.Value != "8000" {
		t.Fatalf("port = %+v, want declaration 8000", f)
	}
	// O usuário sempre pode trocar.
	res = analyzeNode(t, nodeRepo(`{"scripts":{"start":"node index.js"}}`, map[string]string{"index.js": `app.listen(8000)`}), map[string]string{"port": "9000"})
	if f := res.Fields[domain.FieldPort]; f.Value != "9000" || f.Origin != domain.OriginOverride {
		t.Fatalf("override must win: %+v", f)
	}
}

// DEC-53 B: um schema que recebe o ambiente inteiro declara as variáveis dele.
func TestPortReadThroughAnEnvironmentSchema(t *testing.T) {
	src := "import { z } from 'zod';\nconst env = z.object({\n  PORT: z.coerce.number().default(3000),\n  DATABASE_URL: z.string(),\n}).parse(process.env);\nBun.serve({ port: env.PORT });"
	res := analyzeNode(t, nodeRepo(`{"scripts":{"start":"node server.js"}}`, map[string]string{"server.js": src}), nil)
	if f := res.Fields[domain.FieldPort]; f.Value != "8080" || f.Origin != domain.OriginPlatform {
		t.Fatalf("port = %+v, want the platform port by contract", f)
	}
	// Uma chave PORT sem o ambiente inteiro ligado ao schema não prova nada.
	src = "const defaults = {\n  PORT: 3000,\n};\nrun(defaults);"
	res = analyzeNode(t, nodeRepo(`{"scripts":{"start":"node server.js"}}`, map[string]string{"server.js": src}), nil)
	if res.Fields[domain.FieldPort].Value != "" {
		t.Fatalf("a PORT key alone must not be a contract: %+v", res.Fields[domain.FieldPort])
	}
}

// DEC-53 C: nodemon vira opção de `node`, nunca escolha silenciosa.
func TestNodemonStartOffersNodeAsAnOption(t *testing.T) {
	res := analyzeNode(t, nodeRepo(`{"scripts":{"start":"nodemon index.js"}}`, map[string]string{"index.js": `app.listen(process.env.PORT)`}), nil)
	if v := res.Fields[domain.FieldStartCommand].Value; v != "" {
		t.Fatalf("start = %q: the dev tool must not be chosen on the user's behalf", v)
	}
	var pending *domain.Finding
	for i := range res.Pending {
		if res.Pending[i].Field == domain.FieldStartCommand {
			pending = &res.Pending[i]
		}
	}
	if pending == nil {
		t.Fatalf("expected a start question, got %+v", res.Pending)
	}
	if len(pending.Options) == 0 || pending.Options[0].Value != "node index.js" || pending.Options[0].Kind != domain.OptionProduction {
		t.Fatalf("options = %+v, want node index.js first as production", pending.Options)
	}
	// Respondendo, o override vale.
	res = analyzeNode(t, nodeRepo(`{"scripts":{"start":"nodemon index.js"}}`, map[string]string{"index.js": `app.listen(process.env.PORT)`}), map[string]string{"start_command": "node index.js"})
	if res.Fields[domain.FieldStartCommand].Value != "node index.js" {
		t.Fatalf("override ignored: %+v", res.Fields[domain.FieldStartCommand])
	}
}

func TestProductionEquivalent(t *testing.T) {
	for command, want := range map[string]string{
		"nodemon index.js":          "node index.js",
		"nodemon src/server.js":     "node src/server.js",
		"nodemon server":            "node server",
		"nodemon index.ts":          "",
		"nodemon --inspect app.js":  "",
		"nodemon":                   "",
		"nodemon -e js,json app.js": "",
		"nodemon ../x.js":           "",
		"ts-node-dev src/index.ts":  "",
		"node index.js":             "",
	} {
		got, ok := productionEquivalent(command)
		if got != want || ok != (want != "") {
			t.Errorf("productionEquivalent(%q) = %q, %v; want %q", command, got, ok, want)
		}
	}
}

// DEC-53 D: o arquivo que o script manda carregar não veio no repositório.
func TestStartReadsAnEnvFileThatIsNotInTheRepository(t *testing.T) {
	files := nodeRepo(`{"scripts":{"start":"node --env-file=config.env server.js"}}`, map[string]string{
		"server.js":          `app.listen(process.env.PORT)`,
		"config.env.example": "ATLAS_URI=",
	})
	res := analyzeNode(t, files, nil)
	if !hasMessage(res.Warnings, `"config.env"`) || !hasMessage(res.Warnings, "config.env.example") {
		t.Fatalf("expected a warning naming the missing file and its example, got %+v", res.Warnings)
	}
	// Com o arquivo no repositório, não há o que avisar.
	files["config.env"] = "ATLAS_URI=x"
	if res = analyzeNode(t, files, nil); hasMessage(res.Warnings, "config.env") {
		t.Fatalf("unexpected warning when the file exists: %+v", res.Warnings)
	}
}

func TestReferencedEnvFiles(t *testing.T) {
	for command, want := range map[string]string{
		"node --env-file=config.env server.js":      "config.env",
		"node --env-file .env.prod server.js":       ".env.prod",
		"dotenv -e .env.production -- node app.js":  ".env.production",
		"env-cmd -f ./env/prod.json node app.js":    "./env/prod.json",
		"dotenvx run -f .env.local -- node app.js":  ".env.local",
		"node --env-file-if-exists=.env server.js":  "",
		"node -e \"console.log(1)\"":                "",
		"node server.js":                            "",
		"node --env-file=\"x y.env\" a.js":          "\"x",
		"vite build --mode production -f something": "",
	} {
		got := referencedEnvFiles(command)
		switch {
		case want == "" && len(got) == 0:
		case len(got) == 1 && strings.Trim(got[0], `"`) == strings.Trim(want, `"`):
		default:
			t.Errorf("referencedEnvFiles(%q) = %v, want %q", command, got, want)
		}
	}
}

// DEC-53 H: sem lockfile o motor pergunta; com a confirmação instala sem ele, com aviso.
func TestMissingLockfileAsksAndAcceptsConsent(t *testing.T) {
	files := map[string]string{"package.json": `{"scripts":{"start":"node index.js"}}`, "index.js": `app.listen(process.env.PORT)`}

	res := analyzeNode(t, files, nil)
	var ask *domain.Finding
	for i := range res.Pending {
		if res.Pending[i].Field == domain.FieldAllowUnlocked {
			ask = &res.Pending[i]
		}
	}
	if ask == nil || len(ask.Options) == 0 || len(res.Unsupported) > 0 {
		t.Fatalf("expected the allow_unlocked question, got pending=%+v unsupported=%+v", res.Pending, res.Unsupported)
	}

	res = analyzeNode(t, files, map[string]string{"allow_unlocked": "true"})
	if res.Outcome != domain.AnalysisReady || res.Fields[domain.FieldProfile].Value != domain.ProfileNode || res.Fields[domain.FieldPackageManager].Value != domain.ManagerNPM {
		t.Fatalf("outcome = %s fields = %+v pending=%+v", res.Outcome, res.Fields, res.Pending)
	}
	if !hasMessage(res.Warnings, "não tem lockfile") {
		t.Fatalf("the consent must leave a warning, got %+v", res.Warnings)
	}

	// Recusar é uma resposta: continua sem suporte, e a causa fica dita.
	res = analyzeNode(t, files, map[string]string{"allow_unlocked": "false"})
	if res.Outcome != domain.AnalysisUnsupported {
		t.Fatalf("declining must end unsupported, got %s", res.Outcome)
	}

	// Um lockfile existente: nada é perguntado.
	res = analyzeNode(t, nodeRepo(files["package.json"], map[string]string{"index.js": files["index.js"]}), nil)
	for _, p := range res.Pending {
		if p.Field == domain.FieldAllowUnlocked {
			t.Fatalf("a repository with a lockfile must not be asked: %+v", p)
		}
	}
}

func TestUnlockedBunWhenPackageManagerDeclaresIt(t *testing.T) {
	files := map[string]string{"package.json": `{"packageManager":"bun@1.2.0","scripts":{"start":"bun index.ts"}}`, "index.ts": `Bun.serve({ fetch })`}
	res := analyzeNode(t, files, map[string]string{"allow_unlocked": "true"})
	if res.Fields[domain.FieldProfile].Value != domain.ProfileBun {
		t.Fatalf("profile = %q, want P-BUN; %+v %+v", res.Fields[domain.FieldProfile].Value, res.Pending, res.Unsupported)
	}
}

// DEC-53 G: o lockfile de um workspace fica acima da pasta da aplicação.
func TestLockfileOfAWorkspaceIsNamed(t *testing.T) {
	repo := map[string]string{
		"package.json":          `{"workspaces":["apps/*"]}`,
		"bun.lock":              "{}",
		"apps/web/package.json": `{"scripts":{"start":"node server.js"}}`,
		"apps/web/server.js":    `app.listen(process.env.PORT)`,
	}
	res, err := Analyze(toFS(map[string]string{"package.json": repo["apps/web/package.json"], "server.js": repo["apps/web/server.js"]}), Input{Repo: toFS(repo), Dir: "apps/web"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != domain.AnalysisUnsupported || !hasMessage(res.Unsupported, "lockfile de um workspace") || !hasMessage(res.Unsupported, "bun.lock") {
		t.Fatalf("expected the refusal to name the workspace lockfile, got %+v (%s)", res.Unsupported, res.Outcome)
	}
	// Sem a informação de onde a pasta está no repositório, o motor não inventa.
	res, _ = Analyze(toFS(map[string]string{"package.json": repo["apps/web/package.json"], "server.js": repo["apps/web/server.js"]}), Input{})
	if hasMessage(res.Unsupported, "workspace") {
		t.Fatalf("without the repository there is no ancestor to name: %+v", res.Unsupported)
	}
}

// DEC-54 B: ferramenta de build da geração do webpack 4 num Node com OpenSSL 3.
func TestLegacyToolchainAsksForTheOpenSSLWorkaround(t *testing.T) {
	cra := func(version string) map[string]string {
		return nodeRepo(`{"scripts":{"build":"react-scripts build"},"dependencies":{"react":"18.0.0","react-scripts":"`+version+`"}}`, map[string]string{"public/index.html": "<html></html>"})
	}
	ask := func(res domain.AnalysisResult) *domain.Finding {
		for i := range res.Pending {
			if res.Pending[i].Field == domain.FieldBuildLegacyOpenSSL {
				return &res.Pending[i]
			}
		}
		return nil
	}
	for _, v := range []string{"4.0.3", "^4.0.3", "~3.3.0", "3.3.0", "2.1.8"} {
		res := analyzeNode(t, cra(v), nil)
		if ask(res) == nil {
			t.Errorf("react-scripts %s: expected the question; pending=%+v", v, res.Pending)
		}
	}
	for _, v := range []string{"5.0.1", "^5.0.0", "latest", "*"} {
		if res := analyzeNode(t, cra(v), nil); ask(res) != nil {
			t.Errorf("react-scripts %s must not ask", v)
		}
	}
	// A resposta fecha a pergunta; "true" deixa um aviso, "false" não pergunta de novo.
	res := analyzeNode(t, cra("4.0.3"), map[string]string{"build_legacy_openssl": "true"})
	if ask(res) != nil || res.Fields[domain.FieldBuildLegacyOpenSSL].Value != "true" || !hasMessage(res.Warnings, "openssl-legacy-provider") {
		t.Fatalf("confirmed: %+v warnings=%+v", res.Fields[domain.FieldBuildLegacyOpenSSL], res.Warnings)
	}
	if res = analyzeNode(t, cra("4.0.3"), map[string]string{"build_legacy_openssl": "false"}); ask(res) != nil {
		t.Fatal("declining ends the question")
	}
	// Sem comando de build a ferramenta não roda: nada a contornar.
	noBuild := nodeRepo(`{"scripts":{"start":"node server.js"},"dependencies":{"react-scripts":"4.0.3"}}`, map[string]string{"server.js": "app.listen(process.env.PORT)"})
	if res = analyzeNode(t, noBuild, nil); ask(res) != nil {
		t.Fatal("no build script, no question")
	}
	// @vue/cli-service antes da 5 também.
	vue := nodeRepo(`{"scripts":{"build":"vue-cli-service build"},"devDependencies":{"@vue/cli-service":"~4.5.0"}}`, map[string]string{"public/index.html": "<html></html>"})
	if res = analyzeNode(t, vue, nil); ask(res) == nil {
		t.Fatal("@vue/cli-service 4 should ask")
	}
}

// Um projeto que já aplica o contorno no próprio script não é perguntado.
func TestLegacyToolchainAlreadyHandledByTheProject(t *testing.T) {
	files := nodeRepo(`{"scripts":{"build":"cross-env NODE_OPTIONS=--openssl-legacy-provider react-scripts build"},"dependencies":{"react-scripts":"4.0.3"}}`, map[string]string{"public/index.html": "<html></html>"})
	for _, p := range analyzeNode(t, files, nil).Pending {
		if p.Field == domain.FieldBuildLegacyOpenSSL {
			t.Fatalf("the project already sets it: %+v", p)
		}
	}
}
