package analyzer

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

const ruleVersion = "0.2"

func evidenceIDs(evidence []domain.Evidence) []string {
	ids := make([]string, 0, len(evidence))
	for _, e := range evidence {
		ids = append(ids, e.ID)
	}
	return ids
}

func finding(field, message string, evidence ...domain.Evidence) domain.Finding {
	return domain.Finding{Field: field, Message: message, EvidenceIDs: evidenceIDs(evidence)}
}

// HEU-01: package.json e lockfile npm coerentes definem o perfil P-NODE.
func ruleHEU01() Rule {
	return Rule{
		Meta: RuleMeta{
			ID: "HEU-01", Version: ruleVersion, State: RuleProposed,
			Fields: []string{domain.FieldProfile, domain.FieldPackageManager},
		},
		Apply: func(in RuleInput) RuleOutput {
			var out RuleOutput
			unsupported := func(msg string, evidence ...domain.Evidence) {
				out.Unsupported = append(out.Unsupported, finding("", msg, evidence...))
			}

			manifest := in.Of(kindManifest)
			if invalid := in.Of(kindManifestInvalid); len(invalid) > 0 {
				unsupported("package.json inválido: "+invalid[0].Value, invalid[0])
				return out
			}
			if len(manifest) == 0 {
				unsupported("package.json ausente no diretório raiz; perfil P-NODE não se aplica")
				return out
			}

			locks := map[string]domain.Evidence{}
			for _, e := range in.Of(kindLockfile) {
				locks[e.Name] = e
			}
			npm, hasNPM := locks["npm"]
			var others []domain.Evidence
			var otherNames []string
			for _, manager := range []string{"yarn", "pnpm"} {
				if e, ok := locks[manager]; ok {
					others = append(others, e)
					otherNames = append(otherNames, manager)
				}
			}
			switch {
			case !hasNPM && len(others) > 0:
				unsupported(fmt.Sprintf("package-lock.json ausente e lockfile de outro gerenciador encontrado (%s); o perfil P-NODE exige npm", strings.Join(otherNames, ", ")), others...)
			case !hasNPM:
				unsupported("package-lock.json ausente; sem lockfile npm coerente não é possível fixar dependências", manifest...)
			case len(others) > 0:
				unsupported(fmt.Sprintf("múltiplos lockfiles (npm e %s); o perfil P-NODE exige um único gerenciador", strings.Join(otherNames, ", ")), append([]domain.Evidence{npm}, others...)...)
			}
			if ws := in.Of(kindWorkspaces); len(ws) > 0 {
				unsupported("workspaces declarado; monorepos ficam fora do perfil P-NODE do D0", ws...)
			}
			if len(out.Unsupported) > 0 {
				return out
			}

			out.Proposals = []domain.Proposal{
				{Field: domain.FieldProfile, Value: "P-NODE", Origin: domain.OriginInference, EvidenceIDs: evidenceIDs(append(slices.Clone(manifest), npm))},
				{Field: domain.FieldPackageManager, Value: "npm", Origin: domain.OriginInference, EvidenceIDs: evidenceIDs([]domain.Evidence{npm})},
			}
			return out
		},
	}
}

// HEU-02: scripts.start declara o comando de execução; scripts.build, quando
// presente, o comando de build (separar em regra própria com HEU-03).
func ruleHEU02() Rule {
	return Rule{
		Meta: RuleMeta{
			ID: "HEU-02", Version: ruleVersion, State: RuleProposed,
			Requires: []string{"HEU-01"},
			Fields:   []string{domain.FieldStartCommand, domain.FieldBuildCommand},
		},
		Apply: func(in RuleInput) RuleOutput {
			var out RuleOutput
			start := in.Named(kindScript, "start")
			switch {
			case len(start) == 0:
				out.Pending = append(out.Pending, finding(domain.FieldStartCommand,
					"HEU-02: scripts.start ausente; não é permitido assumir um entrypoint arbitrário"))
			case devServerTool(start[0].Value) != "":
				out.Pending = append(out.Pending, finding(domain.FieldStartCommand,
					fmt.Sprintf("HEU-02: scripts.start invoca %q, ferramenta de desenvolvimento; informe o comando de produção", devServerTool(start[0].Value)),
					start[0]))
			default:
				out.Proposals = append(out.Proposals, domain.Proposal{
					Field: domain.FieldStartCommand, Value: start[0].Value, Origin: domain.OriginDeclaration, EvidenceIDs: evidenceIDs(start),
				})
			}
			if build := in.Named(kindScript, "build"); len(build) > 0 {
				out.Proposals = append(out.Proposals, domain.Proposal{
					Field: domain.FieldBuildCommand, Value: build[0].Value, Origin: domain.OriginDeclaration, EvidenceIDs: evidenceIDs(build),
				})
			}
			return out
		},
	}
}

// HEU-05: um contrato de PORT em código de runtime é só uma pista; o número
// nunca é inferido (RN-02), então a porta sempre fica pendente até um override.
func ruleHEU05() Rule {
	return Rule{
		Meta: RuleMeta{
			ID: "HEU-05", Version: ruleVersion, State: RuleProposed,
			Requires: []string{"HEU-01"},
			Fields:   []string{domain.FieldPort},
		},
		Apply: func(in RuleInput) RuleOutput {
			refs := in.Named(kindEnvReference, "PORT")
			var runtime []domain.Evidence
			for _, e := range refs {
				if e.Scope == domain.ScopeRuntime {
					runtime = append(runtime, e)
				}
			}

			var msg string
			var cited []domain.Evidence
			switch {
			case len(runtime) > 0:
				msg = fmt.Sprintf("HEU-05: contrato de PORT detectado em %s, mas o valor numérico não é inferido automaticamente (RN-02); informe a porta", runtime[0].Source)
				cited = runtime
			case len(refs) > 0:
				msg = fmt.Sprintf("HEU-05: PORT referenciada apenas em código de teste ou de ferramentas (%s); sem contrato de runtime, informe a porta", refs[0].Source)
				cited = refs
			default:
				msg = "HEU-05: nenhum contrato de porta identificado nas evidências suportadas; informe a porta"
				if n := len(in.Coverage.Gaps); n > 0 {
					msg += fmt.Sprintf(" (%d arquivo(s) não puderam ser lidos; ver cobertura)", n)
				}
			}
			return RuleOutput{Pending: []domain.Finding{finding(domain.FieldPort, msg, cited...)}}
		},
	}
}

// HEU-09: inventário das variáveis de configuração (camada requires). Não
// classifica obrigatoriedade nem bloqueia o plano (ficha em MOTOR §6.2).
func ruleHEU09() Rule {
	return Rule{
		Meta: RuleMeta{
			ID: "HEU-09", Version: ruleVersion, State: RuleProposed,
			Requires: []string{"HEU-01"},
		},
		Apply: func(in RuleInput) RuleOutput {
			byName := map[string]*domain.ConfigVariable{}
			variable := func(name string) *domain.ConfigVariable {
				if byName[name] == nil {
					byName[name] = &domain.ConfigVariable{Name: name}
				}
				return byName[name]
			}
			for _, e := range in.Of(kindEnvDeclared) {
				v := variable(e.Name)
				v.DeclaredInExample = true
				v.EvidenceIDs = append(v.EvidenceIDs, e.ID)
			}
			for _, e := range in.Of(kindEnvReference) {
				v := variable(e.Name)
				v.References = append(v.References, domain.EnvReference{Path: e.Source, Scope: e.Scope})
				v.EvidenceIDs = append(v.EvidenceIDs, e.ID)
			}

			var out RuleOutput
			for _, v := range byName {
				out.ConfigVariables = append(out.ConfigVariables, *v)
			}
			return out
		},
	}
}

// devServerTool devolve o nome da ferramenta de desenvolvimento invocada por
// um comando, ou "" se nenhuma conhecida aparece. É uma lista curta de
// ferramentas; scripts indiretos (`npm run dev`) não são resolvidos.
func devServerTool(command string) string {
	separators := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n")
	for _, segment := range strings.Split(separators.Replace(command), "\n") {
		tokens := strings.Fields(segment)
		for len(tokens) > 0 && (strings.Contains(tokens[0], "=") || tokens[0] == "npx") {
			tokens = tokens[1:]
		}
		if len(tokens) == 0 {
			continue
		}
		if slices.Contains(tokens, "--watch") {
			return "--watch"
		}
		first, second := tokens[0], ""
		if len(tokens) > 1 {
			second = tokens[1]
		}
		switch first {
		case "nodemon", "ts-node-dev", "webpack-dev-server":
			return first
		case "next", "nuxt", "nuxi", "astro", "remix":
			if second == "dev" {
				return first + " dev"
			}
		case "webpack":
			if second == "serve" {
				return "webpack serve"
			}
		case "react-scripts":
			if second == "start" {
				return "react-scripts start"
			}
		case "tsx":
			if second == "watch" {
				return "tsx watch"
			}
		case "vite":
			if second == "" || second == "dev" || second == "serve" || strings.HasPrefix(second, "-") {
				return "vite"
			}
		}
	}
	return ""
}
