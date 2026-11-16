// Package planner resolve o plano final e gera o Dockerfile por perfil
// (§11.3 do mestre). Em M1 só existe o template do perfil P-NODE.
package planner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Imagens do perfil P-NODE (DEC-25): o build roda na imagem Debian completa,
// que traz compilador e python para módulos nativos; a execução, na variante
// slim, sem essas ferramentas. Ambas são da mesma distribuição (glibc), então
// binários compilados no build valem na execução. As tags acompanham o
// release mais recente da major; pinagem por digest é resolvida pelo worker no
// pull e registrada na release (limitação documentada do M1).
func nodeBuildImage(version string) string   { return "node:" + version }
func nodeRuntimeImage(version string) string { return "node:" + version + "-slim" }

// NodePlanInput são os campos já resolvidos pelo analisador para P-NODE.
type NodePlanInput struct {
	StartCommand string
	BuildCommand string
	// Mode é domain.ModeProduction (padrão) ou domain.ModeDevelopment.
	Mode string
	// HealthPath é o caminho da sonda de readiness; vazio usa "/".
	HealthPath string
	// NodeVersion é a major do Node (domain.SupportedNodeVersions); vazio usa
	// domain.DefaultNodeVersion.
	NodeVersion string
	Port        int
	PortOrigin  string
}

// FromAnalysis converte uma análise pronta no plano executável (DEC-23: o
// analisador termina nos campos resolvidos; gerar o plano é daqui).
func FromAnalysis(res domain.AnalysisResult) (domain.Plan, error) {
	if res.Outcome != domain.AnalysisReady {
		return domain.Plan{}, fmt.Errorf("planner: análise %s não pode gerar plano", res.Outcome)
	}
	if profile := res.Fields[domain.FieldProfile].Value; profile != "P-NODE" {
		return domain.Plan{}, fmt.Errorf("planner: perfil %q sem template", profile)
	}
	portField := res.Fields[domain.FieldPort]
	port, err := strconv.Atoi(portField.Value)
	if err != nil {
		return domain.Plan{}, fmt.Errorf("planner: porta inválida %q: %w", portField.Value, err)
	}
	mode := res.Fields[domain.FieldExecutionMode].Value
	if mode == "" {
		mode = domain.ModeProduction
	}
	if mode != domain.ModeProduction && mode != domain.ModeDevelopment {
		return domain.Plan{}, fmt.Errorf("planner: modo de execução %q desconhecido", mode)
	}
	version := res.Fields[domain.FieldNodeVersion].Value
	if version == "" {
		version = domain.DefaultNodeVersion
	}
	if !slices.Contains(domain.SupportedNodeVersions, version) {
		return domain.Plan{}, fmt.Errorf("planner: versão de Node %q não suportada", version)
	}
	return BuildNodePlan(NodePlanInput{
		StartCommand: res.Fields[domain.FieldStartCommand].Value,
		BuildCommand: res.Fields[domain.FieldBuildCommand].Value,
		Mode:         mode,
		NodeVersion:  version,
		HealthPath:   res.Fields[domain.FieldHealthPath].Value,
		Port:         port,
		PortOrigin:   string(portField.Origin),
	}), nil
}

// BuildNodePlan monta o plano executável e o Dockerfile do perfil P-NODE
// (DEC-25).
func BuildNodePlan(in NodePlanInput) domain.Plan {
	if in.Mode == "" {
		in.Mode = domain.ModeProduction
	}
	if in.NodeVersion == "" {
		in.NodeVersion = domain.DefaultNodeVersion
	}
	if in.HealthPath == "" {
		in.HealthPath = "/"
	}
	// Em desenvolvimento não há build nem prune: o servidor de
	// desenvolvimento compila por conta própria.
	if in.Mode == domain.ModeDevelopment {
		in.BuildCommand = ""
	}
	install := "npm ci --omit=dev"
	if in.Mode == domain.ModeDevelopment || in.BuildCommand != "" {
		install = "npm ci --include=dev"
	}
	return domain.Plan{
		Profile:        "P-NODE",
		Runtime:        nodeRuntimeImage(in.NodeVersion),
		BuildImage:     nodeBuildImage(in.NodeVersion),
		NodeVersion:    in.NodeVersion,
		PackageManager: "npm",
		InstallCommand: install,
		BuildCommand:   in.BuildCommand,
		StartCommand:   in.StartCommand,
		ExecutionMode:  in.Mode,
		Port:           in.Port,
		PortOrigin:     in.PortOrigin,
		HealthPath:     in.HealthPath,
		Dockerfile:     renderNodeDockerfile(in, install),
	}
}

func renderNodeDockerfile(in NodePlanInput, install string) string {
	env := "production"
	if in.Mode == domain.ModeDevelopment {
		env = "development"
	}
	// Binários instalados pelo npm (tsc, vite, next...) precisam estar no PATH
	// para os comandos dos scripts rodarem como estão, sem os ganchos
	// pre/post do `npm run` (DEC-25).
	common := fmt.Sprintf("WORKDIR /app\nENV NODE_ENV=%s\nENV PATH=/app/node_modules/.bin:$PATH\n", env)

	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s AS build\n", nodeBuildImage(in.NodeVersion))
	b.WriteString(common)
	b.WriteString("COPY package.json package-lock.json ./\n")
	// Com NODE_ENV=production o npm omite devDependencies por padrão, então
	// a instalação para build ou desenvolvimento as inclui explicitamente.
	fmt.Fprintf(&b, "RUN %s\n", install)
	b.WriteString("COPY . .\n")
	if in.BuildCommand != "" {
		fmt.Fprintf(&b, "RUN %s\n", in.BuildCommand)
		b.WriteString("RUN npm prune --omit=dev\n")
	}

	fmt.Fprintf(&b, "\nFROM %s\n", nodeRuntimeImage(in.NodeVersion))
	b.WriteString(common)
	b.WriteString("COPY --from=build --chown=node:node /app /app\n")
	b.WriteString("USER node\n")
	fmt.Fprintf(&b, "EXPOSE %d\n", in.Port)
	fmt.Fprintf(&b, "CMD [\"sh\", \"-c\", %s]\n", jsonString(in.StartCommand))
	return b.String()
}

// jsonString escreve s como string JSON, que é o que o formato exec do
// Dockerfile exige (o %q do Go difere de JSON em alguns caracteres).
func jsonString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s) // só falha com tipos não serializáveis
	return strings.TrimRight(buf.String(), "\n")
}

// CatalogImages lista as imagens que o catálogo pode usar (todas as versões
// suportadas, de build e de execução), para o worker baixá-las antes do
// primeiro build.
func CatalogImages() []string {
	var images []string
	for _, v := range domain.SupportedNodeVersions {
		images = append(images, nodeBuildImage(v), nodeRuntimeImage(v))
	}
	return images
}
