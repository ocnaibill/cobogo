package planner

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// staticServerImage serve os sites estáticos (DEC-37): nginx oficial sem
// root (uid 101), escutando na porta 8080, que é da plataforma.
const staticServerImage = "nginxinc/nginx-unprivileged:stable-alpine"

// staticRoot é onde o site fica na imagem: um diretório só dele, sem a
// página padrão do nginx.
const staticRoot = "/srv/site"

// StaticPlanInput são os campos resolvidos para P-STATIC.
type StaticPlanInput struct {
	// Manager é npm, bun ou none (sem build).
	Manager      string
	NodeVersion  string
	BunVersion   string
	BunLockfile  string
	BuildCommand string
	DropPrepare  bool
	OutputDir    string
	SPAFallback  bool
	// BuildArgs são as variáveis públicas (VITE_...) que entram no build.
	BuildArgs  []string
	HealthPath string
}

func staticFromAnalysis(res domain.AnalysisResult) (domain.Plan, error) {
	manager := res.Fields[domain.FieldPackageManager].Value
	out := res.Fields[domain.FieldOutputDir].Value
	if out == "" {
		return domain.Plan{}, fmt.Errorf("planner: site estático sem diretório de saída")
	}
	in := StaticPlanInput{
		Manager:      manager,
		BuildCommand: res.Fields[domain.FieldBuildCommand].Value,
		DropPrepare:  dropPrepare(res),
		OutputDir:    out,
		SPAFallback:  res.Fields[domain.FieldSPAFallback].Value == "true",
		HealthPath:   res.Fields[domain.FieldHealthPath].Value,
	}
	switch manager {
	case domain.ManagerNPM:
		in.NodeVersion = res.Fields[domain.FieldNodeVersion].Value
		if in.NodeVersion == "" {
			in.NodeVersion = domain.DefaultNodeVersion
		}
		if !slices.Contains(domain.SupportedNodeVersions, in.NodeVersion) {
			return domain.Plan{}, fmt.Errorf("planner: versão de Node %q não suportada", in.NodeVersion)
		}
	case domain.ManagerBun:
		in.BunVersion = res.Fields[domain.FieldBunVersion].Value
		if in.BunVersion == "" {
			in.BunVersion = domain.DefaultBunVersion
		}
		in.BunLockfile = bunLockfile(res)
	case domain.ManagerNone:
		in.BuildCommand = ""
	default:
		return domain.Plan{}, fmt.Errorf("planner: gerenciador %q sem template estático", manager)
	}
	if manager != domain.ManagerNone && in.BuildCommand == "" {
		return domain.Plan{}, fmt.Errorf("planner: site estático com gerenciador %s precisa de um comando de build", manager)
	}
	for _, v := range res.ConfigVariables {
		if v.Class == domain.ClassPublic {
			in.BuildArgs = append(in.BuildArgs, v.Name)
		}
	}
	slices.Sort(in.BuildArgs)
	return BuildStaticPlan(in), nil
}

// BuildStaticPlan monta o plano e o Dockerfile do site estático: a etapa de
// build do gerenciador (com dependências de desenvolvimento) e só o diretório
// de saída na imagem do nginx.
func BuildStaticPlan(in StaticPlanInput) domain.Plan {
	if in.HealthPath == "" {
		in.HealthPath = "/"
	}
	plan := domain.Plan{
		Profile:        domain.ProfileStatic,
		Runtime:        staticServerImage,
		BuildImage:     staticServerImage,
		PackageManager: in.Manager,
		BuildCommand:   in.BuildCommand,
		StartCommand:   "nginx -g 'daemon off;'",
		ExecutionMode:  domain.ModeProduction,
		Port:           8080,
		PortOrigin:     string(domain.OriginPlatform),
		HealthPath:     in.HealthPath,
		OutputDir:      in.OutputDir,
		SPAFallback:    in.SPAFallback,
		BuildArgs:      in.BuildArgs,
	}

	var b strings.Builder
	switch in.Manager {
	case domain.ManagerNPM:
		plan.BuildImage, plan.NodeVersion = nodeBuildImage(in.NodeVersion), in.NodeVersion
		plan.InstallCommand = "npm ci --include=dev"
		fmt.Fprintf(&b, "FROM %s AS build\nWORKDIR /app\nENV PATH=/app/node_modules/.bin:$PATH\n", plan.BuildImage)
		b.WriteString("COPY package.json package-lock.json ./\n")
		if in.DropPrepare {
			b.WriteString("RUN npm pkg delete scripts.prepare\n")
		}
	case domain.ManagerBun:
		plan.BuildImage, plan.BunVersion = bunBuildImage(in.BunVersion), in.BunVersion
		plan.InstallCommand = "bun install --frozen-lockfile"
		fmt.Fprintf(&b, "FROM %s AS build\nWORKDIR /app\nENV PATH=/app/node_modules/.bin:$PATH\n", plan.BuildImage)
		fmt.Fprintf(&b, "COPY package.json %s ./\n", in.BunLockfile)
		if in.DropPrepare {
			b.WriteString("RUN bun pm pkg delete scripts.prepare\n")
		}
	}
	if in.Manager != domain.ManagerNone {
		fmt.Fprintf(&b, "RUN %s\n", plan.InstallCommand)
		b.WriteString("COPY . .\n")
		// Configuração pública: vai para o bundle por definição (DEC-15).
		for _, name := range in.BuildArgs {
			fmt.Fprintf(&b, "ARG %s\n", name)
		}
		b.WriteString("ENV NODE_ENV=production\n")
		fmt.Fprintf(&b, "RUN %s\n", in.BuildCommand)
		fmt.Fprintf(&b, "RUN test -f %[1]s/index.html || { echo \"o build não gerou %[1]s/index.html; informe o diretório de saída do build (output_dir)\" >&2; exit 1; }\n\n", in.OutputDir)
	}

	if in.Manager == domain.ManagerNone {
		// Sem build o site é o próprio repositório. Os arquivos de infraestrutura
		// (Dockerfile, compose, manifestos e lockfiles, arquivos com ponto) saem
		// numa etapa à parte, para nem existirem na imagem final (DEC-37 item 8).
		b.WriteString(staticFilterStage())
	}
	fmt.Fprintf(&b, "FROM %s\n", staticServerImage)
	b.WriteString("USER root\n")
	fmt.Fprintf(&b, "RUN printf '%%s\\n' %s > /etc/nginx/conf.d/default.conf\n", nginxConf(in.SPAFallback))
	if in.Manager == domain.ManagerNone {
		fmt.Fprintf(&b, "COPY --from=site /site/ %s/\n", staticRoot)
	} else {
		fmt.Fprintf(&b, "COPY --from=build /app/%s/ %s/\n", in.OutputDir, staticRoot)
	}
	b.WriteString("USER 101\nEXPOSE 8080\n")
	plan.Dockerfile = b.String()
	return plan
}

// nginxConf devolve a configuração do servidor como argumentos de printf,
// uma linha por argumento, entre aspas simples (nenhuma linha tem aspas).
func nginxConf(spa bool) string {
	fallback := "try_files $uri $uri/ $uri.html =404;"
	notFound := "error_page 404 /404.html;"
	if spa {
		fallback, notFound = "try_files $uri $uri/ /index.html;", ""
	}
	lines := []string{
		"server {",
		"    listen 8080;",
		"    server_name _;",
		"    root " + staticRoot + ";",
		"    index index.html;",
		"    gzip on;",
		"    gzip_types text/css application/javascript application/json image/svg+xml text/plain;",
		"    add_header X-Content-Type-Options nosniff always;",
		// Arquivos com ponto (.git, .env, .github) nunca saem; só /.well-known/.
		`    location ~ /\.(?!well-known) { return 404; }`,
		"    location / { " + fallback + " }",
	}
	if notFound != "" {
		lines = append(lines, "    "+notFound)
	}
	lines = append(lines, "}")
	quoted := make([]string, len(lines))
	for i, l := range lines {
		quoted[i] = "'" + l + "'"
	}
	return strings.Join(quoted, " ")
}

// infraFiles são os nomes que não fazem parte de um site: arquivos de
// infraestrutura e de gerenciamento de dependências. É uma lista de exclusão
// e não de permissão de propósito: um site pode servir qualquer tipo de dado
// (.csv, .md, .geojson, .glb...), e uma lista de permissão o quebraria em
// silêncio (DEC-37 item 8).
var infraFiles = []string{
	"Dockerfile*", "docker-compose*", "compose.y*ml", "package.json", "package-lock.json",
	"npm-shrinkwrap.json", "bun.lock", "bun.lockb", "yarn.lock", "pnpm-lock.yaml", "node_modules",
}

// staticFilterStage é a etapa do Dockerfile que copia o repositório e tira
// dele os arquivos de infraestrutura e os que começam com ponto (exceto
// .well-known). Usa a própria imagem do nginx, que já está no catálogo.
func staticFilterStage() string {
	names := []string{"-name '.*' ! -name '.well-known'"}
	for _, n := range infraFiles {
		names = append(names, "-iname '"+n+"'")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s AS site\nUSER root\nCOPY . /site/\n", staticServerImage)
	fmt.Fprintf(&b, "RUN find /site -mindepth 1 \\( %s \\) -prune -exec rm -rf {} +\n\n", strings.Join(names, " -o "))
	return b.String()
}
