package analyzer

import (
	"io/fs"
	"path"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Evidência de lockfile numa pasta acima da aplicação (DEC-53 G): o lockfile de um
// workspace fica na raiz dele, não na pasta de cada pacote. O Cobogó não suporta
// workspaces, mas a recusa precisa dizer onde o lockfile está em vez de "nenhum
// lockfile". Name é o gerenciador; Value, a pasta; Source, o arquivo.
const kindAncestorLock = "ancestor.lock"

var ancestorLocks = []struct{ file, manager string }{
	{"package-lock.json", "npm"}, {"yarn.lock", "yarn"}, {"pnpm-lock.yaml", "pnpm"},
	{"bun.lock", "bun"}, {"bun.lockb", "bun"}, {"uv.lock", "uv"}, {"poetry.lock", "poetry"},
}

// extractAncestors procura lockfiles nas pastas acima de dir, dentro do repositório. repo
// é a raiz do repositório e dir, o diretório da aplicação nele.
func extractAncestors(repo fs.FS, dir string, x *Extraction) {
	dir = path.Clean(dir)
	if repo == nil || dir == "." || dir == "" || path.IsAbs(dir) {
		return
	}
	for parent := path.Dir(dir); ; parent = path.Dir(parent) {
		for _, l := range ancestorLocks {
			file := joinDir(parent, l.file)
			if exists(repo, file) {
				x.emit("ancestors", domain.Evidence{Kind: kindAncestorLock, Name: l.manager, Value: parent, Source: file, Scope: domain.ScopeBuild})
			}
		}
		if parent == "." {
			return
		}
	}
}
