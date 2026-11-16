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

// extractManifest lê package.json e detecta os lockfiles. Só emite os
// scripts start e build, os únicos que as regras consomem.
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
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		invalid(err.Error())
		return nil
	}

	x.emit(extractorManifest, domain.Evidence{Kind: kindManifest, Source: "package.json", Scope: domain.ScopeUnknown})
	if hasWorkspaces(pkg.Workspaces) {
		x.emit(extractorManifest, domain.Evidence{Kind: kindWorkspaces, Source: "package.json#workspaces", Scope: domain.ScopeUnknown})
	}
	scopes := map[string]domain.Scope{"start": domain.ScopeRuntime, "build": domain.ScopeBuild}
	for name, scope := range scopes {
		if cmd := strings.TrimSpace(pkg.Scripts[name]); cmd != "" {
			x.emit(extractorManifest, domain.Evidence{Kind: kindScript, Name: name, Value: cmd, Source: "package.json#scripts." + name, Scope: scope})
		}
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
