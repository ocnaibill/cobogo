package analyzer

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// bunExact reconhece uma versão exata do Bun, com ou sem "v" ou "=" na frente.
var bunExact = regexp.MustCompile(`^[v=]?(\d+)\.(\d+)\.(\d+)$`)

// ruleHEU11 escolhe a versão do Bun da imagem (DEC-36 item 6). Uma versão
// exata declarada em packageManager ("bun@1.1.38") ou em engines.bun fixa a
// imagem; uma faixa ou nada usam a linha mais recente da major 1. Uma major
// diferente de 1 vira aviso e vale o padrão.
func ruleHEU11() Rule {
	return Rule{
		Meta: RuleMeta{
			ID: "HEU-11", Version: "0.1", State: RuleProposed,
			Requires: []string{"HEU-01"},
			Fields:   []string{domain.FieldBunVersion},
		},
		Apply: func(in RuleInput) RuleOutput {
			manager, lock := jsManager(in)
			if manager != domain.ManagerBun {
				return RuleOutput{}
			}
			out := RuleOutput{Proposals: []domain.Proposal{
				{Field: domain.FieldBunVersion, Value: domain.DefaultBunVersion, Origin: domain.OriginDefault},
			}}

			var declared []domain.Evidence
			for _, e := range in.Of(kindPackageManagerField) {
				if name, version, ok := strings.Cut(e.Value, "@"); ok && name == "bun" {
					// "bun@1.1.38+sha512..." traz hash depois do +.
					version, _, _ = strings.Cut(version, "+")
					declared = append(declared, domain.Evidence{ID: e.ID, Source: e.Source, Value: version})
				}
			}
			declared = append(declared, in.Named(kindEngines, "bun")...)

			for _, e := range declared {
				m := bunExact.FindStringSubmatch(strings.TrimSpace(e.Value))
				if m == nil {
					continue // faixa: vale a linha mais recente da major 1
				}
				if m[1] != "1" {
					out.Warnings = append(out.Warnings, finding("", fmt.Sprintf(
						"HEU-11: %s declara o Bun %q, fora da major 1 que o Cobogó suporta; a imagem usará oven/bun:%s", e.Source, e.Value, domain.DefaultBunVersion), e))
					continue
				}
				version := m[1] + "." + m[2] + "." + m[3]
				if minor, _ := strconv.Atoi(m[2]); lock.Source == "bun.lock" && minor < 2 {
					out.Warnings = append(out.Warnings, finding("", fmt.Sprintf(
						"HEU-11: %s fixa o Bun %s, mas o bun.lock em texto exige o Bun 1.2 ou mais novo; a instalação pode falhar", e.Source, version), e, lock))
				}
				out.Proposals = append(out.Proposals, domain.Proposal{
					Field: domain.FieldBunVersion, Value: version, Origin: domain.OriginDeclaration, EvidenceIDs: []string{e.ID},
				})
				return out
			}
			return out
		},
	}
}
