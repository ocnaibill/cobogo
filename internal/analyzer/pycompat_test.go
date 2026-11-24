package analyzer

import (
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func flaskRepoWith(requirements string, extra map[string]string) map[string]string {
	files := map[string]string{
		"requirements.txt": requirements,
		"app.py":           "from flask import Flask\napp = Flask(__name__)\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

func pythonVersionAsk(res domain.AnalysisResult) *domain.Finding {
	for i := range res.Pending {
		if res.Pending[i].Field == domain.FieldPythonVersion {
			return &res.Pending[i]
		}
	}
	return nil
}

func TestPythonRangeLookup(t *testing.T) {
	for _, c := range []struct {
		pkg, ver, lo, hi string
		ok               bool
	}{
		{"pillow", "10.2.0", "3.8", "3.12", true},
		{"pillow", "11.1.0", "3.9", "3.13", true},
		{"django", "3.1.2", "3.6", "3.8", true},
		{"django", "5.1.3", "3.10", "3.13", true},
		{"psycopg2", "2.9.9", "3.7", "3.12", true},
		{"flask", "3.1.0", "", "", false}, // fora da lista fechada
		{"django", "0.5", "", "", false},  // antes do primeiro release conhecido
	} {
		lo, hi, ok := pythonRangeOf(c.pkg, c.ver)
		if lo != c.lo || hi != c.hi || ok != c.ok {
			t.Errorf("pythonRangeOf(%s, %s) = %q %q %v; want %q %q %v", c.pkg, c.ver, lo, hi, ok, c.lo, c.hi, c.ok)
		}
	}
	if !versionLess("10.2", "10.10") || versionLess("3.2.25", "3.2.9") || versionLess("5.1", "5.1.0") {
		t.Error("versionLess compara números, não texto")
	}
}

// DEC-54 A: a época das dependências fixadas contra o Python que o Cobogó usaria.
func TestPinnedDependenciesAskForAnOlderPython(t *testing.T) {
	cases := []struct {
		name string
		reqs string
		want string // versão sugerida; "" = nenhuma pergunta
		ask  bool
	}{
		{"Pillow 10.2 declara até 3.12", "flask==3.1.0\nPillow==10.2.0\n", "3.12", true},
		{"psycopg2 2.9.9 declara até 3.12", "flask==3.1.0\npsycopg2==2.9.9\n", "3.12", true},
		{"Django 3.1 só cabe num Python legado", "flask==3.1.0\nDjango==3.1.2\n", "3.8", true},
		{"o mais restritivo manda", "flask==3.1.0\nDjango==4.2.8\nPillow==9.1.1\n", "3.10", true},
		{"Django 5.1.3 já declara o 3.13: nada a perguntar", "flask==3.1.0\nDjango==5.1.3\n", "", false},
		{"sem fixar com ==: o motor não sabe a versão", "flask==3.1.0\nPillow>=9.0\n", "", false},
		{"pacote fora da lista fechada", "flask==3.1.0\nrequests==2.31.0\n", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := analyzeNode(t, flaskRepoWith(c.reqs, nil), nil)
			ask := pythonVersionAsk(res)
			if !c.ask {
				if ask != nil {
					t.Fatalf("unexpected question: %+v", ask)
				}
				return
			}
			if ask == nil || len(ask.Options) < 2 || ask.Options[0].Value != c.want {
				t.Fatalf("pending = %+v, want first option %s", res.Pending, c.want)
			}
			if res.Fields[domain.FieldPythonVersion].Value != domain.DefaultPythonVersion {
				t.Errorf("the plan keeps the default until the person answers, got %q", res.Fields[domain.FieldPythonVersion].Value)
			}
			if domain.IsLegacyPython(c.want) && !hasMessage([]domain.Finding{{Message: ask.Options[0].Note}}, "só por confirmação") {
				t.Errorf("a legacy suggestion must say it needs confirmation: %q", ask.Options[0].Note)
			}
		})
	}
}

func TestPythonVersionAnswerEndsTheQuestion(t *testing.T) {
	files := flaskRepoWith("flask==3.1.0\nPillow==10.2.0\n", nil)
	res := analyzeNode(t, files, map[string]string{"python_version": "3.12"})
	if pythonVersionAsk(res) != nil || res.Fields[domain.FieldPythonVersion].Value != "3.12" || res.Fields[domain.FieldPythonVersion].Origin != domain.OriginOverride {
		t.Fatalf("answer ignored: %+v pending=%+v", res.Fields[domain.FieldPythonVersion], res.Pending)
	}
	if res.Outcome != domain.AnalysisReady {
		t.Fatalf("outcome = %s, pending = %+v", res.Outcome, res.Pending)
	}
	// Quem declarou a versão decide: só aviso.
	res = analyzeNode(t, flaskRepoWith("flask==3.1.0\nPillow==10.2.0\n", map[string]string{".python-version": "3.13\n"}), nil)
	if pythonVersionAsk(res) != nil || !hasMessage(res.Warnings, "Você declarou essa versão") {
		t.Fatalf("a declared version only warns: pending=%+v warnings=%+v", res.Pending, res.Warnings)
	}
}

func TestRequiresPythonLimitsTheSuggestion(t *testing.T) {
	// Pillow 10.2 admite 3.8 a 3.12; o requires-python do projeto exclui o 3.10 e abaixo.
	files := map[string]string{
		"requirements.txt": "flask==3.1.0\nPillow==10.2.0\n",
		"pyproject.toml":   "[project]\nname='x'\nversion='0'\nrequires-python='>=3.11'\ndependencies=['flask']\n",
		"app.py":           "from flask import Flask\napp = Flask(__name__)\n",
	}
	ask := pythonVersionAsk(analyzeNode(t, files, nil))
	if ask == nil || ask.Options[0].Value != "3.12" {
		t.Fatalf("expected 3.12, got %+v", ask)
	}
}
