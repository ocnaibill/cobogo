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
			ID: "HEU-10", Version: ruleVersion, State: RuleProposed,
			Requires: []string{"HEU-01"},
			Fields:   []string{domain.FieldNodeVersion},
		},
		Apply: func(in RuleInput) RuleOutput {
			out := RuleOutput{Proposals: []domain.Proposal{
				{Field: domain.FieldNodeVersion, Value: domain.DefaultNodeVersion, Origin: domain.OriginDefault},
			}}
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
