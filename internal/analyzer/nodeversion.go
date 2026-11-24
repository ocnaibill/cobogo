package analyzer

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"

	"github.com/ocnaibill/cobogo/internal/domain"
)

var nvmrcVersion = regexp.MustCompile(`^v?(\d+)(?:\.\d+){0,2}$`)

func supportedNodeVersions() string { return strings.Join(domain.SupportedNodeVersions, ", ") }

// admittedByRange devolve, em ordem crescente, as versões major do catálogo
// para as quais algum release satisfaz o intervalo do npm. A imagem de uma
// major acompanha o release mais recente dela, então "algum release" é o
// critério: `^22.12` admite 22 mesmo que 22.0 não o satisfaça. A verificação
// amostra minors de 0 a 99 com patches 0 e 999; é uma aproximação (um pin
// exato num patch incomum pode não ser reconhecido).
func admittedByRange(c *semver.Constraints) []string {
	var admitted []string
	for _, major := range domain.SupportedNodeVersions {
		m, _ := strconv.ParseUint(major, 10, 64)
	releases:
		for minor := uint64(0); minor < 100; minor++ {
			for _, patch := range []uint64{0, 999} {
				if c.Check(semver.New(m, minor, patch, "", "")) {
					admitted = append(admitted, major)
					break releases
				}
			}
		}
	}
	return admitted
}

// ruleHEU10 escolhe a versão do Node da imagem (DEC-25). A declaração do
// projeto (engines.node, senão .nvmrc) escolhe a maior versão suportada que
// ela admite; sem declaração, ou se nenhuma versão suportada a admite, vale o
// padrão e uma declaração que nenhuma versão admite vira aviso, não bloqueio:
// o npm só avisa sobre engines, e a maioria dos projetos roda em versões mais
// novas.
func ruleHEU10() Rule {
	return Rule{
		Meta: RuleMeta{
			ID: "HEU-10", Version: "0.3", State: RuleProposed,
			Requires: []string{"HEU-01"},
			Fields:   []string{domain.FieldNodeVersion, domain.FieldBuildLegacyOpenSSL},
		},
		Apply: func(in RuleInput) RuleOutput {
			// A versão do Node só vale para o perfil P-NODE; o P-BUN tem a
			// HEU-11 (DEC-36).
			if manager, _ := jsManager(in); manager != domain.ManagerNPM {
				return RuleOutput{}
			}
			out := RuleOutput{Proposals: []domain.Proposal{
				{Field: domain.FieldNodeVersion, Value: domain.DefaultNodeVersion, Origin: domain.OriginDefault},
			}}
			// node-sass compila código nativo contra a API do V8 e está
			// descontinuado: não funciona nas versões do catálogo.
			for _, dep := range in.Named(kindDependency, "node-sass") {
				out.Warnings = append(out.Warnings, finding("", fmt.Sprintf(
					"HEU-10: %s depende de node-sass, descontinuado e que compila código nativo: ele não funciona nas versões de Node do catálogo (%s) e o build tende a falhar. O substituto recomendado é o pacote sass (Dart Sass). Suporte a versões antigas do Node é a issue #32.",
					dep.Source, supportedNodeVersions()), dep))
			}
			// Ferramenta de build do webpack 4 num Node que tem o OpenSSL 3 (DEC-54 B): o
			// contorno documentado vale só no build, e só com a confirmação da pessoa.
			if legacy := in.Of(kindLegacyToolchain); len(legacy) > 0 && len(in.Named(kindScript, "build")) > 0 && !scriptsHandleOpenSSL(in) {
				first := legacy[0]
				switch in.Overrides[domain.FieldBuildLegacyOpenSSL] {
				case "true":
					out.Warnings = append(out.Warnings, finding("", fmt.Sprintf(
						"HEU-10: %s %s usa o webpack 4; o build roda com NODE_OPTIONS=--openssl-legacy-provider (só no build, nunca na imagem que executa a aplicação), como você confirmou.",
						first.Name, first.Value), first))
				case "false":
				default:
					f := finding(domain.FieldBuildLegacyOpenSSL, fmt.Sprintf(
						"HEU-10: %s %s usa o webpack 4, que falha no Node 17 ou mais novo com ERR_OSSL_EVP_UNSUPPORTED (o OpenSSL 3 não tem o algoritmo MD4 que ele usa). A imagem usa o Node %s. O contorno documentado é rodar só o build com NODE_OPTIONS=--openssl-legacy-provider; a aplicação em execução não muda. Quer ativá-lo?",
						first.Name, first.Value, domain.DefaultNodeVersion), first)
					f.Options = []domain.Option{{Value: "true", Kind: domain.OptionProduction, Note: "NODE_OPTIONS=--openssl-legacy-provider só no comando de build"}}
					out.Pending = append(out.Pending, f)
				}
			}
			warn := func(e domain.Evidence, why string) {
				out.Warnings = append(out.Warnings, finding("", fmt.Sprintf(
					"HEU-10: %s declara %q, %s; a imagem usará o Node %s (versões suportadas: %s). Se a aplicação falhar, a versão do Node pode ser o motivo; você pode escolher outra.",
					e.Source, e.Value, why, domain.DefaultNodeVersion, supportedNodeVersions()), e))
			}

			var hints []domain.Evidence
			hints = append(hints, in.Of(kindEngines)...)
			hints = append(hints, in.Of(kindNvmrc)...)
			for _, hint := range hints {
				admitted, understood := admittedVersions(hint)
				switch {
				case !understood:
					warn(hint, "um valor que o Cobogó não sabe interpretar")
				case len(admitted) == 0:
					warn(hint, "uma versão que nenhuma versão suportada admite")
				default:
					out.Proposals = append(out.Proposals, domain.Proposal{
						Field: domain.FieldNodeVersion, Value: admitted[len(admitted)-1],
						Origin: domain.OriginDeclaration, EvidenceIDs: []string{hint.ID},
					})
					return out
				}
			}
			return out
		},
	}
}

// admittedVersions interpreta uma pista de versão. understood=false quando o
// valor não é um intervalo do npm nem uma versão numérica (ex.: `lts/*`).
func admittedVersions(hint domain.Evidence) (admitted []string, understood bool) {
	if hint.Kind == kindNvmrc {
		m := nvmrcVersion.FindStringSubmatch(hint.Value)
		if m == nil {
			return nil, false
		}
		if slices.Contains(domain.SupportedNodeVersions, m[1]) {
			return []string{m[1]}, true
		}
		return nil, true
	}
	c, err := semver.NewConstraint(hint.Value)
	if err != nil {
		return nil, false
	}
	return admittedByRange(c), true
}

// validateNodeVersion recusa versões fora do catálogo.
func validateNodeVersion(v string) error {
	if !slices.Contains(domain.SupportedNodeVersions, v) {
		return fmt.Errorf("%q não é uma versão de Node suportada; use uma de %s", v, supportedNodeVersions())
	}
	return nil
}

// scriptsHandleOpenSSL diz se algum script do projeto já aplica o contorno do OpenSSL: o projeto
// resolveu sozinho, e o Cobogó não pergunta (DEC-54 B).
func scriptsHandleOpenSSL(in RuleInput) bool {
	for _, e := range in.Of(kindScript) {
		if strings.Contains(e.Value, "openssl-legacy-provider") {
			return true
		}
	}
	return false
}
