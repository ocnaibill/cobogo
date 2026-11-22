package analyzer

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Necessidade de banco (HEU-06, DEC-42). Heurística estática, fechada e
// explicável, só para PostgreSQL: cada pista traz arquivo e linha. Ela informa;
// criar um banco exige a escolha do usuário (RN-05).

// pgDrivers: pacotes que falam com PostgreSQL.
var pgDrivers = map[string]bool{"pg": true, "postgres": true, "pg-promise": true}

// pgVariables: nomes de variável de conexão, em ordem de preferência.
var pgVariables = []string{"DATABASE_URL", "POSTGRES_URL", "DB_URL"}

var (
	// Importação de um driver de PostgreSQL ou de um adaptador que fixa a engine.
	pgImport = regexp.MustCompile(`(?:from\s+|require\(|import\(|import\s+)\s*["'](?:pg|postgres|pg-promise|drizzle-orm/(?:node-postgres|postgres-js|pg-core|neon-http))["']`)
	// Configuração de ORMs que fixa a engine: typeorm, sequelize e knex.
	pgConfig = regexp.MustCompile(`(?:\btype|\bdialect|\bclient)\s*:\s*["'](?:postgres|postgresql|pg)["']`)
	// Schema do Prisma.
	prismaProvider = regexp.MustCompile(`(?m)^\s*provider\s*=\s*"(?:postgresql|postgres)"`)
	prismaEnvURL   = regexp.MustCompile(`url\s*=\s*env\(\s*"([A-Za-z_][A-Za-z0-9_]*)"\s*\)`)
)

// detectServiceNeeds lê o package.json, o schema do Prisma e o código de runtime.
// vars são as variáveis de ambiente que a análise já descobriu.
func detectServiceNeeds(fsys fs.FS, vars []domain.ConfigVariable) []domain.ServiceNeed {
	deps := dependencies(fsys)
	hasPgDep, pgDep, hasPrismaClient := false, "", false
	for _, d := range deps {
		if pgDrivers[d] && !hasPgDep {
			hasPgDep, pgDep = true, d
		}
		if d == "@prisma/client" || d == "prisma" {
			hasPrismaClient = true
		}
	}

	var runtimeEvidence []string
	_ = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if skippedDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !sourceExtensions[strings.ToLower(path.Ext(name))] || d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		if classifyScope(name) != domain.ScopeRuntime || isToolingPath(name) {
			return nil
		}
		data, state, rerr := readSmallFile(fsys, name)
		if rerr != nil || state != fileOK {
			return nil
		}
		for _, re := range []*regexp.Regexp{pgImport, pgConfig} {
			if loc := re.FindIndex(data); loc != nil && len(runtimeEvidence) < 3 {
				runtimeEvidence = append(runtimeEvidence, fmt.Sprintf("%s:%d", name, lineAt(data, loc[0])))
				break
			}
		}
		return nil
	})

	// Prisma: o schema fixa a engine e a variável da URL (HEU-06.2).
	var prismaEvidence []string
	prismaVar := ""
	for _, schema := range []string{"prisma/schema.prisma", "schema.prisma"} {
		data, state, err := readSmallFile(fsys, schema)
		if err != nil || state != fileOK {
			continue
		}
		if loc := prismaProvider.FindIndex(data); loc != nil {
			prismaEvidence = append(prismaEvidence, fmt.Sprintf("%s:%d", schema, lineAt(data, loc[0])))
			if m := prismaEnvURL.FindSubmatch(data); m != nil {
				prismaVar = string(m[1])
			}
		}
	}

	var evidence []string
	reason := ""
	level := ""
	switch {
	case len(runtimeEvidence) > 0:
		level, evidence = domain.NeedRequired, runtimeEvidence
		reason = "o código usa PostgreSQL"
	case len(prismaEvidence) > 0 && hasPrismaClient:
		level, evidence = domain.NeedRequired, prismaEvidence
		reason = "o schema do Prisma usa PostgreSQL"
	case hasPgDep:
		level, evidence = domain.NeedPossible, []string{"package.json"}
		reason = "a dependência " + pgDep + " fala com PostgreSQL, mas nenhum arquivo de runtime a usa"
	case len(prismaEvidence) > 0:
		level, evidence = domain.NeedPossible, prismaEvidence
		reason = "há um schema do Prisma para PostgreSQL, mas o cliente não está nas dependências"
	default:
		return []domain.ServiceNeed{}
	}

	need := domain.ServiceNeed{Engine: domain.ServicePostgres, Level: level, Reason: reason, Evidence: evidence}
	need.Style, need.Family, need.Variables = connectionStyle(prismaVar, vars)
	need.Variable = need.Variables[domain.PrimaryRole(need.Style)]
	return []domain.ServiceNeed{need}
}

// Famílias de variáveis de conexão em partes (DEC-44): o prefixo e os sufixos que
// dizem o papel. Fechadas de propósito; nome fora delas se escreve na pergunta.
var (
	partName = regexp.MustCompile(`^(POSTGRES|POSTGRESQL|PG|DB|DATABASE)_?(HOST|HOSTNAME|PORT|USER|USERNAME|PASSWORD|PASS|PWD|DB|DATABASE|NAME)$`)
	partRole = map[string]string{
		"HOST": domain.RoleHost, "HOSTNAME": domain.RoleHost, "PORT": domain.RolePort,
		"USER": domain.RoleUser, "USERNAME": domain.RoleUser,
		"PASSWORD": domain.RolePassword, "PASS": domain.RolePassword, "PWD": domain.RolePassword,
		"DB": domain.RoleDatabase, "DATABASE": domain.RoleDatabase, "NAME": domain.RoleDatabase,
	}
	// Ordem de preferência quando duas famílias empatam.
	familyOrder = []string{"POSTGRES", "PG", "DB", "DATABASE", "POSTGRESQL"}
	// Variáveis em que o código lê a CA do banco.
	caNames = []string{"POSTGRES_CA", "DB_CA", "DATABASE_CA", "DATABASE_SSL_CA", "PG_CA", "POSTGRESQL_CA"}
)

// connectionStyle decide como o código lê a conexão: a variável da URL do schema
// do Prisma ou a primeira URL conhecida (estilo `url`), ou uma família de partes
// que ele referencia (estilo `parts`), com a CA se ele a lê. A URL vence quando o
// código referencia as duas coisas: é uma variável só.
func connectionStyle(prismaVar string, vars []domain.ConfigVariable) (style, family string, variables map[string]string) {
	known := map[string]bool{}
	for _, v := range vars {
		known[v.Name] = true
	}
	urlVar := prismaVar
	if urlVar == "" {
		for _, n := range pgVariables {
			if known[n] {
				urlVar = n
				break
			}
		}
	}
	if urlVar != "" {
		return domain.StyleURL, "", map[string]string{domain.RoleURL: urlVar}
	}

	byFamily := map[string]map[string]string{}
	for _, v := range vars {
		m := partName.FindStringSubmatch(v.Name)
		if m == nil {
			continue
		}
		if byFamily[m[1]] == nil {
			byFamily[m[1]] = map[string]string{}
		}
		role := partRole[m[2]]
		if _, taken := byFamily[m[1]][role]; !taken {
			byFamily[m[1]][role] = v.Name
		}
	}
	best, bestScore := "", 0
	for _, fam := range familyOrder {
		roles := byFamily[fam]
		// Precisa do host e da senha: sem eles o banco gerenciado não tem como se ligar.
		if roles[domain.RoleHost] == "" || roles[domain.RolePassword] == "" {
			continue
		}
		if len(roles) > bestScore {
			best, bestScore = fam, len(roles)
		}
	}
	if best == "" {
		return domain.StyleURL, "", map[string]string{domain.RoleURL: pgVariables[0]}
	}
	out := byFamily[best]
	for _, n := range caNames {
		if known[n] {
			out[domain.RoleCA] = n
			break
		}
	}
	return domain.StyleParts, best, out
}
