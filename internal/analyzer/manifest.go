package analyzer

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

const extractorManifest = "manifest"

var lockfiles = []struct{ file, manager string }{
	{"package-lock.json", "npm"},
	{"yarn.lock", "yarn"},
	{"pnpm-lock.yaml", "pnpm"},
}

// extractManifest lê package.json e detecta os lockfiles. Emite todos os
// scripts: start e build alimentam as regras; os demais só servem de opções
// quando o comando de execução precisa ser informado (DEC-24).
func extractManifest(fsys fs.FS, x *Extraction) error {
	for _, lf := range lockfiles {
		if _, err := fs.Lstat(fsys, lf.file); err == nil {
			x.emit(extractorManifest, domain.Evidence{Kind: kindLockfile, Name: lf.manager, Source: lf.file, Scope: domain.ScopeBuild})
		}
	}

	data, state, err := readSmallFile(fsys, "package.json")
	if err != nil {
		return err
	}
	invalid := func(reason string) {
		x.emit(extractorManifest, domain.Evidence{Kind: kindManifestInvalid, Name: "package.json", Value: reason, Source: "package.json", Scope: domain.ScopeUnknown})
	}
	switch state {
	case fileMissing:
		return nil
	case fileSymlink:
		invalid("é um link simbólico")
		return nil
	case fileTooLarge:
		invalid("excede o limite de leitura")
		return nil
	}
	x.filesRead++

	var pkg struct {
		Scripts    map[string]string `json:"scripts"`
		Workspaces json.RawMessage   `json:"workspaces"`
		Engines    map[string]any    `json:"engines"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		invalid(err.Error())
		return nil
	}

	x.emit(extractorManifest, domain.Evidence{Kind: kindManifest, Source: "package.json", Scope: domain.ScopeUnknown})
	if hasWorkspaces(pkg.Workspaces) {
		x.emit(extractorManifest, domain.Evidence{Kind: kindWorkspaces, Source: "package.json#workspaces", Scope: domain.ScopeUnknown})
	}
	for name, raw := range pkg.Scripts {
		cmd := strings.TrimSpace(raw)
		if cmd == "" {
			continue
		}
		scope := domain.ScopeUnknown
		switch name {
		case "start":
			scope = domain.ScopeRuntime
		case "build":
			scope = domain.ScopeBuild
		}
		x.emit(extractorManifest, domain.Evidence{Kind: kindScript, Name: name, Value: cmd, Source: "package.json#scripts." + name, Scope: scope})
	}

	if node, ok := pkg.Engines["node"].(string); ok && strings.TrimSpace(node) != "" {
		x.emit(extractorManifest, domain.Evidence{Kind: kindEngines, Name: "node", Value: strings.TrimSpace(node), Source: "package.json#engines.node", Scope: domain.ScopeBuild})
	}
	return extractNvmrc(fsys, x)
}

// extractNvmrc lê a primeira linha de .nvmrc, que declara a versão de Node
// que a pessoa desenvolvedora usa. Só a linha é guardada, nunca executada.
func extractNvmrc(fsys fs.FS, x *Extraction) error {
	data, state, err := readSmallFile(fsys, ".nvmrc")
	if err != nil {
		return err
	}
	switch state {
	case fileMissing:
		return nil
	case fileSymlink:
		x.gap(".nvmrc", "symlink")
		return nil
	case fileTooLarge:
		x.gap(".nvmrc", "too_large")
		return nil
	}
	x.filesRead++
	first, _, _ := strings.Cut(string(data), "\n")
	if value := strings.TrimSpace(first); value != "" {
		x.emit(extractorManifest, domain.Evidence{Kind: kindNvmrc, Name: ".nvmrc", Value: value, Source: ".nvmrc", Line: 1, Scope: domain.ScopeBuild})
	}
	return nil
}

func hasWorkspaces(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "", "null", "[]", "{}":
		return false
	}
	return true
}
