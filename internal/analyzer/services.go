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

// mysqlDrivers: pacotes que falam com MySQL; mysqlVariables, os nomes de URL, em ordem de preferência.
var (
	mysqlDrivers   = map[string]bool{"mysql2": true, "mysql": true}
	mysqlVariables = []string{"DATABASE_URL", "MYSQL_URL", "DB_URL", "CLEARDB_DATABASE_URL", "JAWSDB_URL"}
)

var (
	// Importação de um driver de PostgreSQL ou de um adaptador que fixa a engine.
	pgImport = regexp.MustCompile(`(?:from\s+|require\(|import\(|import\s+)\s*["'](?:pg|postgres|pg-promise|drizzle-orm/(?:node-postgres|postgres-js|pg-core|neon-http))["']`)
	// Configuração de ORMs que fixa a engine: typeorm, sequelize e knex.
	pgConfig = regexp.MustCompile(`(?:\btype|\bdialect|\bclient)\s*:\s*["'](?:postgres|postgresql|pg)["']`)
	// MySQL e MariaDB (DEC-50): o driver ou o adaptador que fixa a engine.
	mysqlImport = regexp.MustCompile(`(?:from\s+|require\(|import\(|import\s+)\s*["'](?:mysql2|mysql2/promise|mysql|drizzle-orm/mysql2|drizzle-orm/mysql-core)["']`)
	mysqlConfig = regexp.MustCompile(`(?:\btype|\bdialect|\bclient)\s*:\s*["'](?:mysql|mysql2|mariadb)["']`)
	prismaMySQL = regexp.MustCompile(`(?m)^\s*provider\s*=\s*"mysql"`)
	// Schema do Prisma.
	prismaProvider = regexp.MustCompile(`(?m)^\s*provider\s*=\s*"(?:postgresql|postgres)"`)
	prismaEnvURL   = regexp.MustCompile(`url\s*=\s*env\(\s*"([A-Za-z_][A-Za-z0-9_]*)"\s*\)`)
)

// detectServiceNeeds lê o package.json, o schema do Prisma e o código de runtime.
// vars são as variáveis de ambiente que a análise já descobriu. Devolve uma necessidade
// por engine relacional que o código usa (PostgreSQL e MySQL, DEC-50).
func detectServiceNeeds(fsys fs.FS, vars []domain.ConfigVariable) []domain.ServiceNeed {
	needs := detectPostgresNeeds(fsys, vars)
	needs = append(needs, detectMySQLNeeds(fsys, vars)...)
	return append(needs, detectJSRedisNeeds(fsys, vars)...)
}

// detectJSRedisNeeds é a HEU-06 para Redis no JavaScript: o import do cliente ou o endereço
// `redis://` escrito no código (forte); o cliente só nas dependências (possível).
func detectJSRedisNeeds(fsys fs.FS, vars []domain.ConfigVariable) []domain.ServiceNeed {
	dep := ""
	for _, d := range dependencies(fsys) {
		if jsRedisDeps[d] && dep == "" {
			dep = d
		}
	}
	var strong, fixed []string
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
		if !sourceExtensions[strings.ToLower(path.Ext(name))] || d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() || classifyScope(name) != domain.ScopeRuntime || isToolingPath(name) {
			return nil
		}
		data, state, rerr := readSmallFile(fsys, name)
		if rerr != nil || state != fileOK {
			return nil
		}
		if loc := jsRedisImport.FindIndex(data); loc != nil && len(strong) < 3 {
			strong = append(strong, fmt.Sprintf("%s:%d", name, lineAt(data, loc[0])))
		}
		for _, m := range redisURLLiteral.FindAllSubmatchIndex(data, -1) {
			if _, _, ok := redisAddress(string(data[m[2]:m[3]])); ok && len(fixed) < 3 {
				ref := fmt.Sprintf("%s:%d", name, lineAt(data, m[0]))
				fixed = append(fixed, ref)
				if len(strong) < 3 {
					strong = append(strong, ref)
				}
			}
		}
		return nil
	})
	return buildRedisNeed(strong, dep, "package.json", fixed, vars)
}

// detectMySQLNeeds é a HEU-06 para MySQL e MariaDB: o driver, o adaptador ou o schema do Prisma.
func detectMySQLNeeds(fsys fs.FS, vars []domain.ConfigVariable) []domain.ServiceNeed {
	deps := dependencies(fsys)
	dep, prismaClient := "", false
	for _, d := range deps {
		if mysqlDrivers[d] && dep == "" {
			dep = d
		}
		if d == "@prisma/client" || d == "prisma" {
			prismaClient = true
		}
	}
	var runtime, prismaEv []string
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
		if !sourceExtensions[strings.ToLower(path.Ext(name))] || d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() || classifyScope(name) != domain.ScopeRuntime || isToolingPath(name) {
			return nil
		}
		data, state, rerr := readSmallFile(fsys, name)
		if rerr != nil || state != fileOK {
			return nil
		}
		for _, re := range []*regexp.Regexp{mysqlImport, mysqlConfig} {
			if loc := re.FindIndex(data); loc != nil && len(runtime) < 3 {
				runtime = append(runtime, fmt.Sprintf("%s:%d", name, lineAt(data, loc[0])))
				break
			}
		}
		return nil
	})
	prismaVar := ""
	for _, schema := range []string{"prisma/schema.prisma", "schema.prisma"} {
		data, state, err := readSmallFile(fsys, schema)
		if err != nil || state != fileOK {
			continue
		}
		if loc := prismaMySQL.FindIndex(data); loc != nil {
			prismaEv = append(prismaEv, fmt.Sprintf("%s:%d", schema, lineAt(data, loc[0])))
			if m := prismaEnvURL.FindSubmatch(data); m != nil {
				prismaVar = string(m[1])
			}
		}
	}
	var evidence []string
	level, reason := "", ""
	switch {
	case len(runtime) > 0:
		level, evidence, reason = domain.NeedRequired, runtime, "o código usa MySQL"
	case len(prismaEv) > 0 && prismaClient:
		level, evidence, reason = domain.NeedRequired, prismaEv, "o schema do Prisma usa MySQL"
	case dep != "":
		level, evidence, reason = domain.NeedPossible, []string{"package.json"}, "a dependência "+dep+" fala com MySQL, mas nenhum arquivo de runtime a usa"
	default:
		return nil
	}
	need := domain.ServiceNeed{Engine: domain.ServiceMySQL, Level: level, Reason: reason, Evidence: evidence}
	need.Style, need.Family, need.Variables = connectionStyleFor(domain.ServiceMySQL, prismaVar, vars)
	need.Variable = need.Variables[domain.PrimaryRole(need.Style)]
	return []domain.ServiceNeed{need}
}

func detectPostgresNeeds(fsys fs.FS, vars []domain.ConfigVariable) []domain.ServiceNeed {
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
	partName      = regexp.MustCompile(`^(POSTGRES|POSTGRESQL|PG|DB|DATABASE)_?(HOST|HOSTNAME|PORT|USER|USERNAME|PASSWORD|PASS|PWD|DB|DATABASE|NAME)$`)
	mysqlPartName = regexp.MustCompile(`^(MYSQL|DB|DATABASE)_?(HOST|HOSTNAME|PORT|USER|USERNAME|PASSWORD|PASS|PWD|DB|DATABASE|NAME)$`)
	partRole      = map[string]string{
		"HOST": domain.RoleHost, "HOSTNAME": domain.RoleHost, "PORT": domain.RolePort,
		"USER": domain.RoleUser, "USERNAME": domain.RoleUser,
		"PASSWORD": domain.RolePassword, "PASS": domain.RolePassword, "PWD": domain.RolePassword,
		"DB": domain.RoleDatabase, "DATABASE": domain.RoleDatabase, "NAME": domain.RoleDatabase,
	}
	// Ordem de preferência quando duas famílias empatam.
	familyOrder      = []string{"POSTGRES", "PG", "DB", "DATABASE", "POSTGRESQL"}
	mysqlFamilyOrder = []string{"MYSQL", "DB", "DATABASE"}
	// Variáveis em que o código lê a CA do banco.
	caNames = []string{"POSTGRES_CA", "DB_CA", "DATABASE_CA", "DATABASE_SSL_CA", "PG_CA", "POSTGRESQL_CA"}
)

// connectionStyle decide como o código lê a conexão: a variável da URL do schema
// do Prisma ou a primeira URL conhecida (estilo `url`), ou uma família de partes
// que ele referencia (estilo `parts`), com a CA se ele a lê. A URL vence quando o
// código referencia as duas coisas: é uma variável só.
func connectionStyle(prismaVar string, vars []domain.ConfigVariable) (style, family string, variables map[string]string) {
	return connectionStyleFor(domain.ServicePostgres, prismaVar, vars)
}

// connectionStyleFor é connectionStyle para uma engine: os nomes de URL e as famílias de
// partes mudam (MYSQL_*, JAWSDB_URL…), a lógica é a mesma. O MySQL não tem CA por variável.
func connectionStyleFor(engine, prismaVar string, vars []domain.ConfigVariable) (style, family string, variables map[string]string) {
	urlNames, parts, order := pgVariables, partName, familyOrder
	switch engine {
	case domain.ServiceMySQL:
		urlNames, parts, order = mysqlVariables, mysqlPartName, mysqlFamilyOrder
	case domain.ServiceRedis:
		urlNames, parts, order = redisVariables, redisPartName, redisFamilyOrder
	}
	known := map[string]bool{}
	for _, v := range vars {
		known[v.Name] = true
	}
	urlVar := prismaVar
	if urlVar == "" {
		for _, n := range urlNames {
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
		m := parts.FindStringSubmatch(v.Name)
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
	for _, fam := range order {
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
		return domain.StyleURL, "", map[string]string{domain.RoleURL: urlNames[0]}
	}
	out := byFamily[best]
	if engine == domain.ServiceRedis {
		delete(out, domain.RoleUser)
		delete(out, domain.RoleDatabase)
	}
	if engine == domain.ServicePostgres {
		for _, n := range caNames {
			if known[n] {
				out[domain.RoleCA] = n
				break
			}
		}
	}
	return domain.StyleParts, best, out
}
