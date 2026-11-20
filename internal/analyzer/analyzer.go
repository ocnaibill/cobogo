// Package analyzer é o motor de heurísticas (DEC-23): extratores leem o
// repositório e produzem evidências com escopo; regras versionadas propõem
// valores, pendências e conflitos; um resolvedor aplica a precedência da
// RN-03. Nada do repositório é executado (RN-08).
package analyzer

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Input é o que, além dos arquivos, influencia a análise. Overrides mapeia
// campo do plano para o valor informado pelo usuário (RN-03, RF-08).
type Input struct {
	Overrides map[string]string
}

// Analyze aplica o catálogo do perfil P-NODE sobre o diretório do projeto.
func Analyze(fsys fs.FS, in Input) (domain.AnalysisResult, error) {
	return analyzeWith(NodeCatalog(), fsys, in)
}

func analyzeWith(cat Catalog, fsys fs.FS, in Input) (domain.AnalysisResult, error) {
	overrides, invalid, err := checkOverrides(cat, in.Overrides)
	if err != nil {
		return domain.AnalysisResult{}, err
	}

	x := newExtraction()
	for _, e := range cat.Extractors {
		if err := e.Extract(fsys, x); err != nil {
			return domain.AnalysisResult{}, fmt.Errorf("analyzer: extrator %s: %w", e.ID, err)
		}
	}
	evidence, coverage := x.finish()
	ruleInput := RuleInput{Evidence: evidence, Coverage: coverage}

	res := domain.AnalysisResult{
		CatalogVersion:  cat.Version,
		Rules:           []domain.AppliedRule{},
		Pending:         []domain.Finding{},
		Conflicts:       []domain.Finding{},
		Unsupported:     []domain.Finding{},
		ConfigVariables: []domain.ConfigVariable{},
		Warnings:        []domain.Finding{},
		Evidences:       evidence,
		Coverage:        coverage,
	}

	blocked := map[string]bool{}
	var proposals []domain.Proposal
	for _, rule := range cat.Rules {
		applied := domain.AppliedRule{ID: rule.Meta.ID, Version: rule.Meta.Version, State: string(rule.Meta.State), Status: "applied"}
		blockedBy := firstBlocked(rule.Meta.Requires, blocked)
		switch {
		case rule.Meta.State == RuleRetired:
			applied.Status = "retired"
		case blockedBy != "":
			applied.Status = "skipped"
			applied.Reason = "pré-condição não satisfeita: " + blockedBy
			blocked[rule.Meta.ID] = true
		}
		res.Rules = append(res.Rules, applied)
		if applied.Status != "applied" {
			continue
		}

		out := rule.Apply(ruleInput)
		if err := checkOwnership(rule.Meta, out); err != nil {
			return domain.AnalysisResult{}, err
		}
		for _, p := range out.Proposals {
			p.Rule = rule.Meta.ID
			proposals = append(proposals, p)
		}
		res.Pending = append(res.Pending, stamp(rule.Meta.ID, out.Pending)...)
		res.Conflicts = append(res.Conflicts, stamp(rule.Meta.ID, out.Conflicts)...)
		res.Unsupported = append(res.Unsupported, stamp(rule.Meta.ID, out.Unsupported)...)
		res.ConfigVariables = append(res.ConfigVariables, out.ConfigVariables...)
		res.Warnings = append(res.Warnings, stamp(rule.Meta.ID, out.Warnings)...)
		if len(out.Unsupported) > 0 {
			blocked[rule.Meta.ID] = true
		}
	}
	slices.SortFunc(res.ConfigVariables, func(a, b domain.ConfigVariable) int { return strings.Compare(a.Name, b.Name) })

	supported := len(res.Unsupported) == 0
	res.Fields, res.Pending, res.Conflicts = resolve(cat, proposals, res.Pending, res.Conflicts, overrides, invalid, supported)

	switch {
	case !supported:
		res.Outcome = domain.AnalysisUnsupported
	case len(res.Pending) > 0 || len(res.Conflicts) > 0:
		res.Outcome = domain.AnalysisNeedsInput
	default:
		res.Outcome = domain.AnalysisReady
	}
	return res, nil
}

func firstBlocked(requires []string, blocked map[string]bool) string {
	for _, id := range requires {
		if blocked[id] {
			return id
		}
	}
	return ""
}

func stamp(ruleID string, findings []domain.Finding) []domain.Finding {
	out := make([]domain.Finding, len(findings))
	for i, f := range findings {
		f.Rule = ruleID
		out[i] = f
	}
	return out
}

// checkOverrides separa os overrides em válidos e inválidos. Pedir override
// de um campo que não o aceita é erro de quem chama; um valor inválido vira
// pendência e o override é ignorado.
func checkOverrides(cat Catalog, in map[string]string) (valid map[string]string, invalid map[string]error, err error) {
	specs := map[string]FieldSpec{}
	for _, s := range cat.Fields {
		specs[s.Name] = s
	}
	valid, invalid = map[string]string{}, map[string]error{}
	for _, name := range slices.Sorted(maps.Keys(in)) {
		spec, ok := specs[name]
		if !ok || !spec.Overridable {
			return nil, nil, fmt.Errorf("analyzer: o campo %q não aceita override", name)
		}
		value := strings.TrimSpace(in[name])
		if spec.Validate != nil {
			if verr := spec.Validate(value); verr != nil {
				invalid[name] = verr
				continue
			}
		}
		valid[name] = value
	}
	return valid, invalid, nil
}

// CheckOverrides valida overrides contra o catálogo atual antes de a API
// aceitá-los: campo desconhecido, que não aceita override, ou valor inválido.
// É validação estrutural; se o valor funciona só o build e a execução dizem.
func CheckOverrides(in map[string]string) error {
	_, invalid, err := checkOverrides(NodeCatalog(), in)
	if err != nil {
		return err
	}
	for _, field := range slices.Sorted(maps.Keys(invalid)) {
		return fmt.Errorf("valor inválido para %q: %w", field, invalid[field])
	}
	return nil
}

// checkOwnership garante que uma regra só escreve nos campos que lhe
// pertencem (§6 do motor).
func checkOwnership(meta RuleMeta, out RuleOutput) error {
	trespass := func(field string) error {
		return fmt.Errorf("analyzer: a regra %s escreveu o campo %q, que não lhe pertence", meta.ID, field)
	}
	for _, p := range out.Proposals {
		if !slices.Contains(meta.Fields, p.Field) {
			return trespass(p.Field)
		}
	}
	for _, f := range slices.Concat(out.Pending, out.Conflicts, out.Unsupported) {
		if f.Field != "" && !slices.Contains(meta.Fields, f.Field) {
			return trespass(f.Field)
		}
	}
	return nil
}

var originRank = map[domain.Origin]int{
	domain.OriginDeclaration: 4,
	domain.OriginPlatform:    3,
	domain.OriginInference:   2,
	domain.OriginDefault:     1,
}

// resolve junta as propostas por campo: override > declaração > plataforma >
// inferência > default. Valores diferentes no mesmo nível são conflito, nunca "o último
// vence". Com fillRequired, campo obrigatório sem valor e sem pendência vira
// pendência genérica.
func resolve(cat Catalog, proposals []domain.Proposal, pending, conflicts []domain.Finding,
	overrides map[string]string, invalid map[string]error, fillRequired bool,
) (map[string]domain.Field, []domain.Finding, []domain.Finding) {
	byField := map[string][]domain.Proposal{}
	for _, p := range proposals {
		byField[p.Field] = append(byField[p.Field], p)
	}

	fields := map[string]domain.Field{}
	answered := map[string]bool{}
	for _, spec := range cat.Fields {
		candidates := byField[spec.Name]
		if value, ok := overrides[spec.Name]; ok {
			fields[spec.Name] = domain.Field{Value: value, Origin: domain.OriginOverride, Overrode: candidates}
			answered[spec.Name] = true
			continue
		}
		if len(candidates) == 0 {
			continue
		}
		field, conflict := pickProposal(spec.Name, candidates)
		if conflict != nil {
			conflicts = append(conflicts, *conflict)
			continue
		}
		fields[spec.Name] = field
	}

	kept := []domain.Finding{}
	for _, p := range pending {
		if p.Field == "" || !answered[p.Field] {
			kept = append(kept, p)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(invalid)) {
		kept = append(kept, domain.Finding{Field: name, Message: fmt.Sprintf("override inválido para %s: %v", name, invalid[name])})
	}

	if fillRequired {
		open := map[string]bool{}
		for _, f := range slices.Concat(kept, conflicts) {
			open[f.Field] = true
		}
		for _, spec := range cat.Fields {
			if _, ok := fields[spec.Name]; !ok && spec.Required && !open[spec.Name] {
				kept = append(kept, domain.Finding{Field: spec.Name, Message: "nenhuma regra resolveu o campo obrigatório " + spec.Name})
			}
		}
	}
	return fields, kept, conflicts
}

func pickProposal(field string, candidates []domain.Proposal) (domain.Field, *domain.Finding) {
	top := 0
	for _, p := range candidates {
		top = max(top, originRank[p.Origin])
	}
	values := map[string]bool{}
	var ids, rules []string
	var winner domain.Proposal
	for _, p := range candidates {
		if originRank[p.Origin] != top {
			continue
		}
		winner = p
		values[p.Value] = true
		ids = append(ids, p.EvidenceIDs...)
		rules = append(rules, p.Rule)
	}
	if len(values) > 1 {
		return domain.Field{}, &domain.Finding{
			Field:       field,
			Message:     fmt.Sprintf("valores incompatíveis para %s: %s", field, strings.Join(slices.Sorted(maps.Keys(values)), " | ")),
			EvidenceIDs: sortedUnique(ids),
		}
	}
	return domain.Field{Value: winner.Value, Origin: winner.Origin, Rules: sortedUnique(rules), EvidenceIDs: sortedUnique(ids)}, nil
}

func sortedUnique(ss []string) []string {
	return slices.Compact(slices.Sorted(slices.Values(ss)))
}
