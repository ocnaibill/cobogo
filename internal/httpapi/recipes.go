package httpapi

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Receitas (DEC-52 C): comandos conhecidos que pedem dados, montados por evidência e
// rodados com os valores só desta execução. O recurso geral é o comando com valores
// (vale para qualquer stack, pelo campo livre); a receita só preenche o comando e os
// campos quando a análise sabe qual é. É um catálogo fechado: cada linha diz de que
// evidência depende, e só entra a que foi verificada com um projeto real.

type recipeInput struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
}

type recipe struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Command     string        `json:"command"`
	Inputs      []recipeInput `json:"inputs"`
	Evidence    []string      `json:"evidence"`
}

const recipeDjangoSuperuser = "django_superuser"

var fieldLabel = map[string]string{"email": "E-mail", "username": "Usuário", "first_name": "Nome", "last_name": "Sobrenome", "name": "Nome", "nome": "Nome"}

// recipesFrom monta as receitas que a análise sustenta.
func recipesFrom(res domain.AnalysisResult) []recipe {
	out := []recipe{}
	if r, ok := djangoSuperuserRecipe(res); ok {
		out = append(out, r)
	}
	return out
}

// djangoSuperuserRecipe: um Django com django.contrib.auth. O createsuperuser lê, com
// --noinput, DJANGO_SUPERUSER_<CAMPO> para o campo de login e os obrigatórios do modelo
// de usuário, e DJANGO_SUPERUSER_PASSWORD. O modelo próprio (AUTH_USER_MODEL) troca os
// campos; sem ele, valem os do usuário padrão (username e email).
func djangoSuperuserRecipe(res domain.AnalysisResult) (recipe, bool) {
	var auth, model *domain.Evidence
	hasDjango := false
	for i, e := range res.Evidences {
		switch {
		case e.Kind == "django.settings_module":
			hasDjango = true
		case e.Kind == "django.setting" && e.Name == "AUTH" && auth == nil:
			auth = &res.Evidences[i]
		case e.Kind == "django.setting" && e.Name == "AUTH_USER_MODEL" && model == nil:
			model = &res.Evidences[i]
		}
	}
	if !hasDjango || auth == nil {
		return recipe{}, false
	}
	usernameField, required := "username", []string{"email"}
	evidence := []string{evidenceRef(*auth)}
	if model != nil {
		evidence = append(evidence, evidenceRef(*model))
		found := false
		for _, e := range res.Evidences {
			if e.Kind == "django.user_model" && e.Name == model.Value {
				field, req, _ := strings.Cut(e.Value, "|")
				usernameField, required, found = field, nil, true
				for _, f := range strings.Split(req, ",") {
					if f = strings.TrimSpace(f); f != "" {
						required = append(required, f)
					}
				}
				evidence = append(evidence, evidenceRef(e))
				break
			}
		}
		// Sem achar o modelo, não se adivinha o formulário.
		if !found {
			return recipe{}, false
		}
	}
	inputs := []recipeInput{}
	seen := map[string]bool{}
	for _, f := range append([]string{usernameField}, required...) {
		if seen[f] || !pyFieldName.MatchString(f) {
			continue
		}
		seen[f] = true
		label := fieldLabel[f]
		if label == "" {
			label = f
		}
		inputs = append(inputs, recipeInput{Name: "DJANGO_SUPERUSER_" + strings.ToUpper(f), Label: label, Required: true})
	}
	inputs = append(inputs, recipeInput{Name: "DJANGO_SUPERUSER_PASSWORD", Label: "Senha", Secret: true, Required: true})
	return recipe{
		ID:          recipeDjangoSuperuser,
		Title:       "Criar administrador",
		Description: "Cria o superusuário do Django (o login do /admin). Os dados vão só para este comando: a senha não vira variável do projeto e não aparece na saída.",
		Command:     "python manage.py createsuperuser --noinput",
		Inputs:      inputs,
		Evidence:    evidence,
	}, true
}

var pyFieldName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,40}$`)

func evidenceRef(e domain.Evidence) string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d", e.Source, e.Line)
	}
	return e.Source
}

// Valores só desta execução: nomes de variável comuns, poucos e curtos, nunca os da plataforma.
var inputName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

const (
	maxCommandInputs    = 10
	maxCommandInputSize = 4096
)

func checkCommandInputs(in map[string]string) string {
	if len(in) > maxCommandInputs {
		return fmt.Sprintf("no máximo %d valores por comando", maxCommandInputs)
	}
	for k, v := range in {
		switch {
		case !inputName.MatchString(k):
			return "nome de valor inválido: " + k + " (letras maiúsculas, números e _)"
		case k == "PORT" || strings.HasPrefix(k, "COBOGO_"):
			return k + " é da plataforma"
		case len(v) > maxCommandInputSize || !utf8.ValidString(v) || strings.ContainsRune(v, 0):
			return "o valor de " + k + " precisa ser um texto de até 4096 bytes, sem bytes nulos"
		}
	}
	return ""
}
