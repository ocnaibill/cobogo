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

// Analyze decide o ecossistema da pasta (DEC-48 item 1) e aplica o catálogo dele:
// o JavaScript (P-NODE, P-BUN, P-STATIC) ou o Python (P-PY). Marcadores dos dois na
// mesma pasta viram pergunta, a menos que a pessoa já tenha respondido.
func Analyze(fsys fs.FS, in Input) (domain.AnalysisResult, error) {
	eco, markers := detectEcosystem(fsys)
	switch choice := strings.TrimSpace(in.Overrides[domain.FieldEcosystem]); {
	case choice == domain.EcosystemPython:
		return analyzeWith(PythonCatalog(), fsys, in)
	case choice == domain.EcosystemJavaScript:
		return analyzeWith(NodeCatalog(), fsys, in)
	case eco == ecosystemBoth:
		return ecosystemQuestion(markers), nil
	case eco == domain.EcosystemPython:
		return analyzeWith(PythonCatalog(), fsys, in)
	case len(markers) == 0 && !exists(fsys, "index.html"):
		// Nada na raiz: um monorepo com as aplicações em subpastas (F-5)?
		if found := subprojects(fsys); len(found) > 0 {
			return monorepoResult(found), nil
		}
	}
	return analyzeWith(NodeCatalog(), fsys, in)
}

// allCatalogs são os catálogos que existem: um override de campo de outro catálogo
// (de uma escolha de ecossistema anterior) é ignorado, não é erro.
func allCatalogs() []Catalog { return []Catalog{NodeCatalog(), PythonCatalog()} }

// forCatalog separa os overrides que valem para este catálogo. Um campo que só
// outro catálogo conhece é deixado de lado; um campo que nenhum conhece fica (e o
// checkOverrides o recusa).
func forCatalog(cat Catalog, in map[string]string) map[string]string {
	mine := map[string]bool{}
	for _, f := range cat.Fields {
		mine[f.Name] = true
	}
	known := map[string]bool{}
	for _, c := range allCatalogs() {
		for _, f := range c.Fields {
			known[f.Name] = true
		}
	}
	out := map[string]string{}
	for k, v := range in {
		if mine[k] || !known[k] {
			out[k] = v
		}
	}
	return out
}

func analyzeWith(cat Catalog, fsys fs.FS, in Input) (domain.AnalysisResult, error) {
	overrides, invalid, err := checkOverrides(cat, forCatalog(cat, in.Overrides))
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
	ruleInput := RuleInput{Evidence: evidence, Coverage: coverage, Overrides: overrides}

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
	res.PersistentSuggestions = []domain.PersistentSuggestion{}
	res.ServiceNeeds = []domain.ServiceNeed{}
	if cat.Persistence != nil {
		res.PersistentSuggestions = cat.Persistence(fsys, evidence)
	}
	if cat.Services != nil {
		res.ServiceNeeds = cat.Services(fsys, evidence, res.ConfigVariables)
	}

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
//
// Vale para os campos de qualquer catálogo: a escolha do ecossistema pode vir
// junto das respostas do outro (DEC-48).
func CheckOverrides(in map[string]string) error {
	pending := maps.Clone(in)
	for _, cat := range allCatalogs() {
		mine := map[string]string{}
		for _, f := range cat.Fields {
			if v, ok := pending[f.Name]; ok {
				mine[f.Name] = v
			}
		}
		_, invalid, err := checkOverrides(cat, mine)
		if err != nil {
			return err
		}
		for _, field := range slices.Sorted(maps.Keys(invalid)) {
			return fmt.Errorf("valor inválido para %q: %w", field, invalid[field])
		}
		for k := range mine {
			delete(pending, k)
		}
	}
	for _, field := range slices.Sorted(maps.Keys(pending)) {
		return fmt.Errorf("analyzer: o campo %q não aceita override", field)
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
		// Um campo obrigatório sem valor não ganha pendência genérica quando a
		// regra dona dele já perguntou outra coisa: o perfil sai da escolha
		// do gerenciador, por exemplo (DEC-36).
		open, asked := map[string]bool{}, map[string]bool{}
		for _, f := range slices.Concat(kept, conflicts) {
			open[f.Field] = true
			asked[f.Rule] = true
		}
		owner := map[string]string{}
		for _, r := range cat.Rules {
			for _, field := range r.Meta.Fields {
				owner[field] = r.Meta.ID
			}
		}
		profile := fields[domain.FieldProfile].Value
		for _, spec := range cat.Fields {
			if slices.Contains(spec.OptionalFor, profile) {
				continue
			}
			if _, ok := fields[spec.Name]; !ok && spec.Required && !open[spec.Name] && !asked[owner[spec.Name]] {
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
