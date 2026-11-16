// Package planner resolve o plano final e gera o Dockerfile por perfil
// (§11.3 do mestre). Em M1 só existe o template do perfil P-NODE.
package planner

import (
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
	Port         int
	PortOrigin   string
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
	return BuildNodePlan(NodePlanInput{
		StartCommand: res.Fields[domain.FieldStartCommand].Value,
		BuildCommand: res.Fields[domain.FieldBuildCommand].Value,
		Port:         port,
		PortOrigin:   string(portField.Origin),
	}), nil
}

// BuildNodePlan monta o plano executável e o Dockerfile do perfil P-NODE.
func BuildNodePlan(in NodePlanInput) domain.Plan {
	dockerfile := renderNodeDockerfile(in)
	return domain.Plan{
		Profile:        "P-NODE",
		Runtime:        NodeBaseImage,
		PackageManager: "npm",
		InstallCommand: "npm ci --omit=dev",
		BuildCommand:   in.BuildCommand,
		StartCommand:   in.StartCommand,
		Port:           in.Port,
		PortOrigin:     in.PortOrigin,
		Dockerfile:     dockerfile,
	}
}

func renderNodeDockerfile(in NodePlanInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s\n", NodeBaseImage)
	b.WriteString("WORKDIR /app\n")
	b.WriteString("COPY --chown=node:node package.json package-lock.json ./\n")
	if in.BuildCommand != "" {
		// Build precisa de devDependencies; instala tudo, builda e depois
		// remove o que não é necessário em runtime.
		b.WriteString("RUN npm ci\n")
		b.WriteString("COPY --chown=node:node . .\n")
		fmt.Fprintf(&b, "RUN %s\n", in.BuildCommand)
		b.WriteString("RUN npm prune --omit=dev\n")
	} else {
		b.WriteString("RUN npm ci --omit=dev\n")
		b.WriteString("COPY --chown=node:node . .\n")
	}
	b.WriteString("USER node\n")
	fmt.Fprintf(&b, "EXPOSE %d\n", in.Port)
	fmt.Fprintf(&b, "CMD [\"sh\", \"-c\", %q]\n", in.StartCommand)
	return b.String()
}
