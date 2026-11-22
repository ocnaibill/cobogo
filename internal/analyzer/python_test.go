package analyzer

import (
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

const djangoManage = `#!/usr/bin/env python
import os
import sys

def main():
    os.environ.setdefault("DJANGO_SETTINGS_MODULE", "mysite.settings")
    from django.core.management import execute_from_command_line
    execute_from_command_line(sys.argv)

if __name__ == "__main__":
    main()
`

const djangoSettings = `import os
from pathlib import Path

BASE_DIR = Path(__file__).resolve().parent.parent
SECRET_KEY = os.environ["DJANGO_SECRET_KEY"]
DEBUG = os.environ.get("DEBUG") == "1"
ALLOWED_HOSTS = os.environ.get("ALLOWED_HOSTS", "").split(",")
MIDDLEWARE = [
    "django.middleware.security.SecurityMiddleware",
    "whitenoise.middleware.WhiteNoiseMiddleware",
]
WSGI_APPLICATION = "mysite.wsgi.application"
DATABASES = {"default": {"ENGINE": "django.db.backends.postgresql", "HOST": os.environ.get("DB_HOST")}}
STATIC_URL = "static/"
STATIC_ROOT = BASE_DIR / "staticfiles"
MEDIA_ROOT = BASE_DIR / "media"
`

func djangoRepo(extra map[string]string) map[string]string {
	files := map[string]string{
		"requirements.txt":   "Django==5.1.4\npsycopg2-binary==2.9.10\nwhitenoise==6.8.2\n",
		"manage.py":          djangoManage,
		"mysite/__init__.py": "",
		"mysite/settings.py": djangoSettings,
		"mysite/wsgi.py":     "import os\nfrom django.core.wsgi import get_wsgi_application\napplication = get_wsgi_application()\n",
		"mysite/urls.py":     "urlpatterns = []\n",
	}
	for k, v := range extra {
		if v == "" && strings.HasSuffix(k, "!") {
			delete(files, strings.TrimSuffix(k, "!"))
			continue
		}
		files[k] = v
	}
	return files
}

type pyCase struct {
	name        string
	files       map[string]string
	overrides   map[string]string
	outcome     domain.AnalysisOutcome
	fields      map[string]string // campo → valor esperado
	pending     []string          // campos com pergunta
	unsupported string            // trecho da mensagem
	warnings    []string          // trechos que algum aviso tem
	noWarnings  []string          // trechos que nenhum aviso pode ter
	catalog     string
}

func TestPythonAnalysis(t *testing.T) {
	flask := map[string]string{
		"requirements.txt": "flask==3.1.0\n",
		"app.py":           "from flask import Flask\n\napp = Flask(__name__)\n\n@app.get('/')\ndef index():\n    return 'ok'\n",
	}
	cases := []pyCase{
		{
			name: "flask com requirements", files: flask, outcome: domain.AnalysisReady, catalog: pythonCatalogVersion,
			fields: map[string]string{
				domain.FieldProfile: domain.ProfilePython, domain.FieldPackageManager: "pip", domain.FieldPythonVersion: "3.13",
				domain.FieldAppTarget: "app:app", domain.FieldPythonInterface: "wsgi", domain.FieldPythonFramework: "flask",
				domain.FieldPort: domain.PlatformPort, domain.FieldExecutionMode: "production",
			},
			noWarnings: []string{"sem versão fixada"},
		},
		{
			name: "fastapi com uv e src", outcome: domain.AnalysisReady,
			files: map[string]string{
				"pyproject.toml":      "[project]\nname = \"api\"\nrequires-python = \">=3.11,<3.13\"\ndependencies = [\"fastapi>=0.115\", \"uvicorn\"]\n",
				"uv.lock":             "version = 1\n",
				"src/api/__init__.py": "",
				"src/api/main.py":     "from fastapi import FastAPI\n\napp = FastAPI()\n",
				"tests/test_main.py":  "from fastapi import FastAPI\napp = FastAPI()\n",
			},
			fields: map[string]string{
				domain.FieldPackageManager: "uv", domain.FieldPythonVersion: "3.12", domain.FieldAppTarget: "api.main:app",
				domain.FieldPythonInterface: "asgi", domain.FieldPythonFramework: "fastapi",
			},
		},
		{
			name: "django completo", files: djangoRepo(nil), outcome: domain.AnalysisReady,
			fields: map[string]string{
				domain.FieldPythonFramework: "django", domain.FieldAppTarget: "mysite.wsgi:application", domain.FieldPythonInterface: "wsgi",
				domain.FieldDjangoSettings: "mysite.settings", domain.FieldBuildCommand: "python manage.py collectstatic --noinput",
			},
			warnings:   []string{"CSRF_TRUSTED_ORIGINS", "ALLOWED_HOSTS vem da variável ALLOWED_HOSTS", "manage.py migrate"},
			noWarnings: []string{"DEBUG = True", "SECRET_KEY está escrita", "whitenoise", "STATIC_ROOT"},
		},
		{
			name: "django de tutorial", outcome: domain.AnalysisReady,
			files: djangoRepo(map[string]string{
				"requirements.txt":   "django\n",
				"mysite/settings.py": "SECRET_KEY = 'django-insecure-abc'\nDEBUG = True\nALLOWED_HOSTS = []\nWSGI_APPLICATION = 'mysite.wsgi.application'\nDATABASES = {'default': {'ENGINE': 'django.db.backends.sqlite3', 'NAME': BASE_DIR / 'db.sqlite3'}}\n",
			}),
			fields:   map[string]string{domain.FieldBuildCommand: ""},
			warnings: []string{"DEBUG = True", "SECRET_KEY está escrita", "ALLOWED_HOSTS está fixo", "STATIC_ROOT", "SQLite", "sem versão fixada (django)"},
		},
		{
			name: "django com settings de dev e de produção", outcome: domain.AnalysisNeedsInput,
			files: djangoRepo(map[string]string{
				"manage.py":                     strings.Replace(djangoManage, "mysite.settings", "mysite.settings.dev", 1),
				"mysite/settings.py!":           "",
				"mysite/settings/__init__.py":   "",
				"mysite/settings/base.py":       djangoSettings,
				"mysite/settings/dev.py":        "from .base import *\nDEBUG = True\n",
				"mysite/settings/production.py": "from .base import *\nCSRF_TRUSTED_ORIGINS = ['https://x.example']\n",
			}),
			pending:    []string{domain.FieldDjangoSettings},
			noWarnings: []string{"DEBUG = True", "CSRF_TRUSTED_ORIGINS"},
		},
		{
			name: "django: settings de produção escolhido", outcome: domain.AnalysisReady,
			overrides: map[string]string{domain.FieldDjangoSettings: "mysite.settings.production"},
			files: djangoRepo(map[string]string{
				"manage.py":                     strings.Replace(djangoManage, "mysite.settings", "mysite.settings.dev", 1),
				"mysite/settings.py!":           "",
				"mysite/settings/__init__.py":   "",
				"mysite/settings/base.py":       djangoSettings,
				"mysite/settings/dev.py":        "from .base import *\n",
				"mysite/settings/production.py": "from .base import *\n",
			}),
			fields: map[string]string{domain.FieldDjangoSettings: "mysite.settings.production"},
		},
		{
			name: "react e django na mesma pasta", outcome: domain.AnalysisNeedsInput, catalog: dispatchVersion,
			files:   djangoRepo(map[string]string{"package.json": `{"scripts":{"build":"vite build"}}`}),
			pending: []string{domain.FieldEcosystem},
		},
		{
			name: "react e django: escolheu python", outcome: domain.AnalysisReady, catalog: pythonCatalogVersion,
			overrides: map[string]string{domain.FieldEcosystem: "python"},
			files:     djangoRepo(map[string]string{"package.json": `{"scripts":{"build":"vite build"}}`}),
			fields:    map[string]string{domain.FieldPythonFramework: "django"},
		},
		{
			name: "monorepo com backend e frontend em pastas", outcome: domain.AnalysisUnsupported, catalog: dispatchVersion,
			files: map[string]string{
				"README.md":                "x",
				"backend/requirements.txt": "django==5.1\n",
				"backend/manage.py":        djangoManage,
				"frontend/package.json":    `{"scripts":{"build":"vite build"}}`,
			},
			unsupported: "backend/ (Python), frontend/ (JavaScript)",
		},
		{
			name: "poetry fica para a fase 2", outcome: domain.AnalysisUnsupported,
			files:       map[string]string{"pyproject.toml": "[tool.poetry]\nname='x'\n", "poetry.lock": "", "app.py": flask["app.py"]},
			unsupported: "fase 2",
		},
		{
			name: "duas aplicações", outcome: domain.AnalysisNeedsInput,
			files: map[string]string{
				"requirements.txt": "flask==3.1.0\nfastapi==0.115.0\n",
				"web.py":           flask["app.py"],
				"api/__init__.py":  "",
				"api/main.py":      "from fastapi import FastAPI\napp = FastAPI()\n",
			},
			pending: []string{domain.FieldAppTarget},
		},
		{
			name: "um script sem aplicação web", outcome: domain.AnalysisNeedsInput,
			files:   map[string]string{"requirements.txt": "requests==2.32.0\n", "main.py": "import requests\nprint(requests.get('https://x').text)\n"},
			pending: []string{domain.FieldAppTarget},
		},
		{
			name: "procfile só com worker", outcome: domain.AnalysisUnsupported,
			files:       map[string]string{"requirements.txt": "discord.py==2.4.0\n", "Procfile": "worker: python bot.py\n", "bot.py": "import discord\n"},
			unsupported: "workers e bots",
		},
		{
			name: "procfile web com gunicorn", outcome: domain.AnalysisReady,
			files: map[string]string{
				"requirements.txt": "flask==3.1.0\ngunicorn==23.0.0\n",
				"Procfile":         "web: gunicorn app:app --bind 0.0.0.0:$PORT\n",
				"app.py":           flask["app.py"],
			},
			fields: map[string]string{domain.FieldStartCommand: "gunicorn app:app --bind 0.0.0.0:$PORT", domain.FieldPort: domain.PlatformPort, domain.FieldAppTarget: "app:app"},
		},
		{
			name: "procfile com servidor de desenvolvimento", outcome: domain.AnalysisReady,
			files: map[string]string{
				"requirements.txt": "fastapi==0.115.0\nuvicorn==0.32.0\n",
				"Procfile":         "web: uvicorn main:app --reload --port 5000\n",
				"main.py":          "from fastapi import FastAPI\napp = FastAPI()\n",
			},
			fields:   map[string]string{domain.FieldStartCommand: "", domain.FieldPort: domain.PlatformPort, domain.FieldAppTarget: "main:app", domain.FieldPythonInterface: "asgi"},
			warnings: []string{"servidor de desenvolvimento"},
		},
		{
			name: "procfile com gunicorn sem bind usa a PORT", outcome: domain.AnalysisReady,
			files:  map[string]string{"requirements.txt": "flask==3.1.0\ngunicorn==23.0.0\n", "Procfile": "web: gunicorn app:app\n", "app.py": flask["app.py"]},
			fields: map[string]string{domain.FieldPort: domain.PlatformPort},
		},
		{
			name: "procfile com uvicorn sem host usa as variáveis do uvicorn", outcome: domain.AnalysisReady,
			files:  map[string]string{"requirements.txt": "fastapi==0.115.0\nuvicorn==0.32.0\n", "Procfile": "web: uvicorn main:app\n", "main.py": "from fastapi import FastAPI\napp = FastAPI()\n"},
			fields: map[string]string{domain.FieldPort: domain.PlatformPort},
		},
		{
			name: "procfile com script que não diz a porta", outcome: domain.AnalysisNeedsInput,
			files:    map[string]string{"requirements.txt": "flask==3.1.0\n", "Procfile": "web: python app.py\nworker: python jobs.py\n", "app.py": flask["app.py"]},
			pending:  []string{domain.FieldPort},
			warnings: []string{`o processo "worker" do Procfile é ignorado`},
		},
		{
			name: "procfile com script que lê PORT", outcome: domain.AnalysisReady,
			files:  map[string]string{"requirements.txt": "flask==3.1.0\n", "Procfile": "web: python app.py\n", "app.py": flask["app.py"] + "\nimport os\napp.run(host='0.0.0.0', port=int(os.environ.get('PORT', 5000)))\n"},
			fields: map[string]string{domain.FieldPort: domain.PlatformPort, domain.FieldStartCommand: "python app.py"},
		},
		{
			name: "python fora do catálogo", outcome: domain.AnalysisReady,
			files:    map[string]string{"requirements.txt": "flask==3.1.0\n", ".python-version": "3.9.18\n", "app.py": flask["app.py"]},
			fields:   map[string]string{domain.FieldPythonVersion: "3.10"},
			warnings: []string{"fora do catálogo", "saiu de suporte"},
		},
		{
			name: "runtime.txt do heroku", outcome: domain.AnalysisReady,
			files:  map[string]string{"requirements.txt": "flask==3.1.0\n", "runtime.txt": "python-3.11.9\n", "app.py": flask["app.py"]},
			fields: map[string]string{domain.FieldPythonVersion: "3.11"},
		},
		{
			name: "pip e uv juntos", outcome: domain.AnalysisNeedsInput,
			files:   map[string]string{"requirements.txt": "flask==3.1.0\n", "uv.lock": "", "pyproject.toml": "[project]\ndependencies=['flask']\n", "app.py": flask["app.py"]},
			pending: []string{domain.FieldPackageManager},
		},
		{
			name: "índice privado com credencial", outcome: domain.AnalysisUnsupported,
			files:       map[string]string{"requirements.txt": "--extra-index-url https://user:${TOKEN}@pkgs.example.com/simple\nflask==3.1.0\n", "app.py": flask["app.py"]},
			unsupported: "credencial",
		},
		{
			name: "fábrica do flask", outcome: domain.AnalysisReady,
			files: map[string]string{
				"requirements.txt":  "flask==3.1.0\n",
				"myapp/__init__.py": "from flask import Flask\n\ndef create_app():\n    app = Flask(__name__)\n    return app\n",
				"myapp/views.py":    "x = 1\n",
			},
			fields: map[string]string{domain.FieldAppTarget: "myapp:create_app()", domain.FieldPythonFramework: "flask"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Analyze(toFS(tc.files), Input{Overrides: tc.overrides})
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			dump := func() string {
				var b strings.Builder
				for k, f := range res.Fields {
					b.WriteString("  field " + k + "=" + f.Value + " (" + string(f.Origin) + ")\n")
				}
				for _, f := range res.Pending {
					b.WriteString("  pending " + f.Field + ": " + f.Message + "\n")
				}
				for _, f := range res.Unsupported {
					b.WriteString("  unsupported: " + f.Message + "\n")
				}
				for _, f := range res.Warnings {
					b.WriteString("  warning: " + f.Message + "\n")
				}
				return b.String()
			}
			if res.Outcome != tc.outcome {
				t.Fatalf("outcome = %s, want %s\n%s", res.Outcome, tc.outcome, dump())
			}
			if tc.catalog != "" && res.CatalogVersion != tc.catalog {
				t.Errorf("catalog = %s, want %s", res.CatalogVersion, tc.catalog)
			}
			for field, want := range tc.fields {
				if got := res.Fields[field].Value; got != want {
					t.Errorf("field %s = %q, want %q\n%s", field, got, want, dump())
				}
			}
			var pending []string
			for _, f := range res.Pending {
				pending = append(pending, f.Field)
			}
			if strings.Join(pending, ",") != strings.Join(tc.pending, ",") {
				t.Errorf("pending = %v, want %v\n%s", pending, tc.pending, dump())
			}
			if tc.unsupported != "" && (len(res.Unsupported) == 0 || !strings.Contains(res.Unsupported[0].Message, tc.unsupported)) {
				t.Errorf("unsupported sem %q\n%s", tc.unsupported, dump())
			}
			hasWarning := func(s string) bool {
				for _, w := range res.Warnings {
					if strings.Contains(w.Message, s) {
						return true
					}
				}
				return false
			}
			for _, w := range tc.warnings {
				if !hasWarning(w) {
					t.Errorf("falta o aviso %q\n%s", w, dump())
				}
			}
			for _, w := range tc.noWarnings {
				if hasWarning(w) {
					t.Errorf("aviso indevido %q\n%s", w, dump())
				}
			}
		})
	}
}

func TestPythonServicesAndPersistence(t *testing.T) {
	res, err := Analyze(toFS(djangoRepo(map[string]string{"mysite/settings.py": strings.Replace(djangoSettings, `os.environ.get("DB_HOST")`, `os.environ.get("DB_HOST"), "PASSWORD": os.environ.get("DB_PASSWORD")`, 1)})), Input{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ServiceNeeds) != 1 || res.ServiceNeeds[0].Level != domain.NeedRequired || res.ServiceNeeds[0].Style != domain.StyleParts || res.ServiceNeeds[0].Family != "DB" {
		t.Errorf("service needs = %+v", res.ServiceNeeds)
	}
	found := false
	for _, s := range res.PersistentSuggestions {
		if s.Path == "/app/media" {
			found = true
		}
	}
	if !found {
		t.Errorf("sem sugestão de /app/media: %+v", res.PersistentSuggestions)
	}

	res, err = Analyze(toFS(map[string]string{
		"requirements.txt": "flask==3.1.0\nsqlalchemy==2.0.0\n",
		"app.py":           "from flask import Flask\nfrom sqlalchemy import create_engine\napp = Flask(__name__)\nengine = create_engine('sqlite:///data/app.db')\n",
	}), Input{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PersistentSuggestions) == 0 || res.PersistentSuggestions[0].Path != "/app/data" {
		t.Errorf("persistence = %+v", res.PersistentSuggestions)
	}
	if len(res.ServiceNeeds) != 0 {
		t.Errorf("service needs = %+v", res.ServiceNeeds)
	}
}

func TestPythonVersionSpec(t *testing.T) {
	for _, tc := range []struct {
		v, spec string
		ok      bool
	}{
		{"3.13", ">=3.10", true}, {"3.13", ">=3.10,<3.13", false}, {"3.12", ">=3.10,<3.13", true},
		{"3.13", "~=3.11", true}, {"3.10", "~=3.11", false}, {"3.12", "==3.12.*", true}, {"3.13", "==3.12.*", false},
		{"3.13", ">3.12", true}, {"3.12", ">3.12", false}, {"3.13", "<=3.12", false}, {"3.12", "<=3.12", true},
		{"3.13", "!=3.13.*", false}, {"3.13", "", true},
	} {
		if got := satisfies(tc.v, tc.spec); got != tc.ok {
			t.Errorf("satisfies(%s, %q) = %v, want %v", tc.v, tc.spec, got, tc.ok)
		}
	}
}

func TestCheckOverridesKnowsPythonFields(t *testing.T) {
	if err := CheckOverrides(map[string]string{domain.FieldPythonVersion: "3.9"}); err == nil {
		t.Error("3.9 deveria ser recusada")
	}
	if err := CheckOverrides(map[string]string{domain.FieldPythonVersion: "3.12", domain.FieldAppTarget: "app.main:app"}); err != nil {
		t.Errorf("override válido recusado: %v", err)
	}
	if err := CheckOverrides(map[string]string{domain.FieldAppTarget: "app main"}); err == nil {
		t.Error("alvo inválido aceito")
	}
}
