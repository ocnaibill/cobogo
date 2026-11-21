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

	return []domain.ServiceNeed{{
		Engine: domain.ServicePostgres, Level: level, Variable: connectionVariable(prismaVar, vars), Reason: reason, Evidence: evidence,
	}}
}

// connectionVariable escolhe a variável da URL: a do schema do Prisma; senão a
// primeira da lista que o código referencia; senão DATABASE_URL.
func connectionVariable(prismaVar string, vars []domain.ConfigVariable) string {
	if prismaVar != "" {
		return prismaVar
	}
	known := map[string]bool{}
	for _, v := range vars {
		known[v.Name] = true
	}
	for _, n := range pgVariables {
		if known[n] {
			return n
		}
	}
	return pgVariables[0]
}
