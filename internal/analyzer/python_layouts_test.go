package analyzer

import (
	"maps"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// djangoWithout devolve o repositório Django de teste sem alguns arquivos.
func djangoWithout(names ...string) map[string]string {
	files := djangoRepo(nil)
	for _, n := range names {
		delete(files, n)
	}
	return files
}

// DEC-53 E: dependências do pip fora do requirements.txt da raiz.
func TestRequirementsOutsideTheRoot(t *testing.T) {
	deps := "Django==5.1.4\npsycopg2-binary==2.9.10\nwhitenoise==6.8.2\n"
	cases := []struct {
		name    string
		extra   map[string]string
		want    string // arquivo escolhido; "" = pergunta
		options int
	}{
		{"production ao lado de base e local", map[string]string{
			"requirements/base.txt": deps, "requirements/local.txt": "-r base.txt\npytest==8.0.0\n", "requirements/production.txt": "-r base.txt\ngunicorn==23.0.0\n",
		}, "requirements/production.txt", 0},
		{"um arquivo só", map[string]string{"requirements/all.txt": deps}, "requirements/all.txt", 0},
		{"requirements numa pasta de deploy", map[string]string{"deploy/requirements.txt": deps}, "deploy/requirements.txt", 0},
		{"sufixo na raiz", map[string]string{"requirements-prod.txt": deps, "requirements-dev.txt": "pytest==8.0.0\n"}, "requirements-prod.txt", 0},
		{"base e extras, sem produção: pergunta", map[string]string{"requirements/base.txt": deps, "requirements/extras.txt": "Pillow==10.0.0\n"}, "", 2},
		{"só desenvolvimento: pergunta", map[string]string{"requirements/dev.txt": deps, "requirements/test.txt": "pytest==8.0.0\n"}, "", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := djangoWithout("requirements.txt")
			maps.Copy(files, c.extra)
			res := analyzeNode(t, files, nil)
			got := res.Fields[domain.FieldRequirementsFile].Value
			if c.want != "" {
				if got != c.want || res.Fields[domain.FieldPackageManager].Value != domain.ManagerPip {
					t.Fatalf("requirements_file = %q manager = %q, want %q; pending=%+v unsupported=%+v", got, res.Fields[domain.FieldPackageManager].Value, c.want, res.Pending, res.Unsupported)
				}
				if len(res.Unsupported) > 0 {
					t.Fatalf("unexpected unsupported: %+v", res.Unsupported)
				}
				// As dependências do arquivo escolhido (e dos que ele inclui) valem para as demais regras.
				if len(res.ServiceNeeds) == 0 {
					t.Fatalf("service needs not detected from the chosen requirements: %+v", res)
				}
				return
			}
			var p *domain.Finding
			for i := range res.Pending {
				if res.Pending[i].Field == domain.FieldRequirementsFile {
					p = &res.Pending[i]
				}
			}
			if p == nil || len(p.Options) != c.options {
				t.Fatalf("pending = %+v, want %d options", res.Pending, c.options)
			}
		})
	}
}

func TestRequirementsAnswerChoosesTheFile(t *testing.T) {
	deps := "Django==5.1.4\npsycopg2-binary==2.9.10\n"
	files := djangoWithout("requirements.txt")
	files["requirements/base.txt"] = deps
	files["requirements/extras.txt"] = "Pillow==10.0.0\n"
	res := analyzeNode(t, files, map[string]string{"requirements_file": "requirements/base.txt"})
	if res.Fields[domain.FieldRequirementsFile].Value != "requirements/base.txt" || res.Fields[domain.FieldRequirementsFile].Origin != domain.OriginOverride {
		t.Fatalf("answer ignored: %+v pending=%+v", res.Fields[domain.FieldRequirementsFile], res.Pending)
	}
	if err := CheckOverrides(map[string]string{"requirements_file": "../etc/passwd"}); err == nil {
		t.Fatal("a path that leaves the project must be refused")
	}
	if err := CheckOverrides(map[string]string{"requirements_file": "/abs/requirements.txt"}); err == nil {
		t.Fatal("an absolute path must be refused")
	}
	if err := CheckOverrides(map[string]string{"requirements_file": "requirements/prod.txt"}); err != nil {
		t.Fatal(err)
	}
}

func TestRootRequirementsKeepsTheDefault(t *testing.T) {
	res := analyzeNode(t, djangoRepo(map[string]string{"requirements/other.txt": "x==1\n"}), nil)
	if v := res.Fields[domain.FieldRequirementsFile].Value; v != "" {
		t.Fatalf("a root requirements.txt must stay the default, got %q", v)
	}
}

// DEC-53 E: o manage.py numa subpasta.
func TestManagePyInASubfolder(t *testing.T) {
	files := map[string]string{"requirements.txt": "Django==5.1.4\npsycopg2-binary==2.9.10\n"}
	for name, content := range djangoRepo(nil) {
		if name != "requirements.txt" {
			files["server/"+name] = content
		}
	}
	res := analyzeNode(t, files, nil)
	if res.Fields[domain.FieldDjangoDir].Value != "server" {
		t.Fatalf("django_dir = %+v pending=%+v unsupported=%+v", res.Fields[domain.FieldDjangoDir], res.Pending, res.Unsupported)
	}
	if res.Fields[domain.FieldPythonFramework].Value != domain.FrameworkDjango || res.Fields[domain.FieldAppTarget].Value != "mysite.wsgi:application" {
		t.Fatalf("django contract not read from the subfolder: %+v", res.Fields)
	}
	for _, e := range res.Evidences {
		if e.Kind == kindDjangoModule && !strings.HasPrefix(e.Source, "server/") {
			t.Fatalf("evidence source lost the folder: %q", e.Source)
		}
	}
	// Dois manage.py: não se escolhe sozinho.
	files["other/manage.py"] = djangoManage
	if got := djangoManageDir(toFS(files)); got != "" {
		t.Fatalf("two manage.py must not be guessed, got %q", got)
	}
	// Na raiz, nada muda.
	if got := djangoManageDir(toFS(djangoRepo(nil))); got != "" {
		t.Fatalf("root manage.py: %q", got)
	}
}

// DEC-53 E: uma pasta só com requirements não é uma aplicação irmã.
func TestSiblingNeedsCodeNotOnlyRequirements(t *testing.T) {
	files := map[string]string{
		"backend/manage.py":          djangoManage,
		"backend/requirements.txt":   "Django==5.1.4\n",
		"deploy/requirements.txt":    "Django==5.1.4\ngunicorn==23.0.0\n",
		"deploy/entrypoint.sh":       "#!/bin/sh\n",
		"{{cookiecutter.slug}}/a.py": "x = 1\n",
	}
	found := findSiblings(toFS(files))
	var dirs []string
	for _, c := range found {
		dirs = append(dirs, c.Dir)
	}
	if len(dirs) != 1 || dirs[0] != "backend" {
		t.Fatalf("siblings = %v, want only backend", dirs)
	}
}

// DEC-53 G: o uv.lock de um workspace fica acima da pasta da aplicação.
func TestUVWorkspaceLockIsNamed(t *testing.T) {
	repo := map[string]string{"pyproject.toml": "[tool.uv.workspace]\nmembers=[\"backend\"]\n", "uv.lock": "version = 1\n", "backend/pyproject.toml": "[project]\nname='b'\nversion='0'\ndependencies=['fastapi']\n", "backend/app/main.py": "from fastapi import FastAPI\napp = FastAPI()\n"}
	sub := map[string]string{"pyproject.toml": repo["backend/pyproject.toml"], "app/main.py": repo["backend/app/main.py"]}
	res, err := Analyze(toFS(sub), Input{Repo: toFS(repo), Dir: "backend"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != domain.AnalysisUnsupported || !hasMessage(res.Unsupported, "lockfile de um workspace") || !hasMessage(res.Unsupported, "uv.lock") {
		t.Fatalf("expected the refusal to name the workspace lock, got %+v", res.Unsupported)
	}
}
