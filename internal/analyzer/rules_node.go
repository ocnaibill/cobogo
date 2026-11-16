package analyzer

import (
	"fmt"
	"regexp"
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
			Fields:   []string{domain.FieldStartCommand, domain.FieldBuildCommand, domain.FieldExecutionMode},
		},
		Apply: func(in RuleInput) RuleOutput {
			// O modo de execução nunca é inferido: o padrão é produção e só um
			// override do usuário o muda (DEC-25).
			out := RuleOutput{Proposals: []domain.Proposal{
				{Field: domain.FieldExecutionMode, Value: domain.ModeProduction, Origin: domain.OriginDefault},
			}}
			start := in.Named(kindScript, "start")
			options, omitted := startOptions(in.Of(kindScript))
			switch {
			case len(start) == 0:
				f := finding(domain.FieldStartCommand,
					"HEU-02: scripts.start ausente; não é permitido assumir um entrypoint arbitrário")
				f.Options, f.Omitted = options, omitted
				out.Pending = append(out.Pending, f)
			case devServerTool(start[0].Value) != "":
				f := finding(domain.FieldStartCommand,
					fmt.Sprintf("HEU-02: scripts.start invoca %q, ferramenta de desenvolvimento; informe o comando de produção", devServerTool(start[0].Value)),
					start[0])
				f.Options, f.Omitted = options, omitted
				out.Pending = append(out.Pending, f)
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
			Fields:   []string{domain.FieldPort, domain.FieldHealthPath},
		},
		Apply: func(in RuleInput) RuleOutput {
			// Caminho da sonda de readiness: o padrão é a raiz; só um override
			// o muda (DEC-26).
			defaultHealth := domain.Proposal{Field: domain.FieldHealthPath, Value: "/", Origin: domain.OriginDefault}
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
			return RuleOutput{
				Proposals: []domain.Proposal{defaultHealth},
				Pending:   []domain.Finding{finding(domain.FieldPort, msg, cited...)},
			}
		},
	}
}

// HEU-09: inventário das variáveis de configuração, com classe, estado no
// exemplo e aviso por variável, e avisos de exposição de credenciais (DEC-24).
// Nada aqui bloqueia o plano.
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
					byName[name] = &domain.ConfigVariable{Name: name, Class: classifyVariable(name), Example: domain.ExampleAbsent}
				}
				return byName[name]
			}

			// Estado no exemplo: uma declaração ativa vale mais que uma
			// comentada; entre ativas, vale a primeira (ordem estável).
			defaults := map[string]string{}
			for _, e := range in.Of(kindEnvDefault) {
				if _, ok := defaults[e.Name]; !ok {
					defaults[e.Name] = e.Value
				}
			}
			for _, e := range in.Of(kindEnvDeclared) {
				v := variable(e.Name)
				state := domain.ExampleState(e.Value)
				v.EvidenceIDs = append(v.EvidenceIDs, e.ID)
				switch {
				case v.Example == domain.ExampleAbsent:
					v.Example = state
				case v.Example == domain.ExampleCommented && state != domain.ExampleCommented:
					v.Example = state
				}
				v.DeclaredInExample = v.Example != domain.ExampleAbsent && v.Example != domain.ExampleCommented
			}
			for _, e := range in.Of(kindEnvReference) {
				v := variable(e.Name)
				v.References = append(v.References, domain.EnvReference{Path: e.Source, Scope: e.Scope})
				v.EvidenceIDs = append(v.EvidenceIDs, e.ID)
			}

			hasExampleFile := len(in.Of(kindEnvExampleFile)) > 0
			var out RuleOutput
			for _, v := range byName {
				if v.Example == domain.ExampleDefault {
					v.DefaultValue = defaults[v.Name]
				}
				v.Hint = configHint(*v, hasExampleFile)
				out.ConfigVariables = append(out.ConfigVariables, *v)
			}

			for _, e := range in.Of(kindEnvExposure) {
				out.Warnings = append(out.Warnings, exposureWarning(e))
			}
			return out
		},
	}
}

// configHint monta o aviso de uma variável, ou nil quando não há o que dizer.
// Variáveis da plataforma nunca têm aviso: o usuário não as preenche.
func configHint(v domain.ConfigVariable, hasExampleFile bool) *domain.ConfigHint {
	if v.Class == domain.ClassPlatform {
		return nil
	}
	hint := func(code, msg string) *domain.ConfigHint { return &domain.ConfigHint{Code: code, Message: msg} }
	switch v.Example {
	case domain.ExamplePlaceholder:
		return hint(domain.HintLikelyRequired, "O exemplo traz só um valor de preenchimento; provavelmente a variável é obrigatória.")
	case domain.ExampleExposed:
		return hint(domain.HintLikelyRequired, "O exemplo traz o que parece ser uma credencial real, que o Cobogó não usa; provavelmente a variável é obrigatória.")
	case domain.ExampleEmpty:
		return hint(domain.HintLikelyOptional, "O exemplo declara a variável sem valor, então provavelmente é opcional. Preencha agora se achar que o programa quebra sem ela.")
	case domain.ExampleCommented:
		return hint(domain.HintCommented, "A variável está comentada no exemplo; provavelmente é opcional ou alternativa a outra.")
	case domain.ExampleDefault:
		return hint(domain.HintPrefilled, "O exemplo traz um valor padrão; confirme para usá-lo.")
	}
	for _, r := range v.References {
		if r.Scope != domain.ScopeRuntime {
			continue
		}
		if hasExampleFile {
			return hint(domain.HintUndeclared, "O código usa esta variável, mas ela não está no arquivo de exemplo.")
		}
		return hint(domain.HintReferencedOnly, "O código usa esta variável e o projeto não traz arquivo de exemplo; informe um valor se o programa precisar dela.")
	}
	return nil
}

const rotateAdvice = "Revogue ou rotacione a credencial; removê-la do histórico do Git não basta. O Cobogó não usa valores que vêm do repositório."

// exposureWarning descreve uma exposição sem repetir nem citar o valor.
func exposureWarning(e domain.Evidence) domain.Finding {
	where := e.Source
	if e.Line > 0 {
		where = fmt.Sprintf("%s:%d", e.Source, e.Line)
	}
	msg := fmt.Sprintf("HEU-09: %s em %s parece ser uma credencial real (%s). %s", e.Name, where, exposureLabels[e.Value], rotateAdvice)
	if e.Value == exposureCommittedFile {
		msg = fmt.Sprintf("HEU-09: o arquivo de ambiente %s está versionado no repositório e pode conter credenciais reais. %s", e.Name, rotateAdvice)
	}
	return domain.Finding{Message: msg, EvidenceIDs: []string{e.ID}}
}

// scriptNamePattern limita as opções a nomes seguros para entrar sem aspas em
// `npm run <nome>`; o nome acaba dentro de um `sh -c`.
var scriptNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.-]*$`)

// notRunScript são palavras iniciais de scripts que não são um jeito de
// executar o projeto em nenhum modo. `build` entra aqui até haver suporte a
// build como forma de execução (estáticos, HEU-03). Lista curta e por
// convenção: o usuário ainda pode digitar qualquer comando e a interface pode
// mostrar o que foi omitido.
var notRunScript = []string{"build", "test", "lint", "format", "typecheck", "clean"}

// developmentName são nomes que só reforçam uma classificação de
// desenvolvimento; sozinhos não bastam para afirmá-la (HEU-02 no motor).
var developmentName = []string{"dev", "watch"}

// startOptions oferece scripts do package.json como candidatos a comando de
// execução, no formato `npm run <nome>`, classificados em produção ou
// desenvolvimento. É uma lista de escolha, não uma inferência: nada é aplicado
// sem o usuário escolher. O que não é oferecido volta em omitted, com o nome do
// script: scripts de teste/lint/build, ganchos pre/post de outro script e
// nomes que não são seguros para um `sh -c`.
func startOptions(scripts []domain.Evidence) (options []domain.Option, omitted []string) {
	names := map[string]bool{}
	for _, e := range scripts {
		names[e.Name] = true
	}
	for _, e := range scripts {
		name := e.Name
		switch {
		case !scriptNamePattern.MatchString(name),
			slices.ContainsFunc(notRunScript, func(word string) bool { return hasWord(name, word) }),
			isHookOf(name, names):
			omitted = append(omitted, name)
			continue
		}
		opt := domain.Option{Value: "npm run " + name, Kind: domain.OptionProduction}
		switch tool := devServerTool(e.Value); {
		case tool != "":
			opt.Kind = domain.OptionDevelopment
			opt.Note = fmt.Sprintf("invoca %q, ferramenta de desenvolvimento", tool)
		case slices.ContainsFunc(developmentName, func(word string) bool { return hasWord(name, word) }):
			opt.Kind = domain.OptionDevelopment
			opt.Note = "o nome sugere desenvolvimento; o comando não foi reconhecido como ferramenta de desenvolvimento"
		}
		if name == "start" {
			opt.Note = strings.TrimSpace("é o scripts.start declarado, que gerou esta pendência. " + opt.Note)
		}
		options = append(options, opt)
	}
	// Produção antes de desenvolvimento; alfabético dentro de cada grupo.
	slices.SortFunc(options, func(a, b domain.Option) int {
		if a.Kind != b.Kind {
			if a.Kind == domain.OptionProduction {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Value, b.Value)
	})
	slices.Sort(omitted)
	return options, omitted
}

// isHookOf diz se name é o gancho pre/post de outro script existente, que o
// npm já executa sozinho junto dele.
func isHookOf(name string, names map[string]bool) bool {
	for _, prefix := range []string{"pre", "post"} {
		if target, ok := strings.CutPrefix(name, prefix); ok && names[target] {
			return true
		}
	}
	return false
}

// hasWord diz se name é word ou começa com word seguido de separador
// (`test:unit`, `build-css`), sem pegar nomes que só compartilham o prefixo
// (`devices`).
func hasWord(name, word string) bool {
	rest, ok := strings.CutPrefix(name, word)
	return ok && (rest == "" || strings.ContainsRune(":-_.", rune(rest[0])))
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
