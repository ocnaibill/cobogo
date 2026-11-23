package analyzer

import (
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func componentSummary(cs []domain.Component) []string {
	var out []string
	for _, c := range cs {
		s := c.Dir + "|" + c.Role + "|" + c.Label
		if c.Sibling {
			s += "|irmã"
		}
		if !c.Supported {
			s += "|fora"
		}
		out = append(out, s)
	}
	return out
}

func TestComponentsMapOfADjangoWithReactSibling(t *testing.T) {
	files := djangoRepo(map[string]string{
		"requirements.txt":      "Django==5.1.4\ndjangorestframework==3.16.0\nmysqlclient==2.2.7\ncelery==5.5.3\nredis==6.2.0\nwhitenoise==6.8.2\n",
		"mysite/settings.py":    strings.Replace(djangoSettings, "django.db.backends.postgresql", "django.db.backends.mysql", 1) + "CELERY_BROKER_URL = 'redis://localhost:6379/0'\n",
		"frontend/package.json": `{"scripts":{"build":"vite build"},"dependencies":{"react":"19.0.0","axios":"1.9.0"},"devDependencies":{"vite":"6.0.0"}}`,
		"docs/package.json":     `{"scripts":{"build":"vite build"},"dependencies":{"react":"19.0.0"}}`,
		"media/leia-me.txt":     "x",
	})
	res, err := Analyze(toFS(files), Input{})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(componentSummary(res.Components), "\n")
	for _, want := range []string{
		".|backend|Django REST",
		".|database|MySQL",
		".|worker|Celery|fora",
		"frontend|frontend|React + Vite|irmã",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("mapa sem %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "docs|") || strings.Contains(got, "media|") {
		t.Errorf("pasta que não é aplicação entrou no mapa:\n%s", got)
	}
	// Com Celery, o Redis é fila, não cache.
	if !strings.Contains(got, ".|queue|Redis") || strings.Contains(got, ".|queue|Redis|fora") {
		t.Errorf("Redis com Celery deveria ser fila:\n%s", got)
	}
	for _, c := range res.Components {
		if len(c.Evidence) == 0 {
			t.Errorf("peça sem evidência: %+v", c)
		}
	}
}

func TestComponentsRoles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"flask", map[string]string{"requirements.txt": "flask==3.1.0\n", "app.py": "from flask import Flask\napp = Flask(__name__)\n"}, ".|backend|Flask"},
		{"django com templates", djangoRepo(nil), ".|fullstack|Django"},
		{"express", nodeRepo(`{"scripts":{"start":"node server.js"},"dependencies":{"express":"4.21.0"}}`, map[string]string{"server.js": "require('express')().listen(process.env.PORT)"}), ".|backend|Express"},
		{"next", nodeRepo(`{"scripts":{"start":"next start","build":"next build"},"dependencies":{"next":"15.0.0","react":"19.0.0"}}`, nil), ".|fullstack|Next.js"},
		{"vite estático", nodeRepo(`{"scripts":{"build":"vite build"},"dependencies":{"react":"19.0.0"},"devDependencies":{"vite":"6.0.0"}}`, map[string]string{"index.html": "<div id=root></div>"}), ".|frontend|"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Analyze(toFS(tc.files), Input{})
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Join(componentSummary(res.Components), "\n")
			if !strings.Contains(got, tc.want) {
				t.Errorf("sem %q:\n%s", tc.want, got)
			}
		})
	}
}

func TestComponentsNoSiblingsForPlainRepos(t *testing.T) {
	res, err := Analyze(toFS(djangoRepo(nil)), Input{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res.Components {
		if c.Sibling {
			t.Errorf("irmã falsa: %+v", c)
		}
	}
}
