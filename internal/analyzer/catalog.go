package analyzer

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

type RuleState string

const (
	RuleProposed  RuleState = "proposed"
	RuleValidated RuleState = "validated"
	RuleRetired   RuleState = "retired"
)

// RuleMeta é a identidade de uma regra (§6 do motor). Requires lista regras
// cuja pré-condição precisa ter sido satisfeita; Fields, os únicos campos
// em que a regra pode escrever.
type RuleMeta struct {
	ID       string
	Version  string
	State    RuleState
	Requires []string
	Fields   []string
}

type RuleInput struct {
	Evidence []domain.Evidence
	Coverage domain.Coverage
	// Overrides são os overrides válidos do deploy. Uma regra só os lê quando
	// a resposta do usuário muda o que ela faz (a escolha do gerenciador muda
	// o perfil; DEC-36); o valor final de cada campo continua sendo do resolve.
	Overrides map[string]string
}

func (in RuleInput) Of(kind string) []domain.Evidence {
	var out []domain.Evidence
	for _, e := range in.Evidence {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func (in RuleInput) Named(kind, name string) []domain.Evidence {
	var out []domain.Evidence
	for _, e := range in.Evidence {
		if e.Kind == kind && e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

type RuleOutput struct {
	Proposals       []domain.Proposal
	Pending         []domain.Finding
	Conflicts       []domain.Finding
	Unsupported     []domain.Finding
	ConfigVariables []domain.ConfigVariable
	Warnings        []domain.Finding
}

type Rule struct {
	Meta  RuleMeta
	Apply func(RuleInput) RuleOutput
}

// FieldSpec define um campo do plano: se é obrigatório, se aceita override
// do usuário e como validar o valor.
type FieldSpec struct {
	Name        string
	Required    bool
	Overridable bool
	Validate    func(string) error
}

type Extractor struct {
	ID      string
	Extract func(fsys fs.FS, x *Extraction) error
}

type Catalog struct {
	Version    string
	Extractors []Extractor
	Rules      []Rule
	Fields     []FieldSpec
}

// O catálogo JavaScript cobre os perfis P-NODE (npm) e P-BUN (DEC-36).
const nodeCatalogVersion = "p-js-0.5"

func NodeCatalog() Catalog {
	return Catalog{
		Version: nodeCatalogVersion,
		Extractors: []Extractor{
			{ID: extractorManifest, Extract: extractManifest},
			{ID: extractorEnvFiles, Extract: extractEnvFiles},
			{ID: extractorSource, Extract: extractSource},
		},
		Rules: []Rule{ruleHEU01(), ruleHEU02(), ruleHEU05(), ruleHEU09(), ruleHEU10(), ruleHEU11()},
		Fields: []FieldSpec{
			{Name: domain.FieldProfile, Required: true},
			{Name: domain.FieldPackageManager, Required: true, Overridable: true, Validate: validatePackageManager},
			{Name: domain.FieldStartCommand, Required: true, Overridable: true, Validate: validateCommand},
			{Name: domain.FieldBuildCommand, Overridable: true, Validate: validateCommand},
			{Name: domain.FieldExecutionMode, Overridable: true, Validate: validateExecutionMode},
			{Name: domain.FieldNodeVersion, Overridable: true, Validate: validateNodeVersion},
			{Name: domain.FieldBunVersion, Overridable: true, Validate: validateBunVersion},
			{Name: domain.FieldPort, Required: true, Overridable: true, Validate: validatePort},
			{Name: domain.FieldHealthPath, Overridable: true, Validate: validateHealthPath},
		},
	}
}

func validatePackageManager(v string) error {
	if v != domain.ManagerNPM && v != domain.ManagerBun {
		return fmt.Errorf("%q não é um gerenciador suportado (npm ou bun)", v)
	}
	return nil
}

var bunVersionPattern = regexp.MustCompile(`^1(\.\d{1,3}){0,2}$`)

// validateBunVersion aceita uma tag da imagem oven/bun da major 1: "1",
// "1.2" ou "1.2.3" (DEC-36).
func validateBunVersion(v string) error {
	if !bunVersionPattern.MatchString(v) {
		return fmt.Errorf("%q não é uma versão do Bun 1 (use 1, 1.Y ou 1.Y.Z)", v)
	}
	return nil
}

func validatePort(v string) error {
	port, err := strconv.Atoi(v)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%q não é uma porta entre 1 e 65535", v)
	}
	return nil
}

func validateCommand(v string) error {
	if v == "" {
		return errors.New("comando vazio")
	}
	if strings.ContainsAny(v, "\r\n\x00") {
		return errors.New("o comando deve ter uma única linha")
	}
	if strings.HasSuffix(v, "\\") {
		// Num Dockerfile, a barra no fim continuaria na instrução seguinte.
		return errors.New("o comando não pode terminar em barra invertida")
	}
	return nil
}

// validateHealthPath aceita um caminho HTTP simples para a sonda: começa com
// "/", sem espaço nem caractere de controle, até 256 bytes (DEC-26 item 9).
func validateHealthPath(v string) error {
	switch {
	case !strings.HasPrefix(v, "/"):
		return fmt.Errorf("%q deve começar com /", v)
	case len(v) > 256:
		return errors.New("o caminho excede 256 bytes")
	}
	for _, r := range v {
		if r <= ' ' || r == 0x7f {
			return errors.New("o caminho não pode ter espaço nem caractere de controle")
		}
	}
	return nil
}

func validateExecutionMode(v string) error {
	if v != domain.ModeProduction && v != domain.ModeDevelopment {
		return fmt.Errorf("%q não é um modo de execução; use %q ou %q", v, domain.ModeProduction, domain.ModeDevelopment)
	}
	return nil
}
