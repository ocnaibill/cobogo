package analyzer

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Compatibilidade entre as versões fixadas e o Python (DEC-54 A). A tabela
// (`pycompat.json`) é GERADA por `scripts/gen-pycompat` a partir dos classificadores do
// PyPI de cada release de uma lista fechada de pacotes (o Django e os pacotes com código
// nativo que mais quebram num Python mais novo que a versão fixada). Não vem de nenhum
// repositório de teste. Classificador não é garantia de wheel: a regra PERGUNTA, não escolhe.
//
//go:embed pycompat.json
var pycompatJSON []byte

type pycompatFile struct {
	Generated string                          `json:"generated"`
	Packages  map[string]map[string][2]string `json:"packages"`
}

var (
	pycompatOnce sync.Once
	pycompat     pycompatFile
	pycompatKeys map[string][]string // versões de cada pacote, em ordem crescente
)

func loadPycompat() {
	pycompatOnce.Do(func() {
		_ = json.Unmarshal(pycompatJSON, &pycompat)
		pycompatKeys = map[string][]string{}
		for pkg, entries := range pycompat.Packages {
			keys := make([]string, 0, len(entries))
			for v := range entries {
				keys = append(keys, v)
			}
			sort.Slice(keys, func(i, j int) bool { return versionLess(keys[i], keys[j]) })
			pycompatKeys[pkg] = keys
		}
	})
}

// versionLess compara versões numéricas separadas por ponto (10.2.0 < 10.10).
func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// pythonRangeOf devolve a menor e a maior versão do Python que o release declara: a
// entrada da tabela com a maior versão que não passa da fixada.
func pythonRangeOf(pkg, version string) (lo, hi string, ok bool) {
	loadPycompat()
	keys := pycompatKeys[pkg]
	if len(keys) == 0 {
		return "", "", false
	}
	i := sort.Search(len(keys), func(i int) bool { return versionLess(version, keys[i]) })
	if i == 0 {
		return "", "", false // antes do primeiro release conhecido
	}
	r := pycompat.Packages[pkg][keys[i-1]]
	return r[0], r[1], r[0] != "" || r[1] != ""
}

var exactPin = regexp.MustCompile(`^\s*[A-Za-z0-9][A-Za-z0-9._-]*(?:\[[^\]]*\])?\s*===?\s*([0-9]+(?:\.[0-9]+)*)`)

// pinConflict é um pacote fixado cujo release não declara suporte à versão do Python.
type pinConflict struct {
	Package, Version, Lo, Hi string
	Evidence                 domain.Evidence
}

// pinnedRanges devolve, por pacote da tabela fixado com `==`, o intervalo de Python que o
// release declara.
func pinnedRanges(deps []domain.Evidence) []pinConflict {
	var out []pinConflict
	for _, e := range deps {
		m := exactPin.FindStringSubmatch(e.Value)
		if m == nil {
			continue
		}
		if lo, hi, ok := pythonRangeOf(e.Name, m[1]); ok {
			out = append(out, pinConflict{Package: e.Name, Version: m[1], Lo: lo, Hi: hi, Evidence: e})
		}
	}
	return out
}

func pyMinor(v string) int {
	m := minorVersion.FindStringSubmatch(v)
	if m == nil {
		return 0
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	return a*100 + b
}

// fits diz se a versão do Python está dentro do intervalo que o release declara. Um limite
// ausente não restringe.
func (c pinConflict) fits(python string) bool {
	v := pyMinor(python)
	if c.Lo != "" && v < pyMinor(c.Lo) {
		return false
	}
	if c.Hi != "" && v > pyMinor(c.Hi) {
		return false
	}
	return true
}

// compatiblePython devolve a MAIOR versão (do catálogo ou legada) que todos os pacotes
// fixados admitem, e os pacotes que não admitem a versão de partida.
func compatiblePython(pins []pinConflict, from string) (best string, blockers []pinConflict) {
	return compatiblePythonWhere(pins, from, func(string) bool { return true })
}

// compatiblePythonWhere é a mesma busca, só entre as versões que allowed aceita (por
// exemplo, as que o requires-python do projeto admite).
func compatiblePythonWhere(pins []pinConflict, from string, allowed func(string) bool) (best string, blockers []pinConflict) {
	for _, p := range pins {
		if !p.fits(from) {
			blockers = append(blockers, p)
		}
	}
	candidates := append(append([]string{}, domain.SupportedPythonVersions...), domain.LegacyPythonVersions...)
	sort.Slice(candidates, func(i, j int) bool { return pyMinor(candidates[i]) > pyMinor(candidates[j]) })
	for _, v := range candidates {
		ok := allowed(v)
		for _, p := range pins {
			if !p.fits(v) {
				ok = false
				break
			}
		}
		if ok {
			return v, blockers
		}
	}
	return "", blockers
}

// pyRangeText descreve o intervalo que o release declara ("3.8 a 3.12").
func pyRangeText(p pinConflict) string {
	switch {
	case p.Lo != "" && p.Hi != "":
		return "Python " + p.Lo + " a " + p.Hi
	case p.Hi != "":
		return "Python até " + p.Hi
	}
	return "Python " + p.Lo + " ou mais"
}
