package analyzer

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/validate"
)

// Sugestão de caminhos persistentes (DEC-41). Heurística fechada e explicável:
// cada sugestão traz o motivo e os arquivos e linhas em que se apoia. Ela só
// sugere; quem declara o caminho é o usuário. Erra para os dois lados: um
// falso positivo vira um clique ignorado, um falso negativo vira um caminho
// declarado à mão.

const maxSuggestions = 6

// sqliteDeps: dependências que guardam dados num arquivo local.
var sqliteDeps = map[string]bool{
	"better-sqlite3": true, "sqlite3": true, "sqlite": true, "@libsql/client": true,
	"lowdb": true, "nedb": true, "level": true, "classic-level": true,
}

var (
	// Literal que termina como arquivo de banco local. Ignora `:memory:`.
	dbFileLiteral = regexp.MustCompile("[\"'`]([^\"'`\\n]*\\.(?:sqlite3?|db3?))[\"'`]")
	// Importação do SQLite embutido do Bun ou do Node.
	sqliteImport = regexp.MustCompile(`(?:from\s+|require\()\s*["'](?:bun:sqlite|node:sqlite)["']`)
	// Chamadas que escrevem no disco.
	writeCall = regexp.MustCompile(`\b(?:writeFile|writeFileSync|appendFile|appendFileSync|createWriteStream|mkdir|mkdirSync|rename|renameSync|unlink|unlinkSync|rmSync|copyFile|copyFileSync)\s*\(|\bBun\.write\s*\(`)
	// Pasta de dados citada como caminho (`data/x`, `./uploads/y`) ou como argumento
	// de join/resolve (`join(root, "data")`).
	dirNames    = `data|content|uploads?|storage|files|db|database`
	dirWithPath = regexp.MustCompile("[\"'`]((?:\\.{1,2}/)*|/)(" + dirNames + ")/[^\"'`\\n]*[\"'`]")
	dirInJoin   = regexp.MustCompile("(?:join|resolve)\\([^()\\n]*[\"'`]((?:\\.{1,2}/)*)(" + dirNames + ")/?[\"'`]")
	// Texto que antecede um literal de importação (`from "../db/x.ts"`).
	importContext = regexp.MustCompile(`(?:\bfrom|\bimport|\brequire\s*\()\s*\(?\s*$`)
)

type suggestionDraft struct {
	domain.PersistentSuggestion
}

// suggestPersistence lê o código de runtime e o package.json e devolve as
// sugestões, em ordem estável: as fortes primeiro.
func suggestPersistence(fsys fs.FS) []domain.PersistentSuggestion {
	lib := ""
	for _, n := range dependencies(fsys) {
		if sqliteDeps[n] {
			lib = n
			break
		}
	}
	return suggestPersistenceWith(fsys, persistenceLang{
		extensions: sourceExtensions, skip: skippedDirs, sqliteImport: sqliteImport, writeCall: writeCall,
		dirPatterns: []*regexp.Regexp{dirWithPath, dirInJoin},
		sqliteLib:   lib, libSource: "package.json",
	})
}

// persistenceLang é o que muda de uma linguagem para outra na heurística de
// persistência: que arquivos ler, que pastas pular e como se reconhece o SQLite e
// uma escrita em disco.
type persistenceLang struct {
	extensions   map[string]bool
	skip         map[string]bool
	sqliteImport *regexp.Regexp
	writeCall    *regexp.Regexp
	sqliteLib    string // uma dependência que guarda dados num arquivo local
	dirPatterns  []*regexp.Regexp
	skipFile     func(name string) bool
	libSource    string // o arquivo que a declara
	extra        []domain.PersistentSuggestion
}

func suggestPersistenceWith(fsys fs.FS, lang persistenceLang) []domain.PersistentSuggestion {
	byPath := map[string]*suggestionDraft{}
	// unknownSQLite: usa SQLite, mas o caminho do arquivo não se sabe (Path vazio).
	var unknownSQLite *suggestionDraft
	add := func(path, kind, confidence, reason, evidence string) {
		// Um caminho que o usuário não poderia declarar (sistema do contêiner, por
		// exemplo) vira "não sei onde".
		if path != "" {
			if _, err := validate.PersistentPaths([]string{path}); err != nil {
				path, kind, confidence = "", domain.SuggestSQLite, domain.SuggestWeak
				reason = "o código usa um arquivo de banco local em um caminho que o Cobogó não deixa persistir; informe uma pasta dentro do app"
			}
		}
		slot := byPath[path]
		if path == "" {
			slot = unknownSQLite
		}
		if slot == nil {
			slot = &suggestionDraft{domain.PersistentSuggestion{Path: path, Kind: kind, Confidence: confidence, Reason: reason, Evidence: []string{}}}
			if path == "" {
				unknownSQLite = slot
			} else {
				byPath[path] = slot
			}
		}
		if confidence == domain.SuggestStrong && slot.Confidence != domain.SuggestStrong {
			slot.Confidence, slot.Kind, slot.Reason = confidence, kind, reason
		}
		if evidence != "" && len(slot.Evidence) < 3 && !contains(slot.Evidence, evidence) {
			slot.Evidence = append(slot.Evidence, evidence)
		}
	}

	usesSQLiteLib := lang.sqliteLib
	for _, e := range lang.extra {
		add(e.Path, e.Kind, e.Confidence, e.Reason, strings.Join(e.Evidence, ""))
	}

	hasDir := func(dir string) bool {
		info, err := fs.Stat(fsys, dir)
		return err == nil && info.IsDir()
	}

	_ = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if lang.skip[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !lang.extensions[strings.ToLower(path.Ext(name))] || d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		if classifyScope(name) != domain.ScopeRuntime || isToolingPath(name) || (lang.skipFile != nil && lang.skipFile(name)) {
			return nil
		}
		data, state, rerr := readSmallFile(fsys, name)
		if rerr != nil || state != fileOK {
			return nil
		}

		// SQLite: um arquivo citado como literal.
		for _, m := range dbFileLiteral.FindAllSubmatchIndex(data, -1) {
			lit := string(data[m[2]:m[3]])
			// URL do SQLAlchemy: sqlite:///relativo e sqlite:////absoluto.
			if rest, ok := strings.CutPrefix(lit, "sqlite:///"); ok {
				lit = rest
			}
			if lit == ":memory:" || strings.Contains(lit, "${") || strings.ContainsAny(lit, "+*") {
				continue
			}
			dir := containerDir(lit)
			ev := fmt.Sprintf("%s:%d", name, lineAt(data, m[0]))
			if dir == "" {
				add("", domain.SuggestSQLite, domain.SuggestWeak, "o código usa um arquivo de banco local, mas o caminho dele está na raiz do app ou não dá para saber; informe a pasta onde ele fica", ev)
				continue
			}
			// Um caminho absoluto fora do app é menos certo: pode ser só um exemplo.
			confidence := domain.SuggestStrong
			if !strings.HasPrefix(dir, "/app/") {
				confidence = domain.SuggestWeak
			}
			add(dir, domain.SuggestSQLite, confidence, "o código abre o arquivo de banco local "+lit+"; sem volume, ele some a cada deploy", ev)
		}
		if loc := lang.sqliteImport.FindIndex(data); loc != nil {
			add("", domain.SuggestSQLite, domain.SuggestWeak, "o código usa SQLite, mas não achei o caminho do arquivo; informe a pasta onde ele fica", fmt.Sprintf("%s:%d", name, lineAt(data, loc[0])))
		}

		// Pasta em que o código escreve arquivos.
		if lang.writeCall.Match(data) {
			for _, re := range lang.dirPatterns {
				for _, m := range re.FindAllSubmatchIndex(data, -1) {
					// Uma importação (`from "../db/client.ts"`) é código, não dados.
					if start := m[0]; importContext.Match(data[max(0, start-24):start]) {
						continue
					}
					dir := writeDir(name, string(data[m[2]:m[3]]), string(data[m[4]:m[5]]))
					if dir == "" {
						continue
					}
					reason := "o código escreve arquivos em " + dir + "; sem volume, o que for escrito some a cada deploy"
					if hasDir(strings.TrimPrefix(dir, "/app/")) && strings.HasPrefix(dir, "/app/") {
						reason += " (o repositório já tem essa pasta, então o volume começa com o conteúdo dela)"
					}
					add(dir, domain.SuggestWriteDir, domain.SuggestWeak, reason, fmt.Sprintf("%s:%d", name, lineAt(data, m[0])))
				}
			}
		}
		return nil
	})

	out := make([]domain.PersistentSuggestion, 0, len(byPath)+1)
	hasSQLitePath := false
	for _, d := range byPath {
		out = append(out, d.PersistentSuggestion)
		hasSQLitePath = hasSQLitePath || d.Kind == domain.SuggestSQLite
	}
	// Sem um caminho de SQLite achado, a dependência ou a importação ainda avisam.
	if !hasSQLitePath {
		if usesSQLiteLib != "" {
			add("", domain.SuggestSQLite, domain.SuggestWeak, "a dependência "+usesSQLiteLib+" guarda dados num arquivo local, mas não achei o caminho; informe a pasta onde ele fica", lang.libSource)
		}
		if unknownSQLite != nil {
			out = append(out, unknownSQLite.PersistentSuggestion)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Confidence != b.Confidence {
			return a.Confidence == domain.SuggestStrong
		}
		return a.Path < b.Path
	})
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// containerDir converte o caminho de um arquivo, relativo ao app ou absoluto, na
// pasta do contêiner que o guarda. Devolve "" se o arquivo está na raiz do app
// (um volume ali esconderia o código) ou se o caminho não serve.
func containerDir(file string) string {
	file = strings.TrimSpace(file)
	var abs string
	switch {
	case strings.HasPrefix(file, "/"):
		abs = path.Dir(file)
	case strings.HasPrefix(file, "../"), strings.Contains(file, ".."):
		return ""
	default:
		abs = path.Join("/app", path.Dir(strings.TrimPrefix(file, "./")))
	}
	if abs == "/" || abs == "/app" || abs == "." {
		return ""
	}
	return abs
}

// dependencies lista os nomes de dependências de execução do package.json.
func dependencies(fsys fs.FS) []string {
	data, state, err := readSmallFile(fsys, "package.json")
	if err != nil || state != fileOK {
		return nil
	}
	var pkg struct {
		Dependencies         map[string]string `json:"dependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}
	var out []string
	for n := range pkg.Dependencies {
		out = append(out, n)
	}
	for n := range pkg.OptionalDependencies {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// writeDir resolve a pasta citada em um arquivo para o caminho no contêiner. Um
// prefixo com `../` vale a partir da pasta do arquivo; sem prefixo ou com só `./`,
// a partir da pasta de trabalho (/app); uma barra inicial é absoluta. Devolve ""
// se sai do app.
func writeDir(file, prefix, dir string) string {
	switch {
	case prefix == "/":
		return "/" + dir
	case !strings.Contains(prefix, ".."):
		return "/app/" + dir
	}
	resolved := path.Clean(path.Join(path.Dir(file), prefix+dir))
	if resolved == "." || strings.HasPrefix(resolved, "..") {
		return ""
	}
	return "/app/" + resolved
}

// toolingDirs são pastas de ferramentas de desenvolvimento e de build: o que elas
// escrevem não é dado da aplicação em execução.
var toolingDirs = map[string]bool{"scripts": true, "script": true, "tools": true, "tooling": true, "build": true}

func isToolingPath(name string) bool {
	segments := strings.Split(strings.ToLower(name), "/")
	for _, dir := range segments[:len(segments)-1] {
		if toolingDirs[dir] {
			return true
		}
	}
	return false
}
