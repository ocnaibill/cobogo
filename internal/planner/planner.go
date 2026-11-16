// Package planner resolve o plano final e gera o Dockerfile por perfil
// (§11.3 do mestre). Em M1 só existe o template do perfil P-NODE.
package planner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// NodeBaseImage é fixada no catálogo da fatia M1. Pinagem por digest é
// resolvida pelo worker no momento do pull e registrada na release; o
// Dockerfile ainda referencia a tag, limitação documentada do M1.
const NodeBaseImage = "node:20-alpine"

// NodePlanInput são os campos já resolvidos pelo analisador para P-NODE.
type NodePlanInput struct {
	StartCommand string
	BuildCommand string
	// Mode é domain.ModeProduction (padrão) ou domain.ModeDevelopment.
	Mode       string
	Port       int
	PortOrigin string
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
	return BuildNodePlan(NodePlanInput{
		StartCommand: res.Fields[domain.FieldStartCommand].Value,
		BuildCommand: res.Fields[domain.FieldBuildCommand].Value,
		Mode:         mode,
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
		Runtime:        NodeBaseImage,
		PackageManager: "npm",
		InstallCommand: install,
		BuildCommand:   in.BuildCommand,
		StartCommand:   in.StartCommand,
		ExecutionMode:  in.Mode,
		Port:           in.Port,
		PortOrigin:     in.PortOrigin,
		Dockerfile:     renderNodeDockerfile(in, install),
	}
}

func renderNodeDockerfile(in NodePlanInput, install string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s\n", NodeBaseImage)
	b.WriteString("WORKDIR /app\n")
	if in.Mode == domain.ModeDevelopment {
		b.WriteString("ENV NODE_ENV=development\n")
	} else {
		b.WriteString("ENV NODE_ENV=production\n")
	}
	// Binários instalados pelo npm (tsc, vite, next...) precisam estar no PATH
	// para os comandos dos scripts rodarem como estão, sem os ganchos
	// pre/post do `npm run` (DEC-25).
	b.WriteString("ENV PATH=/app/node_modules/.bin:$PATH\n")
	b.WriteString("COPY --chown=node:node package.json package-lock.json ./\n")
	// Com NODE_ENV=production o npm omite devDependencies por padrão, então
	// a instalação para build ou desenvolvimento as inclui explicitamente.
	fmt.Fprintf(&b, "RUN %s\n", install)
	b.WriteString("COPY --chown=node:node . .\n")
	if in.BuildCommand != "" {
		fmt.Fprintf(&b, "RUN %s\n", in.BuildCommand)
		b.WriteString("RUN npm prune --omit=dev\n")
	}
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
