package analyzer

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
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

// jsManager diz qual gerenciador vale para o projeto: npm (package-lock.json)
// ou bun (bun.lock, ou bun.lockb), com o lockfile que o comprova. Com os dois
// lockfiles, vale a resposta do usuário (override de package_manager); sem
// ela, devolve "" e a HEU-01 pergunta (DEC-36).
func jsManager(in RuleInput) (string, domain.Evidence) {
	locks := map[string]domain.Evidence{}
	for _, e := range in.Of(kindLockfile) {
		// bun.lock (texto) prevalece sobre bun.lockb quando os dois existem.
		if prev, ok := locks[e.Name]; ok && prev.Source == "bun.lock" {
			continue
		}
		locks[e.Name] = e
	}
	npm, hasNPM := locks[domain.ManagerNPM]
	bun, hasBun := locks[domain.ManagerBun]
	switch {
	case hasNPM && hasBun:
		switch in.Overrides[domain.FieldPackageManager] {
		case domain.ManagerNPM:
			return domain.ManagerNPM, npm
		case domain.ManagerBun:
			return domain.ManagerBun, bun
		}
		return "", domain.Evidence{}
	case hasNPM:
		return domain.ManagerNPM, npm
	case hasBun:
		return domain.ManagerBun, bun
	}
	return "", domain.Evidence{}
}

// HEU-01: package.json e um lockfile coerente definem o perfil: npm → P-NODE,
// Bun → P-BUN (DEC-36), ou P-STATIC quando o build gera um site estático
// (DEC-37). Um index.html na raiz sem build é site estático sem instalação.
// npm e Bun juntos viram pergunta; yarn, pnpm e workspaces seguem fora.
func ruleHEU01() Rule {
	return Rule{
		Meta: RuleMeta{
			ID: "HEU-01", Version: "0.5", State: RuleProposed,
			Fields: []string{domain.FieldProfile, domain.FieldPackageManager, domain.FieldAllowUnlocked},
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
			profile, manager, sd := jsProfile(in)
			// Site estático sem build: nada a instalar, lockfile dispensável.
			if profile == domain.ProfileStatic && sd.NoBuild {
				out.Proposals = []domain.Proposal{
					{Field: domain.FieldProfile, Value: domain.ProfileStatic, Origin: domain.OriginInference, EvidenceIDs: evidenceIDs(sd.Evidence)},
					{Field: domain.FieldPackageManager, Value: domain.ManagerNone, Origin: domain.OriginInference, EvidenceIDs: evidenceIDs(sd.Evidence)},
				}
				return out
			}
			if len(manifest) == 0 {
				unsupported("package.json ausente e nenhum index.html na raiz; nenhum perfil (P-NODE, P-BUN, P-STATIC) se aplica")
				return out
			}

			var others []domain.Evidence
			var otherNames []string
			for _, manager := range []string{"yarn", "pnpm"} {
				if e := in.Named(kindLockfile, manager); len(e) > 0 {
					others = append(others, e[0])
					otherNames = append(otherNames, manager)
				}
			}
			npm := in.Named(kindLockfile, domain.ManagerNPM)
			bun := in.Named(kindLockfile, domain.ManagerBun)
			switch {
			case len(others) > 0 && len(npm)+len(bun) == 0:
				unsupported(fmt.Sprintf("lockfile de gerenciador não suportado (%s); o Cobogó aceita npm (package-lock.json) e Bun (bun.lock)", strings.Join(otherNames, ", ")), others...)
			case len(others) > 0:
				unsupported(fmt.Sprintf("múltiplos lockfiles, incluindo de gerenciador não suportado (%s); deixe só o do npm ou o do Bun", strings.Join(otherNames, ", ")), others...)
			}
			if ws := in.Of(kindWorkspaces); len(ws) > 0 {
				unsupported("workspaces declarado; monorepos ficam fora dos perfis JavaScript", ws...)
			}
			if len(out.Unsupported) > 0 {
				return out
			}

			_, lock := jsManager(in)
			if lock.Value == unlockedMarker {
				// Sem lockfile (DEC-53 G e H).
				switch {
				case len(in.Of(kindAncestorLock)) > 0:
					// O lockfile existe, só que acima desta pasta: é o de um workspace.
					anc := in.Of(kindAncestorLock)
					unsupported(fmt.Sprintf("esta pasta não tem lockfile, mas %s existe na pasta acima (%s): é o lockfile de um workspace, e workspaces ficam fora dos perfis JavaScript. Publique a aplicação a partir de um repositório próprio ou gere um package-lock.json nesta pasta (npm install --package-lock-only)", anc[0].Source, anc[0].Name), anc...)
					return out
				case in.Overrides[domain.FieldAllowUnlocked] == "false":
					unsupported("nenhum lockfile (package-lock.json ou bun.lock) e você escolheu não instalar sem ele", manifest...)
					return out
				case in.Overrides[domain.FieldAllowUnlocked] == "true":
					out.Warnings = append(out.Warnings, finding("", "HEU-01: o projeto não tem lockfile e você escolheu instalar sem ele: o build resolve as versões mais novas que o package.json permite e um deploy futuro pode instalar outras. Gere e versione um package-lock.json (npm install --package-lock-only) para fixá-las", manifest...))
				default:
					// Pergunta em vez de recusar: a instalação sem lock não é reproduzível, e a
					// pessoa decide. As demais regras seguem, para todas as perguntas virem juntas.
					f := finding(domain.FieldAllowUnlocked,
						"HEU-01: o projeto não tem lockfile (package-lock.json ou bun.lock), então as versões das dependências não estão fixadas. Posso instalar com `npm install`, que resolve as versões mais novas que o package.json permite: pode funcionar hoje e quebrar num deploy futuro. Quer instalar assim?", manifest...)
					f.Options = []domain.Option{{Value: "true", Kind: domain.OptionProduction, Note: "instala sem lockfile (npm install; bun install se o package.json declara Bun); o aviso fica na análise"}}
					out.Pending = append(out.Pending, f)
				}
			}
			if manager == "" {
				f := finding(domain.FieldPackageManager,
					"HEU-01: o repositório tem lockfile do npm e do Bun; escolha qual gerenciador usar (o outro lockfile é ignorado)",
					npm[0], bun[0])
				f.Options = []domain.Option{
					{Value: domain.ManagerNPM, Kind: domain.OptionProduction, Note: "usa package-lock.json e o runtime Node"},
					{Value: domain.ManagerBun, Kind: domain.OptionProduction, Note: "usa " + bun[0].Source + " e o runtime Bun"},
				}
				out.Pending = append(out.Pending, f)
				return out
			}

			// Um override de perfil de servidor precisa casar com o gerenciador.
			if want := in.Overrides[domain.FieldProfile]; want != "" && want != domain.ProfileStatic && want != profile {
				out.Conflicts = append(out.Conflicts, finding(domain.FieldProfile, fmt.Sprintf(
					"HEU-01: o perfil %s não combina com o gerenciador %s deste repositório; use %s ou %s", want, manager, profile, domain.ProfileStatic), lock))
				return out
			}
			out.Proposals = []domain.Proposal{
				{Field: domain.FieldProfile, Value: profile, Origin: domain.OriginInference, EvidenceIDs: evidenceIDs(slices.Concat(manifest, []domain.Evidence{lock}, sd.Evidence))},
				{Field: domain.FieldPackageManager, Value: manager, Origin: domain.OriginInference, EvidenceIDs: evidenceIDs([]domain.Evidence{lock})},
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
			profile, _, _ := jsProfile(in)
			manager, _ := jsManager(in)
			options, omitted := startOptions(in.Of(kindScript), manager)
			if manager == domain.ManagerBun {
				options = append(options, entryOptions(in.Of(kindEntryField), options)...)
			}
			switch {
			case profile == domain.ProfileStatic:
				// O site estático é servido pela plataforma: não há comando de
				// execução a pedir (DEC-37).
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
				// O mesmo arquivo sem a recarga automática é uma opção, nunca uma escolha
				// do Cobogó (DEC-53 C).
				if cmd, ok := productionEquivalent(start[0].Value); ok {
					f.Options = slices.Insert(f.Options, 0, domain.Option{
						Value: cmd, Kind: domain.OptionProduction,
						Note: fmt.Sprintf("derivado de %q: o mesmo arquivo, sem recarga automática; confira que é o que você roda em produção", start[0].Value),
					})
				}
				out.Pending = append(out.Pending, f)
			default:
				out.Proposals = append(out.Proposals, domain.Proposal{
					Field: domain.FieldStartCommand, Value: start[0].Value, Origin: domain.OriginDeclaration, EvidenceIDs: evidenceIDs(start),
				})
			}
			if prepare := in.Named(kindScript, "prepare"); len(prepare) > 0 && domain.GitHookOnly(prepare[0].Value) {
				out.Warnings = append(out.Warnings, finding("", fmt.Sprintf(
					"HEU-02: scripts.prepare (%q) só instala ganchos do Git; no contêiner não há repositório Git e a ferramenta costuma ser dependência de desenvolvimento, então o Cobogó o remove do package.json da imagem antes de instalar",
					prepare[0].Value), prepare[0]))
			}
			for _, missing := range in.Of(kindMissingEnvFile) {
				hint := ""
				if missing.Value != "" {
					hint = fmt.Sprintf("; %s traz os nomes das variáveis", missing.Value)
				}
				out.Warnings = append(out.Warnings, finding("", fmt.Sprintf(
					"HEU-02: %s lê variáveis do arquivo %q, que não está no repositório (arquivos assim costumam ficar fora do Git de propósito)%s. Cadastre as variáveis na aba Variáveis: sem o arquivo, o comando recusa iniciar",
					scriptLabel(missing.Source), missing.Name, hint), missing))
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

// HEU-05: quando o código de execução lê PORT, a plataforma escolhe o número e
// o entrega (DEC-35): é contrato, não porta padrão (RN-02). O valor de reserva
// escrito no código nunca é adotado. Sem contrato de execução, a porta fica
// pendente até um override.
func ruleHEU05() Rule {
	return Rule{
		Meta: RuleMeta{
			ID: "HEU-05", Version: "0.4", State: RuleProposed,
			Requires: []string{"HEU-01"},
			Fields:   []string{domain.FieldPort, domain.FieldHealthPath},
		},
		Apply: func(in RuleInput) RuleOutput {
			// Caminho da sonda de readiness: o padrão é a raiz; só um override
			// o muda (DEC-26).
			defaultHealth := domain.Proposal{Field: domain.FieldHealthPath, Value: "/", Origin: domain.OriginDefault}
			// O site estático é servido pelo nginx da plataforma, na porta dela.
			if profile, _, sd := jsProfile(in); profile == domain.ProfileStatic {
				return RuleOutput{Proposals: []domain.Proposal{
					defaultHealth,
					{Field: domain.FieldPort, Value: domain.PlatformPort, Origin: domain.OriginPlatform, EvidenceIDs: evidenceIDs(sd.Evidence)},
				}}
			}
			refs := in.Named(kindEnvReference, "PORT")
			var runtime []domain.Evidence
			for _, e := range refs {
				if e.Scope == domain.ScopeRuntime {
					runtime = append(runtime, e)
				}
			}
			// No Bun, Bun.serve sem port e o export default do arquivo de
			// entrada leem PORT sozinhos: contrato do runtime (DEC-36 item 5).
			if manager, _ := jsManager(in); manager == domain.ManagerBun {
				runtime = append(runtime, in.Of(kindBunDefaultPort)...)
			}

			if len(runtime) > 0 {
				return RuleOutput{Proposals: []domain.Proposal{
					defaultHealth,
					{Field: domain.FieldPort, Value: domain.PlatformPort, Origin: domain.OriginPlatform, EvidenceIDs: evidenceIDs(runtime)},
				}}
			}

			// Sem ler PORT, um código que escuta numa porta escrita nele (`app.listen(8000)`)
			// declara a porta: não é um valor de reserva, é o único que o código usa (DEC-53 B).
			// Duas portas diferentes não se resolvem sozinhas.
			var msg string
			var cited []domain.Evidence
			listens := in.Of(kindListenPort)
			if distinct := distinctValues(listens); len(distinct) == 1 {
				return RuleOutput{Proposals: []domain.Proposal{
					defaultHealth,
					{Field: domain.FieldPort, Value: distinct[0], Origin: domain.OriginDeclaration, EvidenceIDs: evidenceIDs(listens)},
				}}
			} else if len(distinct) > 1 {
				msg = fmt.Sprintf("HEU-05: o código escuta em mais de uma porta escrita nele (%s); informe qual é a da aplicação", strings.Join(distinct, ", "))
				cited = listens
				return RuleOutput{
					Proposals: []domain.Proposal{defaultHealth},
					Pending:   []domain.Finding{finding(domain.FieldPort, msg, cited...)},
				}
			}
			switch {
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
			localDefault := map[string]domain.Evidence{}
			for _, e := range in.Of(kindEnvLocalhostDefault) {
				if _, ok := localDefault[e.Name]; !ok {
					localDefault[e.Name] = e
				}
			}
			var out RuleOutput
			for _, v := range byName {
				if v.Example == domain.ExampleDefault {
					v.DefaultValue = defaults[v.Name]
				}
				v.Hint = configHint(*v, hasExampleFile)
				if e, ok := localDefault[v.Name]; ok && v.Class != domain.ClassPlatform {
					v.Hint = &domain.ConfigHint{Code: domain.HintLocalhostDefault, Message: fmt.Sprintf(
						"O código usa %s quando esta variável falta (%s:%d). Sem valor no build, o site publicado chama esse endereço do navegador de quem o visita, e não o seu servidor. Defina a variável com o endereço público do backend.", e.Value, e.Source, e.Line)}
				}
				out.ConfigVariables = append(out.ConfigVariables, *v)
			}

			for _, e := range in.Of(kindEnvExposure) {
				out.Warnings = append(out.Warnings, exposureWarning(e))
			}
			// Nome de segredo com prefixo público: o valor vai para o
			// JavaScript do navegador, onde qualquer visitante o lê (DEC-15).
			var exposed []string
			for _, v := range byName {
				if v.Class == domain.ClassPublic && secretLikeName(v.Name) {
					exposed = append(exposed, v.Name)
				}
			}
			slices.Sort(exposed)
			for _, name := range exposed {
				out.Warnings = append(out.Warnings, finding("", fmt.Sprintf(
					"HEU-09: %s tem nome de segredo, mas o prefixo a torna pública: o valor vai para o JavaScript entregue ao navegador e qualquer visitante pode lê-lo. Use um valor que possa ser público, ou mova a chamada para um servidor", name)))
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
func startOptions(scripts []domain.Evidence, manager string) (options []domain.Option, omitted []string) {
	runner := "npm run "
	if manager == domain.ManagerBun {
		runner = "bun run "
	}
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
		opt := domain.Option{Value: runner + name, Kind: domain.OptionProduction}
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
	for _, segment := range commandSegments(command) {
		tokens := commandTokens(segment)
		if len(tokens) == 0 {
			continue
		}
		if slices.Contains(tokens, "--watch") {
			return "--watch"
		}
		if slices.Contains(tokens, "--hot") {
			return "--hot"
		}
		// -w é a forma curta de --watch no webpack, no tsc e no rollup.
		if slices.Contains([]string{"webpack", "tsc", "rollup"}, tokens[0]) && slices.Contains(tokens, "-w") {
			return tokens[0] + " -w"
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

// entryOptions oferece `bun run <arquivo>` para os campos module e main do
// package.json, que o bun init usa como entrada (DEC-36 item 4). É opção, não
// escolha: a HEU-02 não assume entrypoint.
func entryOptions(entries []domain.Evidence, existing []domain.Option) []domain.Option {
	var out []domain.Option
	seen := map[string]bool{}
	for _, o := range existing {
		seen[o.Value] = true
	}
	slices.SortFunc(entries, func(a, b domain.Evidence) int { return strings.Compare(a.Name, b.Name) })
	for _, e := range entries {
		value := "bun run " + e.Value
		if seen[value] || strings.ContainsAny(e.Value, " \t'\"$`;&|") {
			continue
		}
		seen[value] = true
		out = append(out, domain.Option{Value: value, Kind: domain.OptionProduction, Note: "arquivo do campo " + e.Name + " do package.json"})
	}
	return out
}

// HEU-03: no perfil estático, o diretório que o build gera (o que será
// servido) e se rotas desconhecidas devolvem index.html (DEC-37).
func ruleHEU03() Rule {
	return Rule{
		Meta: RuleMeta{
			ID: "HEU-03", Version: "0.1", State: RuleProposed,
			Requires: []string{"HEU-01"},
			Fields:   []string{domain.FieldOutputDir, domain.FieldSPAFallback},
		},
		Apply: func(in RuleInput) RuleOutput {
			profile, _, sd := jsProfile(in)
			if profile != domain.ProfileStatic {
				return RuleOutput{Warnings: ssrNotice(in)}
			}
			var out RuleOutput
			if sd.OutDir == "" {
				out.Pending = append(out.Pending, finding(domain.FieldOutputDir,
					"HEU-03: não foi possível saber o diretório que o build gera; informe-o (por exemplo dist)", sd.Evidence...))
			} else {
				out.Proposals = append(out.Proposals, domain.Proposal{Field: domain.FieldOutputDir, Value: sd.OutDir, Origin: sd.OutOrigin, EvidenceIDs: evidenceIDs(sd.OutEvidence)})
			}
			spa := domain.Proposal{Field: domain.FieldSPAFallback, Value: "false", Origin: domain.OriginDefault}
			if sd.Tool != "" {
				spa = domain.Proposal{Field: domain.FieldSPAFallback, Value: strconv.FormatBool(sd.SPA), Origin: domain.OriginInference, EvidenceIDs: evidenceIDs(sd.Evidence)}
			}
			out.Proposals = append(out.Proposals, spa)
			return out
		},
	}
}

// ssrNotice explica por que um build de ferramenta estática não virou site
// estático: o projeto renderiza no servidor e precisa de um start (DEC-37).
func ssrNotice(in RuleInput) []domain.Finding {
	build := in.Named(kindScript, "build")
	if len(build) == 0 {
		return nil
	}
	tool, _ := staticBuild(build[0].Value)
	if tool == "" {
		return nil
	}
	var cited []domain.Evidence
	cited = append(cited, in.Of(kindSSRMarker)...)
	for _, e := range in.Of(kindDependency) {
		if slices.Contains(ssrDeps, e.Name) {
			cited = append(cited, e)
		}
	}
	reason := ""
	switch {
	case len(cited) > 0:
		reason = fmt.Sprintf("o projeto renderiza no servidor (%s em %s)", cited[0].Name, cited[0].Source)
	case tool == "next":
		reason = "Next.js sem output: 'export' renderiza no servidor"
	default:
		return nil
	}
	return []domain.Finding{finding("", fmt.Sprintf(
		"HEU-03: %s; por isso não é publicado como site estático e precisa de um comando de execução de produção", reason), append(cited, build[0])...)}
}

// HEU-08: Dockerfile e compose que o repositório já traz. O Cobogó não os usa
// nem os executa: gera o próprio plano a partir dos manifestos, para poder
// explicar cada decisão e manter os limites e o isolamento (DEC-37). Roda
// mesmo quando o perfil não é suportado: é informação, não decisão.
func ruleHEU08() Rule {
	return Rule{
		Meta: RuleMeta{ID: "HEU-08", Version: "0.1", State: RuleProposed},
		Apply: func(in RuleInput) RuleOutput {
			artifacts := in.Of(kindDockerArtifact)
			if len(artifacts) == 0 {
				return RuleOutput{}
			}
			names := make([]string, 0, len(artifacts))
			for _, e := range artifacts {
				names = append(names, e.Source)
			}
			slices.Sort(names)
			return RuleOutput{Warnings: []domain.Finding{finding("", fmt.Sprintf(
				"HEU-08: o repositório traz %s. O Cobogó não o usa nem o executa: gera o próprio plano a partir do que lê no repositório, para explicar cada decisão. Um compose costuma descrever o ambiente de desenvolvimento, não a produção.",
				strings.Join(names, ", ")), artifacts...)}}
		},
	}
}

// distinctValues devolve os valores diferentes das evidências, em ordem.
func distinctValues(evidence []domain.Evidence) []string {
	var out []string
	for _, e := range evidence {
		if !slices.Contains(out, e.Value) {
			out = append(out, e.Value)
		}
	}
	slices.Sort(out)
	return out
}

// nodeFilePattern limita o arquivo de um `nodemon <arquivo>` a um caminho simples de
// JavaScript que o Node executa sozinho (nada de TypeScript, flags ou shell).
var nodeFilePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)

// productionEquivalent devolve o comando de produção óbvio de um comando de
// desenvolvimento: `nodemon server.js` é `node server.js` sem a recarga. Só o caso
// simples (a ferramenta e um arquivo, sem opções) é derivado; qualquer outra forma
// continua exigindo que a pessoa informe o comando (DEC-53 C).
func productionEquivalent(command string) (string, bool) {
	tokens := strings.Fields(command)
	if len(tokens) != 2 || tokens[0] != "nodemon" || !nodeFilePattern.MatchString(tokens[1]) {
		return "", false
	}
	switch strings.ToLower(path.Ext(tokens[1])) {
	case "", ".js", ".cjs", ".mjs":
		return "node " + tokens[1], true
	}
	return "", false
}

// scriptLabel transforma a fonte `package.json#scripts.start` em "scripts.start".
func scriptLabel(source string) string {
	if _, label, ok := strings.Cut(source, "#"); ok {
		return label
	}
	return source
}
