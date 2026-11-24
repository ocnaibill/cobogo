package planner

import (
	"fmt"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Imagens do perfil P-BUN (DEC-36): oficiais do Bun, Debian, usuário bun
// (uid 1000), com um node de compatibilidade e sem compilador. O build roda
// na imagem completa e a execução na slim da mesma versão.
func bunBuildImage(version string) string   { return "oven/bun:" + version }
func bunRuntimeImage(version string) string { return "oven/bun:" + version + "-slim" }

// BunPlanInput são os campos já resolvidos pelo analisador para P-BUN.
type BunPlanInput struct {
	DropPrepare  bool
	StartCommand string
	BuildCommand string
	Mode         string
	// BunVersion é a tag da imagem oven/bun ("1", "1.2" ou "1.2.3").
	BunVersion string
	// Lockfile é bun.lock ou bun.lockb.
	Lockfile   string
	HealthPath string
	Port       int
	PortOrigin string
	// Unlocked instala sem lockfile (DEC-53 H): sem --frozen-lockfile.
	Unlocked bool
}

// bunLockfile acha, nas evidências da análise, o lockfile do Bun que a HEU-01
// usou (bun.lock prevalece sobre bun.lockb).
func bunLockfile(res domain.AnalysisResult) string {
	lock := ""
	for _, e := range res.Evidences {
		if e.Kind == "lockfile" && e.Name == domain.ManagerBun && (lock == "" || e.Source == "bun.lock") {
			lock = e.Source
		}
	}
	if lock == "" {
		lock = "bun.lock"
	}
	return lock
}

// BuildBunPlan monta o plano executável e o Dockerfile do perfil P-BUN.
func BuildBunPlan(in BunPlanInput) domain.Plan {
	if in.Mode == "" {
		in.Mode = domain.ModeProduction
	}
	if in.BunVersion == "" {
		in.BunVersion = domain.DefaultBunVersion
	}
	if in.HealthPath == "" {
		in.HealthPath = "/"
	}
	if in.Lockfile == "" {
		in.Lockfile = "bun.lock"
	}
	if in.Mode == domain.ModeDevelopment {
		in.BuildCommand = ""
	}
	// NODE_ENV não muda o que o bun install instala; só --production omite as
	// dependências de desenvolvimento (verificado, DEC-36 item 7).
	install := "bun install --frozen-lockfile --production"
	if in.Mode == domain.ModeDevelopment || in.BuildCommand != "" {
		install = "bun install --frozen-lockfile"
	}
	if in.Unlocked {
		install = strings.Replace(install, " --frozen-lockfile", "", 1)
	}
	return domain.Plan{
		Profile:        domain.ProfileBun,
		Runtime:        bunRuntimeImage(in.BunVersion),
		BuildImage:     bunBuildImage(in.BunVersion),
		BunVersion:     in.BunVersion,
		PackageManager: domain.ManagerBun,
		InstallCommand: install,
		BuildCommand:   in.BuildCommand,
		StartCommand:   in.StartCommand,
		ExecutionMode:  in.Mode,
		Port:           in.Port,
		PortOrigin:     in.PortOrigin,
		HealthPath:     in.HealthPath,
		Dockerfile:     renderBunDockerfile(in, install),
	}
}

func renderBunDockerfile(in BunPlanInput, install string) string {
	env := "production"
	if in.Mode == domain.ModeDevelopment {
		env = "development"
	}
	common := fmt.Sprintf("WORKDIR /app\nENV NODE_ENV=%s\nENV PATH=/app/node_modules/.bin:$PATH\n", env)

	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s AS build\n", bunBuildImage(in.BunVersion))
	b.WriteString(common)
	if in.Unlocked {
		b.WriteString("COPY package.json ./\n")
	} else {
		fmt.Fprintf(&b, "COPY package.json %s ./\n", in.Lockfile)
	}
	if in.DropPrepare {
		b.WriteString("RUN bun pm pkg delete scripts.prepare\n")
	}
	fmt.Fprintf(&b, "RUN %s\n", install)
	b.WriteString("COPY . .\n")
	if in.BuildCommand != "" {
		fmt.Fprintf(&b, "RUN %s\n", in.BuildCommand)
		// O Bun não tem prune: reinstala só as dependências de produção.
		prune := "bun install --frozen-lockfile --production"
		if in.Unlocked {
			prune = "bun install --production"
		}
		fmt.Fprintf(&b, "RUN rm -rf node_modules && %s\n", prune)
	}

	fmt.Fprintf(&b, "\nFROM %s\n", bunRuntimeImage(in.BunVersion))
	b.WriteString(common)
	b.WriteString("RUN chown bun:bun /app\n")
	b.WriteString("COPY --from=build --chown=bun:bun /app /app\n")
	b.WriteString("USER bun\n")
	fmt.Fprintf(&b, "EXPOSE %d\n", in.Port)
	fmt.Fprintf(&b, "CMD [\"sh\", \"-c\", %s]\n", jsonString(ShellStart(in.StartCommand)))
	return b.String()
}
