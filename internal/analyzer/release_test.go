package analyzer

import (
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// A fase de release (HEU-04.8, DEC-52): declarada pelo Procfile, inferida num Django com
// migrações, ausente sem elas, e desligável.
func TestReleaseCommand(t *testing.T) {
	withMigrations := map[string]string{"events/__init__.py": "", "events/migrations/__init__.py": "", "events/migrations/0001_initial.py": "from django.db import migrations\n"}
	res, err := Analyze(toFS(djangoRepo(withMigrations)), Input{})
	if err != nil {
		t.Fatal(err)
	}
	f := res.Fields[domain.FieldReleaseCommand]
	if f.Value != "python manage.py migrate --noinput" || f.Origin != domain.OriginInference {
		t.Errorf("Django com migrações: %+v", f)
	}
	if res, _ := Analyze(toFS(djangoRepo(nil)), Input{}); res.Fields[domain.FieldReleaseCommand].Value != "" {
		t.Errorf("sem migrações não há fase de release: %+v", res.Fields[domain.FieldReleaseCommand])
	}
	files := djangoRepo(withMigrations)
	files["Procfile"] = "web: gunicorn mysite.wsgi\nrelease: python manage.py migrate && python manage.py loaddata base\n"
	res, _ = Analyze(toFS(files), Input{})
	if f := res.Fields[domain.FieldReleaseCommand]; f.Value != "python manage.py migrate && python manage.py loaddata base" || f.Origin != domain.OriginDeclaration {
		t.Errorf("Procfile: %+v", f)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w.Message, "fase release") {
			t.Errorf("a fase release não é mais um aviso: %s", w.Message)
		}
	}
	res, _ = Analyze(toFS(djangoRepo(withMigrations)), Input{Overrides: map[string]string{domain.FieldReleaseCommand: "none"}})
	if res.Fields[domain.FieldReleaseCommand].Value != "none" {
		t.Errorf("override none: %+v", res.Fields[domain.FieldReleaseCommand])
	}
}

// O aviso de que nenhuma migração roda só vale quando a fase de release não vai rodar.
func TestMigrationNoticeFollowsRelease(t *testing.T) {
	withMigrations := map[string]string{"events/__init__.py": "", "events/migrations/__init__.py": "", "events/migrations/0001_initial.py": "from django.db import migrations\n"}
	has := func(files map[string]string, overrides map[string]string) bool {
		res, err := Analyze(toFS(files), Input{Overrides: overrides})
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range res.Warnings {
			if strings.Contains(w.Message, "nenhum comando de migração roda") {
				return true
			}
		}
		return false
	}
	if has(djangoRepo(withMigrations), nil) {
		t.Error("com migrações no repositório a release roda `migrate`: o aviso não cabe")
	}
	if !has(djangoRepo(withMigrations), map[string]string{domain.FieldReleaseCommand: "none"}) {
		t.Error("com a release desligada o aviso volta")
	}
	if has(djangoRepo(withMigrations), map[string]string{domain.FieldReleaseCommand: "python manage.py migrate && python manage.py seed"}) {
		t.Error("com um comando de release trocado o aviso não cabe")
	}
	if !has(djangoRepo(nil), nil) {
		t.Error("sem migrações nem Procfile com release, nada roda e o aviso fica")
	}
	procfile := djangoRepo(nil)
	procfile["Procfile"] = "web: gunicorn mysite.wsgi\nrelease: python manage.py migrate\n"
	if has(procfile, nil) {
		t.Error("o release: do Procfile cobre as migrações")
	}
}

// O modelo de usuário próprio do Django: o campo de login e os obrigatórios.
func TestDjangoUserModelEvidence(t *testing.T) {
	files := djangoRepo(map[string]string{
		"mysite/settings.py": djangoSettings + "INSTALLED_APPS = ['django.contrib.auth', 'users']\nAUTH_USER_MODEL = 'users.CustomUser'\n",
		"users/__init__.py":  "",
		"users/models.py":    "from django.db import models\n\nclass Manager(models.Manager):\n    pass\n\nclass CustomUser(AbstractBaseUser):\n    email = models.EmailField(unique=True)\n    USERNAME_FIELD = \"email\"\n    REQUIRED_FIELDS = [\"name\", 'cpf']\n",
	})
	res, err := Analyze(toFS(files), Input{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range res.Evidences {
		switch {
		case e.Kind == kindDjangoUser:
			got["model:"+e.Name] = e.Value
		case e.Kind == kindDjangoSet && (e.Name == "AUTH" || e.Name == "AUTH_USER_MODEL"):
			got[e.Name] = e.Value
		}
	}
	if got["model:CustomUser"] != "email|name,cpf" || got["AUTH_USER_MODEL"] != "CustomUser" {
		t.Errorf("evidências = %v", got)
	}
	if _, ok := got["AUTH"]; !ok {
		t.Errorf("django.contrib.auth não visto: %v", got)
	}
}
