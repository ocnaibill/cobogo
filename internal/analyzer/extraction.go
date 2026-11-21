package analyzer

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/ocnaibill/cobogo/internal/domain"
)

const (
	kindManifest        = "manifest.package_json"
	kindManifestInvalid = "manifest.invalid"
	kindWorkspaces      = "manifest.workspaces"
	kindLockfile        = "lockfile"
	kindScript          = "npm.script"
	kindEngines         = "runtime.engines"
	kindNvmrc           = "runtime.nvmrc"
	kindEnvDeclared     = "env.declared"
	kindEnvReference    = "env.reference"
	// kindEntryField: campo module ou main do package.json (DEC-36).
	kindEntryField = "manifest.entry"
	// kindPackageManagerField: campo packageManager ("bun@1.1.38").
	kindPackageManagerField = "manifest.package_manager"
	// kindBunDefaultPort: servidor do Bun sem porta, que lê PORT sozinho
	// (Bun.serve sem port, ou export default do arquivo de entrada; DEC-36).
	kindBunDefaultPort = "bun.default_port"
	// Perfil estático (DEC-37).
	kindStaticIndex  = "static.index"        // index.html na raiz
	kindDependency   = "manifest.dependency" // dependência que decide a forma de publicar
	kindStaticOutDir = "static.out_dir"      // outDir literal na configuração da ferramenta
	kindSSRMarker    = "static.ssr"          // sinal de renderização no servidor
	kindNextExport   = "static.next_export"  // next.config com output: 'export'
	// kindDockerArtifact: Dockerfile ou compose já presente na raiz (HEU-08).
	kindDockerArtifact = "artifact.docker"
)

const maxFileSize = 1 << 20 // 1 MiB; arquivos maiores viram lacuna de cobertura

// Extraction acumula as evidências e a cobertura de uma análise.
type Extraction struct {
	evidence  map[string]domain.Evidence
	gaps      []domain.Gap
	filesRead int
}

func newExtraction() *Extraction {
	return &Extraction{evidence: map[string]domain.Evidence{}}
}

func (x *Extraction) emit(extractor string, ev domain.Evidence) {
	ev.Extractor = extractor
	ev.ID = evidenceID(ev)
	x.evidence[ev.ID] = ev
}

func (x *Extraction) gap(path, reason string) {
	x.gaps = append(x.gaps, domain.Gap{Path: path, Reason: reason})
}

// finish devolve evidências e lacunas em ordem estável (RNF-01).
func (x *Extraction) finish() ([]domain.Evidence, domain.Coverage) {
	evidence := make([]domain.Evidence, 0, len(x.evidence))
	for _, e := range x.evidence {
		evidence = append(evidence, e)
	}
	slices.SortFunc(evidence, func(a, b domain.Evidence) int {
		return cmp.Or(
			cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(a.Name, b.Name),
			cmp.Compare(a.Source, b.Source),
			cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.ID, b.ID),
		)
	})

	gaps := slices.Clone(x.gaps)
	slices.SortFunc(gaps, func(a, b domain.Gap) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Reason, b.Reason))
	})
	gaps = slices.Compact(gaps)
	if gaps == nil {
		gaps = []domain.Gap{}
	}
	return evidence, domain.Coverage{FilesRead: x.filesRead, Gaps: gaps}
}

func evidenceID(e domain.Evidence) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x1f%s\x1f%s\x1f%s\x1f%s\x1f%d\x1f%s",
		e.Extractor, e.Kind, e.Name, e.Value, e.Source, e.Line, e.Scope))
	return "ev-" + hex.EncodeToString(sum[:6])
}

type fileState int

const (
	fileOK fileState = iota
	fileMissing
	fileSymlink
	fileTooLarge
)

// readSmallFile lê um arquivo regular dentro do alvo. Links simbólicos não
// são seguidos e arquivos grandes não são lidos (§4.1 do motor).
func readSmallFile(fsys fs.FS, name string) ([]byte, fileState, error) {
	info, err := fs.Lstat(fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fileMissing, nil
		}
		return nil, fileMissing, err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, fileSymlink, nil
	case !info.Mode().IsRegular():
		return nil, fileMissing, nil
	case info.Size() > maxFileSize:
		return nil, fileTooLarge, nil
	}
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fileMissing, err
	}
	return data, fileOK, nil
}

// of devolve as evidências já emitidas de um tipo, em ordem estável. Serve a
// extratores que dependem de outros (o de código usa os scripts do manifesto).
func (x *Extraction) of(kind string) []domain.Evidence {
	var out []domain.Evidence
	for _, e := range x.evidence {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b domain.Evidence) int { return cmp.Compare(a.ID, b.ID) })
	return out
}
