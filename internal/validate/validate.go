// Package validate valida e normaliza as entradas do usuário que o Cobogó
// usa para tocar o disco e a rede: o diretório do projeto dentro do
// repositório e a URL de clonagem (RF-01, RNF-04, DEC-27).
package validate

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

// DefaultGitHosts são os hosts públicos de repositório aceitos por padrão.
var DefaultGitHosts = []string{"github.com", "gitlab.com", "codeberg.org", "bitbucket.org", "git.sr.ht"}

// reservedSlugs são nomes que um projeto não pode tomar: ficariam parecidos
// demais com serviços do próprio Cobogó ou com endereços de infraestrutura
// (DEC-31 item 4).
var reservedSlugs = []string{"www", "mail", "docs", "api", "admin", "painel", "cobogo", "traefik"}

// ReservedSlug diz se o slug é um dos nomes reservados.
func ReservedSlug(slug string) bool {
	for _, r := range reservedSlugs {
		if slug == r {
			return true
		}
	}
	return false
}

// GitHosts junta os hosts padrão aos extras da configuração, em minúsculas e
// sem repetição.
func GitHosts(extra []string) []string {
	var hosts []string
	seen := map[string]bool{}
	for _, h := range append(append([]string{}, DefaultGitHosts...), extra...) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" && !seen[h] {
			seen[h] = true
			hosts = append(hosts, h)
		}
	}
	return hosts
}

const maxRootLen = 255

// RootDirectory normaliza o diretório do projeto dentro do repositório.
// Aceita os jeitos comuns de escrevê-lo ("", ".", "./a/b", "a/b/", "/a/b") e
// devolve "" para a raiz ou um caminho relativo limpo, com "/" como separador.
// Recusa "..", barra invertida, caracteres de controle, ".git" na raiz e mais
// de 255 bytes. Não toca o disco: o worker confere os links simbólicos depois
// do clone.
func RootDirectory(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if len(s) > maxRootLen {
		return "", fmt.Errorf("o diretório excede %d bytes", maxRootLen)
	}
	for _, r := range s {
		switch {
		case r == '\\':
			return "", errors.New("use / como separador de diretórios")
		case r < ' ' || r == 0x7f:
			return "", errors.New("o diretório não pode ter caracteres de controle")
		}
	}
	for _, segment := range strings.Split(s, "/") {
		if segment == ".." {
			return "", errors.New("o diretório não pode conter ..")
		}
	}
	cleaned := path.Clean("/" + s)
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" {
		return "", nil
	}
	if first, _, _ := strings.Cut(cleaned, "/"); first == ".git" {
		return "", errors.New("o diretório .git não pode ser usado")
	}
	return cleaned, nil
}

const maxURLLen = 2048

// RepositoryURL valida a URL de clonagem e a devolve normalizada. Exige
// https, sem credenciais, sem porta (ou 443), host exato da lista permitida
// (que nunca inclui IPs nem localhost), caminho "dono/repositório" ou mais
// profundo (subgrupos), sem consulta nem fragmento.
func RepositoryURL(raw string, allowedHosts []string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxURLLen {
		return "", errors.New("repository_url é obrigatória e deve ter até 2048 bytes")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("repository_url não é uma URL válida")
	}
	switch {
	case u.Scheme != "https":
		return "", errors.New("repository_url deve usar https:// (RNF-04)")
	case u.User != nil:
		return "", errors.New("repository_url não pode conter credenciais")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "":
		return "", errors.New("repository_url não pode ter consulta nem fragmento")
	case u.Port() != "" && u.Port() != "443":
		return "", errors.New("repository_url não pode usar outra porta além da 443")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || net.ParseIP(host) != nil {
		return "", errors.New("repository_url deve usar o nome de um host, não um IP")
	}
	allowed := false
	for _, h := range allowedHosts {
		if host == strings.ToLower(h) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("o host %q não está na lista de hosts permitidos (%s)", host, strings.Join(allowedHosts, ", "))
	}

	var segments []string
	for _, seg := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		switch seg {
		case "":
			return "", errors.New("o caminho do repositório tem segmento vazio")
		case ".", "..":
			return "", errors.New("o caminho do repositório não pode conter . nem ..")
		}
		for _, r := range seg {
			if r <= ' ' || r == 0x7f || r == '\\' {
				return "", errors.New("o caminho do repositório tem caracteres inválidos")
			}
		}
		segments = append(segments, seg)
	}
	if len(segments) < 2 {
		return "", errors.New("repository_url deve apontar para um repositório (https://host/dono/repositório)")
	}
	return "https://" + host + "/" + strings.Join(segments, "/"), nil
}

// MaxPersistentPaths é o máximo de caminhos persistentes por projeto (DEC-41).
const MaxPersistentPaths = 8

// persistentPathPattern: caminho absoluto, com letras, números, ponto, hífen,
// sublinhado e barra. Nada de espaços, `..` nem caracteres de shell.
var persistentPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,255}$`)

// systemPrefixes são caminhos do sistema do contêiner: montar um volume sobre
// eles esconderia o que a imagem precisa para rodar (DEC-41 item 1).
var systemPrefixes = []string{"/proc", "/sys", "/dev", "/etc", "/bin", "/sbin", "/usr", "/lib", "/lib64", "/run", "/boot", "/var/run", "/tmp"}

// PersistentPaths valida e normaliza a lista de caminhos persistentes que o
// usuário declara: absolutos, limpos, fora do sistema, sem repetição e sem um
// dentro do outro. Devolve a lista ordenada.
func PersistentPaths(raw []string) ([]string, error) {
	if len(raw) > MaxPersistentPaths {
		return nil, fmt.Errorf("no máximo %d caminhos persistentes", MaxPersistentPaths)
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p != "/" {
			p = strings.TrimRight(p, "/")
		}
		switch {
		case p == "" || p == "/":
			return nil, errors.New("o caminho persistente não pode ser a raiz nem vazio")
		case !persistentPathPattern.MatchString(p):
			return nil, fmt.Errorf("caminho persistente inválido %q: use um caminho absoluto com letras, números, ponto, hífen, sublinhado e barra", p)
		case path.Clean(p) != p || strings.Contains(p, ".."):
			return nil, fmt.Errorf("caminho persistente %q não pode ter .. nem barras repetidas", p)
		}
		for _, sys := range systemPrefixes {
			if p == sys || strings.HasPrefix(p, sys+"/") {
				return nil, fmt.Errorf("o caminho %q é do sistema do contêiner; escolha um dentro da aplicação, como /app/data", p)
			}
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	for i, a := range out {
		for _, b := range out[i+1:] {
			if strings.HasPrefix(b, a+"/") {
				return nil, fmt.Errorf("%q está dentro de %q: declare só um dos dois", b, a)
			}
		}
	}
	return out, nil
}
