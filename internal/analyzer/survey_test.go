package analyzer

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// A leitura do repositório inteiro (DEC-52): o Django da raiz e o React de frontend/,
// cada um com o que a análise da própria pasta achou.
func TestSurveyProposesEachAppOfTheRepository(t *testing.T) {
	files := djangoRepo(map[string]string{
		"requirements.txt":      "Django==5.1.4\ndjangorestframework==3.16.0\nmysqlclient==2.2.7\n",
		"mysite/settings.py":    strings.Replace(djangoSettings, "django.db.backends.postgresql", "django.db.backends.mysql", 1),
		"frontend/package.json": `{"scripts":{"build":"vite build"},"dependencies":{"react":"19.0.0"},"devDependencies":{"vite":"6.0.0"}}`,
		"frontend/index.html":   "<div id=root></div>",
	})
	root := toFS(files)
	apps, err := Survey(root, func(dir string) (fs.FS, error) { return fs.Sub(root, dir) })
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 {
		t.Fatalf("apps = %+v", apps)
	}
	back, front := apps[0], apps[1]
	if back.RootDirectory != "" || back.Role != domain.CompBackend || back.Label != "Django REST" {
		t.Errorf("raiz = %+v", back)
	}
	if !hasComponent(back.Components, domain.CompDatabase, "MySQL") {
		t.Errorf("o backend pede MySQL: %+v", back.Components)
	}
	if front.RootDirectory != "frontend" || front.Role != domain.CompFrontend || len(front.Evidence) == 0 {
		t.Errorf("frontend = %+v", front)
	}
	for _, c := range front.Components {
		if c.Role == domain.CompDatabase {
			t.Errorf("o banco é do backend, não do frontend: %+v", front.Components)
		}
	}
}

// Raiz sem aplicação (só pastas): o projeto tem só as irmãs.
func TestSurveyWithAnEmptyRoot(t *testing.T) {
	root := toFS(map[string]string{
		"README.md":        "# monorepo",
		"web/package.json": `{"scripts":{"build":"vite build"},"dependencies":{"react":"19.0.0"},"devDependencies":{"vite":"6.0.0"}}`,
		"web/index.html":   "<div id=root></div>",
		"api/package.json": `{"scripts":{"start":"node server.js"},"dependencies":{"express":"5.1.0"}}`,
		"api/server.js":    "require('express')().listen(process.env.PORT)",
	})
	apps, err := Survey(root, func(dir string) (fs.FS, error) { return fs.Sub(root, dir) })
	if err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{}
	for _, a := range apps {
		dirs[a.RootDirectory] = a.Role
	}
	if len(apps) != 2 || dirs["web"] != domain.CompFrontend || dirs["api"] != domain.CompBackend {
		t.Errorf("apps = %+v", apps)
	}
}

func hasComponent(cs []domain.Component, role, label string) bool {
	for _, c := range cs {
		if c.Role == role && c.Label == label {
			return true
		}
	}
	return false
}

// DEC-53 F: a raiz mistura Python e JavaScript e as duas partes são aplicações.
func TestSurveyOfAMixedRootProposesOneAppPerEcosystem(t *testing.T) {
	files := djangoRepo(map[string]string{
		"package.json":      `{"scripts":{"start":"react-scripts start","build":"react-scripts build"},"dependencies":{"react":"18.0.0","react-scripts":"5.0.1"}}`,
		"package-lock.json": lockJSON,
		"src/index.js":      "import React from 'react'",
	})
	root := toFS(files)
	apps, err := Survey(root, func(dir string) (fs.FS, error) { return fs.Sub(root, dir) })
	if err != nil {
		t.Fatal(err)
	}
	byEco := map[string]domain.StackScanApp{}
	for _, a := range apps {
		if a.RootDirectory == "" {
			byEco[a.PinnedEcosystem] = a
		}
	}
	py, js := byEco[domain.EcosystemPython], byEco[domain.EcosystemJavaScript]
	if py.PinnedEcosystem == "" || js.PinnedEcosystem == "" || len(apps) != 2 {
		t.Fatalf("expected one app per ecosystem on the shared root, got %+v", apps)
	}
	if py.Role == js.Role {
		t.Errorf("the two roles must differ (backend/fullstack versus frontend): %q %q", py.Role, js.Role)
	}
	if py.Profile != domain.ProfilePython || js.Profile != domain.ProfileStatic {
		t.Errorf("profiles = %q, %q; want P-PY and P-STATIC", py.Profile, js.Profile)
	}
}

// Um package.json só de ferramentas num projeto Python não vira uma segunda aplicação.
func TestSurveyOfAMixedRootIgnoresToolingOnlyPackageJSON(t *testing.T) {
	files := djangoRepo(map[string]string{"package.json": `{"scripts":{"css":"tailwindcss -o out.css"},"devDependencies":{"tailwindcss":"3.0.0"}}`, "package-lock.json": lockJSON})
	root := toFS(files)
	apps, err := Survey(root, func(dir string) (fs.FS, error) { return fs.Sub(root, dir) })
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range apps {
		if a.PinnedEcosystem == domain.EcosystemJavaScript {
			t.Fatalf("a tooling-only package.json is not an app: %+v", a)
		}
	}
}
