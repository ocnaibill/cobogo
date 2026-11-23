package analyzer

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Regras do catálogo Python (DEC-48, HEU-04). Uma regra só escreve os campos que
// lhe pertencem; o que não dá para concluir vira pergunta, nunca palpite.

const pythonCatalogVersion = "p-py-0.1"

// PythonCatalog é o catálogo do perfil P-PY.
func PythonCatalog() Catalog {
	return Catalog{
		Version: pythonCatalogVersion,
		Extractors: []Extractor{
			{ID: extractorPyManifest, Extract: extractPythonManifest},
			{ID: extractorEnvFiles, Extract: extractEnvFiles},
			{ID: extractorPySource, Extract: extractPythonSource},
		},
		Rules: []Rule{ruleHEU041(), ruleHEU042(), ruleHEU043(), ruleHEU044(), ruleHEU045(), ruleHEU046(), ruleHEU047(), ruleHEU048(), ruleHEU09For("HEU-04.1"), ruleHEU08()},
		Fields: []FieldSpec{
			{Name: domain.FieldProfile, Required: true, Overridable: false, Validate: func(v string) error { return nil }},
			{Name: domain.FieldPackageManager, Required: true, Overridable: true, Validate: validatePyManager},
			{Name: domain.FieldPythonVersion, Required: true, Overridable: true, Validate: validatePythonVersion},
			{Name: domain.FieldPythonInterface, Required: true, Overridable: true, Validate: validateInterface},
			// O alvo não é obrigatório para o resolvedor: com um Procfile, o comando dele
			// basta; sem Procfile, a HEU-04.3 pergunta quando não o acha.
			{Name: domain.FieldAppTarget, Overridable: true, Validate: validateAppTarget},
			{Name: domain.FieldPythonFramework, Overridable: true, Validate: validateFramework},
			{Name: domain.FieldDjangoSettings, Overridable: true, Validate: validateDottedModule},
			{Name: domain.FieldDjangoPlatformHosts, Overridable: true, Validate: validateBool},
			{Name: domain.FieldProcessWorker, Overridable: true, Validate: validateProcess},
			{Name: domain.FieldProcessScheduler, Overridable: true, Validate: validateProcess},
			{Name: domain.FieldReleaseCommand, Overridable: true, Validate: validateProcess},
			{Name: domain.FieldStartCommand, Overridable: true, Validate: validateCommand},
			{Name: domain.FieldBuildCommand, Overridable: true, Validate: validateCommand},
			{Name: domain.FieldExecutionMode, Overridable: true, Validate: validateExecutionMode},
			{Name: domain.FieldPort, Required: true, Overridable: true, Validate: validatePort},
			{Name: domain.FieldHealthPath, Overridable: true, Validate: validateHealthPath},
			{Name: domain.FieldEcosystem, Overridable: true, Validate: validateEcosystem},
		},
		Services:    detectPythonServiceNeeds,
		Persistence: suggestPythonPersistence,
	}
}

func validatePyManager(v string) error {
	if v != domain.ManagerPip && v != domain.ManagerUV {
		return fmt.Errorf("%q não é um gerenciador suportado (pip ou uv)", v)
	}
	return nil
}

func validatePythonVersion(v string) error {
	if !slices.Contains(domain.SupportedPythonVersions, v) {
		return fmt.Errorf("%q não é uma versão do catálogo (%s)", v, strings.Join(domain.SupportedPythonVersions, ", "))
	}
	return nil
}

func validateInterface(v string) error {
	if v != domain.InterfaceWSGI && v != domain.InterfaceASGI {
		return fmt.Errorf("%q não é uma interface (wsgi ou asgi)", v)
	}
	return nil
}

var appTargetPattern = regexp.MustCompile(`^[A-Za-z_][\w]*(\.[A-Za-z_][\w]*)*:[A-Za-z_][\w]*(\(\))?$`)

func validateAppTarget(v string) error {
	if !appTargetPattern.MatchString(v) {
		return fmt.Errorf("%q não é um alvo modulo:atributo (por exemplo app.main:app ou projeto.wsgi:application)", v)
	}
	return nil
}

var dottedModule = regexp.MustCompile(`^[A-Za-z_][\w]*(\.[A-Za-z_][\w]*)*$`)

func validateDottedModule(v string) error {
	if !dottedModule.MatchString(v) {
		return fmt.Errorf("%q não é um módulo Python (por exemplo projeto.settings.production)", v)
	}
	return nil
}

func validateFramework(v string) error {
	switch v {
	case domain.FrameworkDjango, domain.FrameworkFlask, domain.FrameworkFastAPI, domain.FrameworkStarlette, domain.FrameworkQuart, domain.FrameworkLitestar, domain.FrameworkFalcon, "sanic", domain.FrameworkOther:
		return nil
	}
	return fmt.Errorf("%q não é um framework reconhecido", v)
}

// interfaceOf diz a interface de um framework.
func interfaceOf(framework string) string {
	switch framework {
	case domain.FrameworkFlask, domain.FrameworkFalcon, domain.FrameworkDjango:
		return domain.InterfaceWSGI
	}
	return domain.InterfaceASGI
}

func hasDep(in RuleInput, names ...string) bool {
	for _, n := range names {
		if len(in.Named(kindPyDependency, n)) > 0 {
			return true
		}
	}
	return false
}

// HEU-04.1: o perfil e o gerenciador. pip (requirements.txt) e uv (uv.lock) na fase 1;
// Poetry, Pipenv, PDM e conda são ditos e recusados; dois gerenciadores suportados
// viram pergunta.
func ruleHEU041() Rule {
	return Rule{
		Meta: RuleMeta{ID: "HEU-04.1", Version: "0.1", State: RuleProposed, Fields: []string{domain.FieldProfile, domain.FieldPackageManager}},
		Apply: func(in RuleInput) RuleOutput {
			var out RuleOutput
			unsupported := func(msg string, ev ...domain.Evidence) {
				out.Unsupported = append(out.Unsupported, finding("", "HEU-04.1: "+msg, ev...))
			}
			for _, e := range in.Of(kindPySpecial) {
				switch e.Name {
				case "private_index":
					unsupported("o requirements usa um índice de pacotes com credencial; o Cobogó não guarda credencial de índice no build. Publique os pacotes num índice público ou vendorize-os no repositório", e)
				case "editable_outside":
					unsupported(fmt.Sprintf("a dependência %q aponta para fora do repositório; o build só enxerga o que está nele", e.Value), e)
				case "vcs":
					out.Warnings = append(out.Warnings, finding("", fmt.Sprintf("HEU-04.1: a dependência %q vem de um repositório Git; o build a baixa a cada deploy e ela não fica fixada como as do PyPI", e.Value), e))
				case "index":
					out.Warnings = append(out.Warnings, finding("", fmt.Sprintf("HEU-04.1: o requirements troca o índice de pacotes (%q); o build usa esse índice", e.Value), e))
				}
			}
			if len(out.Unsupported) > 0 {
				return out
			}
			pip := in.Named(kindPyManifest, "requirements.txt")
			uv := in.Named(kindPyLock, domain.ManagerUV)
			var later []domain.Evidence
			var laterNames []string
			for _, m := range []string{"poetry", "pipenv", "pdm", "conda"} {
				if e := in.Named(kindPyLock, m); len(e) > 0 {
					later = append(later, e[0])
					laterNames = append(laterNames, m)
				}
			}
			var manager string
			var ev []domain.Evidence
			switch {
			case len(pip) > 0 && len(uv) > 0:
				switch in.Overrides[domain.FieldPackageManager] {
				case domain.ManagerPip:
					manager, ev = domain.ManagerPip, pip
				case domain.ManagerUV:
					manager, ev = domain.ManagerUV, uv
				default:
					f := finding(domain.FieldPackageManager, "HEU-04.1: o repositório tem requirements.txt e uv.lock; escolha qual usar para instalar as dependências (o outro é ignorado)", pip[0], uv[0])
					f.Options = []domain.Option{
						{Value: domain.ManagerUV, Kind: domain.OptionProduction, Note: "instala exatamente o que está no uv.lock (uv sync --locked)"},
						{Value: domain.ManagerPip, Kind: domain.OptionProduction, Note: "instala o requirements.txt com o pip"},
					}
					out.Pending = append(out.Pending, f)
					return out
				}
			case len(uv) > 0:
				manager, ev = domain.ManagerUV, uv
			case len(pip) > 0:
				manager, ev = domain.ManagerPip, pip
			case len(later) > 0:
				unsupported(fmt.Sprintf("o projeto usa %s, que entra na fase 2 do suporte a Python; por enquanto o Cobogó instala com pip (requirements.txt) ou uv (uv.lock). Exporte um requirements.txt (por exemplo `poetry export -f requirements.txt`) ou use uv", strings.Join(laterNames, ", ")), later...)
				return out
			case len(in.Named(kindPyManifest, "pyproject.toml")) > 0 || len(in.Named(kindPyManifest, "setup.py")) > 0:
				unsupported("há pyproject.toml ou setup.py, mas nenhum requirements.txt nem uv.lock; instalar só pelo pyproject (sem lock) entra na fase 2. Gere um requirements.txt (`pip freeze`) ou um uv.lock (`uv lock`)")
				return out
			default:
				unsupported("nenhum arquivo de dependências (requirements.txt ou uv.lock) na raiz do projeto")
				return out
			}
			if len(later) > 0 {
				out.Warnings = append(out.Warnings, finding("", fmt.Sprintf("HEU-04.1: o projeto também tem arquivos de %s; o Cobogó usa %s e os ignora", strings.Join(laterNames, ", "), manager), later...))
			}
			for _, e := range in.Named(kindPyDependency, "gunicorn") {
				if domain.OldGunicorn(e.Value) {
					out.Warnings = append(out.Warnings, finding("", fmt.Sprintf("HEU-04.1: o gunicorn fixado (%s) é anterior à 22 e pode importar o pkg_resources, que o venv do Python 3.12+ não traz; o build instala o setuptools%s para ele funcionar. Prefira atualizar o gunicorn (a 22 corrigiu falhas de segurança de request smuggling)", strings.TrimSpace(e.Value), domain.LegacySetuptoolsSpec), e))
					break
				}
			}
			if manager == domain.ManagerPip {
				if loose := in.Of(kindPyUnpinned); len(loose) > 0 {
					names := make([]string, 0, len(loose))
					for _, e := range loose {
						names = append(names, e.Name)
					}
					slices.Sort(names)
					names = slices.Compact(names)
					shown := names
					if len(shown) > 6 {
						shown = append(shown[:6:6], fmt.Sprintf("e mais %d", len(names)-6))
					}
					out.Warnings = append(out.Warnings, finding("", fmt.Sprintf("HEU-04.1: dependências sem versão fixada (%s): o pip não tem lock, então um deploy futuro pode instalar outra versão. Fixe com == (pip freeze) ou use uv", strings.Join(shown, ", ")), loose[0]))
				}
			}
			out.Proposals = []domain.Proposal{
				{Field: domain.FieldProfile, Value: domain.ProfilePython, Origin: domain.OriginInference, EvidenceIDs: evidenceIDs(ev)},
				{Field: domain.FieldPackageManager, Value: manager, Origin: domain.OriginInference, EvidenceIDs: evidenceIDs(ev)},
			}
			return out
		},
	}
}

var (
	minorVersion = regexp.MustCompile(`(\d+)\.(\d+)`)
	specClause   = regexp.MustCompile(`^\s*(~=|===|==|!=|<=|>=|<|>)\s*(\d+)(?:\.(\d+))?(?:\.(\d+|\*))?`)
)

func minorOf(v string) (int, int, bool) {
	m := minorVersion.FindStringSubmatch(v)
	if m == nil {
		return 0, 0, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a, b, true
}

// satisfies diz se a versão X.Y atende a uma especificação como ">=3.10,<3.14".
// Compara no nível da versão menor, que é o que o catálogo escolhe: ">3.12" pede
// a 3.13 em diante, e "<3.12.4" ainda admite a 3.12.
func satisfies(version, spec string) bool {
	vMaj, vMin, _ := minorOf(version)
	v := vMaj*100 + vMin
	for _, raw := range strings.Split(spec, ",") {
		m := specClause.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		maj, _ := strconv.Atoi(m[2])
		mn, _ := strconv.Atoi(m[3])
		c := maj*100 + mn
		patch := m[4] != "" && m[4] != "*" && m[4] != "0"
		ok := true
		switch m[1] {
		case ">=":
			ok = v >= c
		case ">":
			ok = v > c || (patch && v == c)
		case "<=":
			ok = v <= c
		case "<":
			ok = v < c || (patch && v == c)
		case "==", "===":
			ok = v == c || (m[3] == "" && vMaj == maj)
		case "~=":
			if m[4] != "" {
				ok = v == c
			} else {
				ok = vMaj == maj && v >= c
			}
		case "!=":
			ok = m[4] != "" && m[4] != "*" || v != c
		}
		if !ok {
			return false
		}
	}
	return true
}

// HEU-04.2: a versão do Python, da declaração mais explícita (.python-version,
// runtime.txt) para a mais fraca (requires-python). Fora do catálogo, a mais próxima,
// com aviso; a 3.10, com o aviso de fim de suporte.
func ruleHEU042() Rule {
	return Rule{
		Meta: RuleMeta{ID: "HEU-04.2", Version: "0.1", State: RuleProposed, Requires: []string{"HEU-04.1"}, Fields: []string{domain.FieldPythonVersion}},
		Apply: func(in RuleInput) RuleOutput {
			var out RuleOutput
			propose := func(v string, origin domain.Origin, ev ...domain.Evidence) {
				out.Proposals = append(out.Proposals, domain.Proposal{Field: domain.FieldPythonVersion, Value: v, Origin: origin, EvidenceIDs: evidenceIDs(ev)})
				chosen := v
				if o := in.Overrides[domain.FieldPythonVersion]; o != "" {
					chosen = o
				}
				if when, eol := domain.UnsupportedUpstreamPython[chosen]; eol {
					out.Warnings = append(out.Warnings, finding("", fmt.Sprintf("HEU-04.2: o Python %s saiu de suporte em %s e não recebe mais correções de segurança; prefira %s", chosen, when, domain.DefaultPythonVersion), ev...))
				}
			}
			nearest := func(maj, mn int) string {
				first, last := domain.SupportedPythonVersions[0], domain.SupportedPythonVersions[len(domain.SupportedPythonVersions)-1]
				fMaj, fMin, _ := minorOf(first)
				if maj*100+mn < fMaj*100+fMin {
					return first
				}
				return last
			}
			for _, source := range []string{".python-version", "runtime.txt"} {
				for _, e := range in.Named(kindPyVersion, source) {
					maj, mn, ok := minorOf(e.Value)
					if !ok {
						continue
					}
					v := fmt.Sprintf("%d.%d", maj, mn)
					if slices.Contains(domain.SupportedPythonVersions, v) {
						propose(v, domain.OriginDeclaration, e)
						return out
					}
					alt := nearest(maj, mn)
					out.Warnings = append(out.Warnings, finding("", fmt.Sprintf("HEU-04.2: %s pede o Python %s, fora do catálogo (%s); o Cobogó usa o %s", source, v, strings.Join(domain.SupportedPythonVersions, ", "), alt), e))
					propose(alt, domain.OriginInference, e)
					return out
				}
			}
			for _, e := range in.Named(kindPyVersion, "requires-python") {
				if satisfies(domain.DefaultPythonVersion, e.Value) {
					propose(domain.DefaultPythonVersion, domain.OriginInference, e)
					return out
				}
				for i := len(domain.SupportedPythonVersions) - 1; i >= 0; i-- {
					if v := domain.SupportedPythonVersions[i]; satisfies(v, e.Value) {
						propose(v, domain.OriginInference, e)
						return out
					}
				}
				out.Warnings = append(out.Warnings, finding("", fmt.Sprintf("HEU-04.2: requires-python (%s) não admite nenhuma versão do catálogo; o Cobogó usa o %s", e.Value, domain.DefaultPythonVersion), e))
			}
			propose(domain.DefaultPythonVersion, domain.OriginDefault)
			return out
		},
	}
}

var (
	devCommand   = regexp.MustCompile(`\brunserver\b|\bflask\s+run\b|--reload\b|\bfastapi\s+dev\b|\bdebug=True`)
	procTarget   = regexp.MustCompile(`(?:^|\s)([A-Za-z_][\w.]*:[A-Za-z_]\w*(?:\(\))?)(?:\s|$)`)
	portFlag     = regexp.MustCompile(`(?:--port[= ]|--bind[= ]\S*:|-b\s+\S*:|-p\s+)(\d{2,5})\b`)
	usesPortVar  = regexp.MustCompile(`\$\{?PORT\b`)
	asgiServerRe = regexp.MustCompile(`\b(uvicorn|hypercorn|daphne|granian)\b|-k\s+uvicorn`)
)

// HEU-04.3: o alvo, a interface e o framework, sem importar nada. Ordem: Procfile,
// Django (settings do manage.py e WSGI_APPLICATION), atribuição de módulo, fábrica.
func ruleHEU043() Rule {
	return Rule{
		Meta: RuleMeta{
			ID: "HEU-04.3", Version: "0.1", State: RuleProposed, Requires: []string{"HEU-04.1"},
			Fields: []string{domain.FieldAppTarget, domain.FieldPythonInterface, domain.FieldPythonFramework, domain.FieldDjangoSettings},
		},
		Apply: func(in RuleInput) RuleOutput {
			var out RuleOutput
			propose := func(field, value string, origin domain.Origin, ev ...domain.Evidence) {
				out.Proposals = append(out.Proposals, domain.Proposal{Field: field, Value: value, Origin: origin, EvidenceIDs: evidenceIDs(ev)})
			}
			web := in.Named(kindPyProcfile, "web")

			// Só processos que não são web: worker, bot, agendador.
			if len(web) == 0 {
				if procs := in.Of(kindPyProcfile); len(procs) > 0 && len(in.Of(kindPyApp)) == 0 && len(in.Of(kindDjangoModule)) == 0 {
					out.Unsupported = append(out.Unsupported, finding("", "HEU-04.3: o Procfile só declara processos que não são web ("+procs[0].Name+"); o Cobogó publica um serviço web por projeto, e workers e bots ainda não são suportados", procs...))
					return out
				}
			}

			// Django: manage.py com o settings padrão e Django nas dependências.
			if mods := in.Of(kindDjangoModule); len(mods) > 0 && hasDep(in, "django") {
				propose(domain.FieldPythonFramework, domain.FrameworkDjango, domain.OriginInference, mods[0])
				settings := mods[0].Value
				// Um settings de desenvolvimento como padrão e um de produção ao lado: pergunta.
				if isDevSettings(settings) && in.Overrides[domain.FieldDjangoSettings] == "" {
					var prod []domain.Option
					for _, c := range in.Of(kindDjangoCand) {
						if isProdSettings(c.Value) {
							prod = append(prod, domain.Option{Value: c.Value, Kind: domain.OptionProduction, Note: "settings de produção"})
						}
					}
					if len(prod) > 0 {
						f := finding(domain.FieldDjangoSettings, fmt.Sprintf("HEU-04.3: o manage.py usa %s, que parece de desenvolvimento, e há settings de produção; escolha qual o Cobogó usa (vai em DJANGO_SETTINGS_MODULE)", settings), mods[0])
						f.Options = append(prod, domain.Option{Value: settings, Kind: domain.OptionDevelopment, Note: "o padrão do manage.py"})
						out.Pending = append(out.Pending, f)
					}
				}
				propose(domain.FieldDjangoSettings, settings, domain.OriginDeclaration, mods[0])
				apps := in.Of(kindDjangoApp)
				asgi := hasDep(in, "channels", "daphne")
				var chosen *domain.Evidence
				for i := range apps {
					if apps[i].Name == domain.InterfaceWSGI && chosen == nil {
						chosen = &apps[i]
					}
					if apps[i].Name == domain.InterfaceASGI && asgi {
						chosen = &apps[i]
						break
					}
				}
				if chosen == nil {
					f := finding(domain.FieldAppTarget, "HEU-04.3: o settings do Django não tem WSGI_APPLICATION literal; informe o alvo (por exemplo projeto.wsgi:application)", mods[0])
					out.Pending = append(out.Pending, f)
					propose(domain.FieldPythonInterface, domain.InterfaceWSGI, domain.OriginDefault)
					return out
				}
				propose(domain.FieldAppTarget, chosen.Value, domain.OriginDeclaration, *chosen)
				propose(domain.FieldPythonInterface, chosen.Name, domain.OriginInference, *chosen)
				return out
			}

			// Procfile web com um alvo legível (gunicorn app:app, uvicorn main:app).
			if len(web) > 0 {
				if m := procTarget.FindStringSubmatch(web[0].Value); m != nil {
					propose(domain.FieldAppTarget, m[1], domain.OriginDeclaration, web[0])
				} else if apps := in.Of(kindPyApp); len(apps) == 1 {
					// Só informativo, a menos que o Procfile seja de desenvolvimento: aí o
					// servidor de produção do perfil inicia este alvo (HEU-04.4).
					propose(domain.FieldAppTarget, apps[0].Value, domain.OriginInference, apps[0])
				} else if devCommand.MatchString(web[0].Value) && in.Overrides[domain.FieldExecutionMode] != domain.ModeDevelopment {
					out.Pending = append(out.Pending, finding(domain.FieldAppTarget, "HEU-04.3: o Procfile inicia um servidor de desenvolvimento e não achei o alvo da aplicação para o servidor de produção; informe-o (modulo:atributo) ou escolha o modo development", web[0]))
				}
				iface := domain.InterfaceWSGI
				if asgiServerRe.MatchString(web[0].Value) {
					iface = domain.InterfaceASGI
				}
				propose(domain.FieldPythonInterface, iface, domain.OriginDeclaration, web[0])
				if fw := frameworkFromApps(in); fw != "" {
					propose(domain.FieldPythonFramework, fw, domain.OriginInference)
				}
				return out
			}

			// Atribuições de módulo: um alvo só, pronto; mais de um, pergunta.
			targets := map[string]domain.Evidence{}
			var order []string
			for _, e := range in.Of(kindPyApp) {
				if _, ok := targets[e.Value]; !ok {
					order = append(order, e.Value)
				}
				targets[e.Value] = e
			}
			if len(order) == 0 {
				for _, e := range in.Of(kindPyFactory) {
					if _, ok := targets[e.Value]; !ok {
						order = append(order, e.Value)
					}
					targets[e.Value] = e
				}
			}
			slices.Sort(order)
			switch {
			case len(order) == 1:
				e := targets[order[0]]
				fw := strings.TrimSuffix(e.Name, "-asgi")
				propose(domain.FieldAppTarget, e.Value, domain.OriginInference, e)
				propose(domain.FieldPythonFramework, fw, domain.OriginInference, e)
				iface := interfaceOf(fw)
				if strings.HasSuffix(e.Name, "-asgi") {
					iface = domain.InterfaceASGI
				}
				propose(domain.FieldPythonInterface, iface, domain.OriginInference, e)
			case len(order) > 1:
				var ev []domain.Evidence
				var opts []domain.Option
				for _, t := range order {
					e := targets[t]
					ev = append(ev, e)
					opts = append(opts, domain.Option{Value: t, Note: fmt.Sprintf("%s em %s:%d", strings.TrimSuffix(e.Name, "-asgi"), e.Source, e.Line)})
				}
				f := finding(domain.FieldAppTarget, fmt.Sprintf("HEU-04.3: encontrei %d aplicações; escolha qual iniciar", len(order)), ev...)
				f.Options = opts
				out.Pending = append(out.Pending, f)
				fw := strings.TrimSuffix(targets[order[0]].Name, "-asgi")
				if t := in.Overrides[domain.FieldAppTarget]; t != "" {
					if e, ok := targets[t]; ok {
						fw = strings.TrimSuffix(e.Name, "-asgi")
					}
				}
				propose(domain.FieldPythonFramework, fw, domain.OriginInference)
				propose(domain.FieldPythonInterface, interfaceOf(fw), domain.OriginInference)
			default:
				msg := "HEU-04.3: não encontrei uma aplicação web (uma linha como `app = Flask(__name__)` ou `app = FastAPI()` num arquivo que importa o framework, um Django com manage.py, ou um Procfile com web:). Se for uma aplicação web, informe o alvo (modulo:atributo); se for um script, bot ou worker, ainda não é suportado"
				f := finding(domain.FieldAppTarget, msg)
				for _, e := range in.Of(kindPyRunTarget) {
					f.Options = append(f.Options, domain.Option{Value: e.Value, Note: "alvo citado em uvicorn.run em " + e.Source})
				}
				out.Pending = append(out.Pending, f)
				propose(domain.FieldPythonInterface, domain.InterfaceASGI, domain.OriginDefault)
			}
			return out
		},
	}
}

var (
	procServer = regexp.MustCompile(`^(?:[A-Za-z_]\w*=\S*\s+)*(?:python -m\s+)?(gunicorn|uvicorn)\b`)
	fastapiRun = regexp.MustCompile(`^(?:[A-Za-z_]\w*=\S*\s+)*fastapi\s+run\b`)
)

// readsPort diz se o código de runtime lê a variável PORT.
func readsPort(in RuleInput) bool {
	for _, e := range in.Named(kindEnvReference, "PORT") {
		if e.Scope == domain.ScopeRuntime {
			return true
		}
	}
	return false
}

func frameworkFromApps(in RuleInput) string {
	for _, e := range in.Of(kindPyApp) {
		return strings.TrimSuffix(e.Name, "-asgi")
	}
	return ""
}

func isDevSettings(module string) bool {
	last := module[strings.LastIndex(module, ".")+1:]
	return slices.Contains([]string{"dev", "development", "local", "debug"}, last)
}

func isTestSettings(module string) bool {
	last := module[strings.LastIndex(module, ".")+1:]
	return slices.Contains([]string{"test", "tests", "testing", "ci"}, last)
}

// settingsModuleOf converte o arquivo de um settings no módulo dele.
func settingsModuleOf(file string) string {
	mod := strings.TrimSuffix(strings.TrimPrefix(file, "src/"), ".py")
	mod = strings.TrimSuffix(mod, "/__init__")
	return strings.ReplaceAll(mod, "/", ".")
}

// effectiveSettings é o settings que vai rodar: o escolhido, o padrão do manage.py
// ou, enquanto a pergunta entre desenvolvimento e produção está aberta, nenhum.
func effectiveSettings(in RuleInput) string {
	if o := in.Overrides[domain.FieldDjangoSettings]; o != "" {
		return o
	}
	mods := in.Of(kindDjangoModule)
	if len(mods) == 0 {
		return ""
	}
	if isDevSettings(mods[0].Value) {
		for _, c := range in.Of(kindDjangoCand) {
			if isProdSettings(c.Value) {
				return ""
			}
		}
	}
	return mods[0].Value
}

func isProdSettings(module string) bool {
	last := module[strings.LastIndex(module, ".")+1:]
	return slices.Contains([]string{"prod", "production", "deploy", "live"}, last)
}

// HEU-04.4: o comando, a porta, o modo e o build. Sem Procfile, o comando é o do
// servidor de produção que a plataforma monta (no planner) e a porta é a da
// plataforma; com Procfile, valem o comando e a porta dele, e um servidor de
// desenvolvimento vira pergunta.
func ruleHEU044() Rule {
	return Rule{
		Meta: RuleMeta{
			ID: "HEU-04.4", Version: "0.1", State: RuleProposed, Requires: []string{"HEU-04.1"},
			Fields: []string{domain.FieldStartCommand, domain.FieldPort, domain.FieldExecutionMode, domain.FieldBuildCommand, domain.FieldHealthPath},
		},
		Apply: func(in RuleInput) RuleOutput {
			out := RuleOutput{Proposals: []domain.Proposal{{Field: domain.FieldExecutionMode, Value: domain.ModeProduction, Origin: domain.OriginDefault}}}
			propose := func(field, value string, origin domain.Origin, ev ...domain.Evidence) {
				out.Proposals = append(out.Proposals, domain.Proposal{Field: field, Value: value, Origin: origin, EvidenceIDs: evidenceIDs(ev)})
			}
			web := in.Named(kindPyProcfile, "web")
			// Os outros processos do Procfile não rodam: um processo por projeto.
			if len(web) > 0 {
				for _, p := range in.Of(kindPyProcfile) {
					switch p.Name {
					case "web":
					case "release":
						// A fase release roda a cada deploy (HEU-04.8).
					default:
						// worker e agendador viram processos do projeto (HEU-04.7); o resto não roda.
						if processKindOf(p.Name) == "" {
							out.Warnings = append(out.Warnings, finding("", fmt.Sprintf("HEU-04.4: o processo %q do Procfile não é um worker nem um agendador e não roda: o Cobogó roda o web e, do mesmo código, um worker e um agendador. Para outro serviço, crie outro projeto com o mesmo repositório", p.Name), p))
						}
					}
				}
			}
			if len(web) > 0 {
				cmd := web[0].Value
				if devCommand.MatchString(cmd) && in.Overrides[domain.FieldExecutionMode] != domain.ModeDevelopment {
					// Em produção, o comando de desenvolvimento do Procfile é trocado pelo servidor
					// de produção que a plataforma monta; em desenvolvimento, vale o do Procfile.
					out.Warnings = append(out.Warnings, finding("", fmt.Sprintf("HEU-04.4: o Procfile inicia um servidor de desenvolvimento (%q); em produção o Cobogó usa o servidor de produção do perfil (gunicorn ou uvicorn) com o mesmo alvo. Para manter o do Procfile, escolha o modo de execução development", cmd), web[0]))
					propose(domain.FieldPort, domain.PlatformPort, domain.OriginPlatform)
				} else {
					propose(domain.FieldStartCommand, cmd, domain.OriginDeclaration, web[0])
					switch {
					case usesPortVar.MatchString(cmd):
						propose(domain.FieldPort, domain.PlatformPort, domain.OriginPlatform, web[0])
					case portFlag.MatchString(cmd):
						propose(domain.FieldPort, portFlag.FindStringSubmatch(cmd)[1], domain.OriginDeclaration, web[0])
					case procServer.MatchString(cmd):
						// O gunicorn sem --bind ouve na PORT do ambiente; o uvicorn lê
						// UVICORN_HOST e UVICORN_PORT, que a imagem define (planner).
						propose(domain.FieldPort, domain.PlatformPort, domain.OriginPlatform, web[0])
					case readsPort(in):
						propose(domain.FieldPort, domain.PlatformPort, domain.OriginPlatform, in.Named(kindEnvReference, "PORT")[0])
					case fastapiRun.MatchString(cmd):
						propose(domain.FieldPort, "8000", domain.OriginDefault, web[0])
					default:
						out.Pending = append(out.Pending, finding(domain.FieldPort, "HEU-04.4: o comando do Procfile não diz a porta e o código não lê PORT; informe em que porta a aplicação ouve (ou use --bind 0.0.0.0:$PORT no Procfile)", web[0]))
					}
				}
			} else {
				propose(domain.FieldPort, domain.PlatformPort, domain.OriginPlatform)
			}
			// O servidor de produção que a plataforma inicia (DEC-48, decisão 2): se o
			// projeto não o traz, ela o instala numa versão fixada e diz isso.
			platformServer := len(web) == 0 || (devCommand.MatchString(web[0].Value) && in.Overrides[domain.FieldExecutionMode] != domain.ModeDevelopment)
			if platformServer && !hasDep(in, "gunicorn", "uvicorn") {
				out.Warnings = append(out.Warnings, finding("", fmt.Sprintf("HEU-04.4: o projeto não traz um servidor de produção nas dependências; a plataforma instala o gunicorn %s (WSGI) ou o uvicorn %s (ASGI), conforme a aplicação, e o inicia na porta da plataforma", domain.GunicornVersion, domain.UvicornVersion)))
			}
			// Django com STATIC_ROOT: os estáticos são coletados no build.
			if len(in.Of(kindDjangoModule)) > 0 && hasDep(in, "django") {
				if sr := in.Named(kindDjangoSet, "STATIC_ROOT"); len(sr) > 0 {
					propose(domain.FieldBuildCommand, "python manage.py collectstatic --noinput", domain.OriginInference, sr[0])
				}
			}
			return out
		},
	}
}

// HEU-04.5: o contrato de produção do Django (DEC-48 item 9). Só avisa: nunca muda o
// código do projeto.
func ruleHEU045() Rule {
	return Rule{
		Meta: RuleMeta{ID: "HEU-04.5", Version: "0.1", State: RuleProposed, Requires: []string{"HEU-04.1"}},
		Apply: func(in RuleInput) RuleOutput {
			var out RuleOutput
			if len(in.Of(kindDjangoModule)) == 0 || !hasDep(in, "django") {
				return out
			}
			warn := func(msg string, ev ...domain.Evidence) {
				out.Warnings = append(out.Warnings, finding("", "HEU-04.5: "+msg, ev...))
			}
			// Os avisos valem para o settings que vai rodar: o de desenvolvimento só conta
			// quando é ele o escolhido.
			setting := djangoSetting(in)
			if e := setting("ALLOWED_HOSTS_LITERAL"); len(e) > 0 {
				hosts := e[0].Value
				if ext := setting("ALLOWED_HOSTS_EXTENDED"); len(ext) > 0 && len(setting("ALLOWED_HOSTS_ENV")) == 0 {
					warn("ALLOWED_HOSTS começa fixo no código e é completado depois; confira se o domínio do projeto entra na lista (o Django responde 400 a host fora dela)", ext[0])
				} else if !strings.Contains(hosts, "*") {
					warn(fmt.Sprintf("ALLOWED_HOSTS está fixo no código (%s) e não inclui o domínio do projeto: o Django responde 400 a quem o acessar pelo endereço público. Leia a lista de uma variável de ambiente (por exemplo ALLOWED_HOSTS) e defina-a com o domínio na aba Variáveis", orDash(hosts)), e[0])
				}
			}
			if e := setting("ALLOWED_HOSTS_ENV"); len(e) > 0 {
				warn(fmt.Sprintf("ALLOWED_HOSTS vem da variável %s: defina-a com o domínio do projeto na aba Variáveis (sem ele, o Django responde 400 pelo endereço público)", e[0].Value), e[0])
			}
			if e := setting("CSRF_TRUSTED_ORIGINS_LITERAL"); len(e) > 0 {
				warn(fmt.Sprintf("CSRF_TRUSTED_ORIGINS está fixo no código (%s) e não inclui o endereço do projeto: o login do admin e os formulários podem ser recusados com 403 (CSRF). Leia a lista de uma variável de ambiente e defina-a com a URL do projeto na aba Variáveis", orDash(e[0].Value)), e[0])
			}
			if len(setting("CSRF_TRUSTED_ORIGINS")) == 0 {
				warn("o settings não define CSRF_TRUSTED_ORIGINS: atrás do proxy com HTTPS, o Django pode recusar formulários (o login do admin) com 403 CSRF. Defina-o com https://<domínio do projeto>, de preferência por variável")
			}
			if e := setting("DEBUG_TRUE"); len(e) > 0 {
				warn("DEBUG = True fixo no código: em produção o Django mostra o código e as configurações a quem provocar um erro. Leia DEBUG de uma variável", e[0])
			}
			if e := setting("SECRET_KEY_LITERAL"); len(e) > 0 {
				warn("a SECRET_KEY está escrita no código (e no repositório): leia-a de uma variável e gere uma nova na aba Variáveis", e[0])
			}
			if e := setting("SECRET_KEY_DEFAULT"); len(e) > 0 {
				warn(fmt.Sprintf("a SECRET_KEY tem um valor padrão no código, que vale se a variável %s não for definida: a chave do repositório é pública e qualquer pessoa pode forjar sessões e tokens com ela. Defina %s na aba Variáveis (o Cobogó oferece gerar uma)", e[0].Value, e[0].Value), e[0])
			}
			if e := setting("MEDIA_URLS_STATIC"); len(e) > 0 && len(setting("MEDIA_ROOT")) > 0 && len(setting("DEBUG_TRUE")) == 0 {
				warn("os arquivos enviados (MEDIA_ROOT) são servidos só por static(MEDIA_URL…) nas URLs, que não faz nada com DEBUG desligado: em produção, imagens e PDFs em /media/ respondem 404 mesmo com o arquivo no disco. Sirva-os por outro meio (armazenamento de objetos ou um servidor de arquivos)", e[0])
			}
			sr := setting("STATIC_ROOT")
			switch {
			case len(sr) == 0:
				warn("o settings não define STATIC_ROOT: os estáticos (CSS do admin, por exemplo) não são coletados e não aparecem em produção")
			case len(setting("WHITENOISE")) == 0 && !hasDep(in, "whitenoise"):
				warn("os estáticos são coletados, mas nada os serve: com DEBUG desligado o Django não serve estáticos. Adicione o whitenoise (dependência e middleware)", sr[0])
			}
			if len(setting("SQLITE")) > 0 {
				warn("o settings usa SQLite: o arquivo some a cada deploy se não estiver num volume (aba Dados), e o padrão (db.sqlite3 na raiz) fica na pasta da aplicação, que não pode ser volume. Mova o arquivo para uma pasta (por exemplo data/) ou use o PostgreSQL do Cobogó (aba Banco)")
			}
			// Com a fase de release ativa (HEU-04.8), o Cobogó roda as migrações a cada deploy:
			// o aviso só vale quando ela não vai rodar.
			if !releaseWillRun(in) {
				warn("nenhum comando de migração roda nos deploys deste projeto: depois do primeiro deploy (e de cada um que traga migrações), rode `python manage.py migrate` na aba Comandos")
			}
			return out
		},
	}
}

// djangoSetting devolve a leitura de uma configuração do Django só nos settings que
// vão rodar: um de desenvolvimento ou de teste só conta quando é o escolhido.
func djangoSetting(in RuleInput) func(string) []domain.Evidence {
	effective := effectiveSettings(in)
	return func(name string) []domain.Evidence {
		var out []domain.Evidence
		for _, e := range in.Named(kindDjangoSet, name) {
			mod := settingsModuleOf(e.Source)
			if (isDevSettings(mod) || isTestSettings(mod)) && mod != effective {
				continue
			}
			out = append(out, e)
		}
		return out
	}
}

// HEU-04.6: o ajuste do Django feito pela plataforma (DEC-49 item 6). Quando o settings
// fixa ALLOWED_HOSTS ou CSRF_TRUSTED_ORIGINS no código, sem o endereço do projeto, a
// análise pergunta se a plataforma deve acrescentá-lo, na imagem e sem mudar o repositório.
// Nunca por padrão: a resposta é da pessoa.
func ruleHEU046() Rule {
	return Rule{
		Meta: RuleMeta{ID: "HEU-04.6", Version: "0.1", State: RuleProposed, Requires: []string{"HEU-04.1"}, Fields: []string{domain.FieldDjangoPlatformHosts}},
		Apply: func(in RuleInput) RuleOutput {
			var out RuleOutput
			if len(in.Of(kindDjangoModule)) == 0 || !hasDep(in, "django") {
				return out
			}
			// Com a escolha do settings ainda aberta (desenvolvimento ou produção), a pergunta
			// espera: ela depende de qual settings vai rodar.
			if effectiveSettings(in) == "" {
				return out
			}
			setting := djangoSetting(in)
			var ev []domain.Evidence
			fixedHosts := len(setting("ALLOWED_HOSTS_LITERAL")) > 0 && len(setting("ALLOWED_HOSTS_ENV")) == 0 && len(setting("ALLOWED_HOSTS_EXTENDED")) == 0
			if fixedHosts {
				e := setting("ALLOWED_HOSTS_LITERAL")[0]
				if strings.Contains(e.Value, "*") {
					fixedHosts = false
				} else {
					ev = append(ev, e)
				}
			}
			fixedOrigins := len(setting("CSRF_TRUSTED_ORIGINS_LITERAL")) > 0
			if fixedOrigins {
				ev = append(ev, setting("CSRF_TRUSTED_ORIGINS_LITERAL")[0])
			}
			if !fixedHosts && !fixedOrigins {
				return out
			}
			if _, answered := in.Overrides[domain.FieldDjangoPlatformHosts]; answered {
				return out
			}
			f := finding(domain.FieldDjangoPlatformHosts, "HEU-04.6: o settings fixa ALLOWED_HOSTS ou CSRF_TRUSTED_ORIGINS no código, sem o endereço deste projeto: o Django responderá 400 e recusará formulários. Quer que a plataforma acrescente o endereço do projeto a essas listas, num módulo gerado na imagem e sem mudar o repositório? Nada que o projeto definiu é removido.", ev...)
			f.Options = []domain.Option{
				{Value: "false", Note: "não: o projeto fica como está (conserte o settings para ler os valores de variáveis de ambiente)"},
				{Value: "true", Note: "sim: a imagem usa um módulo de ajuste que importa o settings do projeto e acrescenta o endereço público"},
			}
			out.Pending = append(out.Pending, f)
			return out
		},
	}
}

// processKindOf diz que processo do Procfile um nome é: worker, agendador ou nenhum dos dois.
func processKindOf(name string) string {
	n := strings.ToLower(name)
	switch {
	case n == "beat" || n == "clock" || n == "scheduler" || n == "celerybeat" || n == "celery-beat" || strings.HasSuffix(n, "beat"):
		return domain.ProcessScheduler
	case n == "worker" || n == "workers" || n == "celery" || strings.HasSuffix(n, "worker"):
		return domain.ProcessWorker
	}
	return ""
}

func validateProcess(v string) error {
	if v == domain.ProcessNone {
		return nil
	}
	return validateCommand(v)
}

// HEU-04.7: os processos além do web (DEC-51): o worker e o agendador, do Procfile (declaração) ou do
// Celery (inferência, com o arquivo e a linha do app). São propostas com evidência: o override com o
// comando troca, e "none" desliga. O Celery sem sinal de broker ganha um aviso.
func ruleHEU047() Rule {
	return Rule{
		Meta: RuleMeta{ID: "HEU-04.7", Version: "0.1", State: RuleProposed, Requires: []string{"HEU-04.1"}, Fields: []string{domain.FieldProcessWorker, domain.FieldProcessScheduler}},
		Apply: func(in RuleInput) RuleOutput {
			var out RuleOutput
			propose := func(field, value string, origin domain.Origin, ev ...domain.Evidence) {
				out.Proposals = append(out.Proposals, domain.Proposal{Field: field, Value: value, Origin: origin, EvidenceIDs: evidenceIDs(ev)})
			}
			haveWorker, haveScheduler := false, false
			for _, p := range in.Of(kindPyProcfile) {
				switch processKindOf(p.Name) {
				case domain.ProcessWorker:
					if !haveWorker {
						propose(domain.FieldProcessWorker, p.Value, domain.OriginDeclaration, p)
						haveWorker = true
					}
				case domain.ProcessScheduler:
					if !haveScheduler {
						propose(domain.FieldProcessScheduler, p.Value, domain.OriginDeclaration, p)
						haveScheduler = true
					}
				}
			}
			var celeryWorker, celeryBeat []domain.Evidence
			for _, e := range in.Of(kindPyCelery) {
				switch e.Name {
				case "worker":
					celeryWorker = append(celeryWorker, e)
				case "beat", "beat_setting":
					celeryBeat = append(celeryBeat, e)
				}
			}
			if !hasDep(in, "celery") || len(celeryWorker) == 0 {
				return out
			}
			app := celeryWorker[0]
			if !haveWorker {
				propose(domain.FieldProcessWorker, "celery -A "+shellQuoteArg(app.Value)+" worker -l info --concurrency=2", domain.OriginInference, app)
			}
			if !haveScheduler && len(celeryBeat) > 0 {
				cmd := "celery -A " + shellQuoteArg(app.Value) + " beat -l info"
				if hasDep(in, "django-celery-beat") {
					cmd += " --scheduler django_celery_beat.schedulers:DatabaseScheduler"
				}
				propose(domain.FieldProcessScheduler, cmd, domain.OriginInference, celeryBeat[0])
			}
			if len(in.Of(kindPyRedis)) == 0 && !hasDep(in, "redis", "django-redis") {
				out.Warnings = append(out.Warnings, finding("", "HEU-04.7: este projeto usa Celery, mas não achei um sinal de broker (Redis); o worker sobe e não recebe tarefas. Se o broker é outro (RabbitMQ, SQS), o Cobogó ainda não o provê: informe um externo numa variável de ambiente lida pelo código", app))
			}
			return out
		},
	}
}

// HEU-04.8: a fase de release (DEC-52 item 4), que roda uma vez por deploy antes de a
// aplicação nova subir. Declarada pelo `release:` do Procfile, ou, num Django com
// migrações no repositório, `python manage.py migrate --noinput` (inferência, com a
// primeira migração achada como evidência). O override troca o comando, e "none" desliga.
// Rollback da imagem não desfaz uma migração (RN-13).
func ruleHEU048() Rule {
	return Rule{
		Meta: RuleMeta{ID: "HEU-04.8", Version: "0.1", State: RuleProposed, Requires: []string{"HEU-04.1"}, Fields: []string{domain.FieldReleaseCommand}},
		Apply: func(in RuleInput) RuleOutput {
			var out RuleOutput
			propose := func(value string, origin domain.Origin, ev ...domain.Evidence) {
				out.Proposals = append(out.Proposals, domain.Proposal{Field: domain.FieldReleaseCommand, Value: value, Origin: origin, EvidenceIDs: evidenceIDs(ev)})
			}
			if rel := in.Named(kindPyProcfile, "release"); len(rel) > 0 {
				propose(rel[0].Value, domain.OriginDeclaration, rel[0])
				return out
			}
			mods, migs := in.Of(kindDjangoModule), in.Of(kindDjangoMigration)
			if len(mods) > 0 && len(migs) > 0 {
				propose("python manage.py migrate --noinput", domain.OriginInference, mods[0], migs[0])
			}
			return out
		},
	}
}

// releaseWillRun diz se a HEU-04.8 vai ter um comando de release: o override (que não seja
// "none"), o `release:` do Procfile ou o `migrate` inferido de um Django com migrações.
func releaseWillRun(in RuleInput) bool {
	if o, ok := in.Overrides[domain.FieldReleaseCommand]; ok {
		return o != "" && o != domain.ProcessNone
	}
	if len(in.Named(kindPyProcfile, "release")) > 0 {
		return true
	}
	return len(in.Of(kindDjangoModule)) > 0 && len(in.Of(kindDjangoMigration)) > 0
}

// shellQuoteArg põe aspas simples num alvo com caracteres de shell (um alvo `pacote.modulo:app` não precisa).
func shellQuoteArg(s string) string {
	if regexp.MustCompile(`^[A-Za-z0-9_.:/-]+$`).MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func orDash(s string) string {
	if s == "" {
		return "vazio"
	}
	return s
}

// ruleHEU09For é a HEU-09 (variáveis de configuração) com a pré-condição do catálogo.
func ruleHEU09For(requires string) Rule {
	r := ruleHEU09()
	r.Meta.Requires = []string{requires}
	return r
}
