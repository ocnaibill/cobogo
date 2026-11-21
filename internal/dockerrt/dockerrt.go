// Package dockerrt é o adaptador Docker do worker (RF-04 a RF-06). Em M1,
// fala direto com o socket local do host; a topologia de proxies filtrados
// de DEC-19 entra quando o worker for implantado na VM do homelab — a
// interface abaixo isola essa troca futura do resto do pipeline.
package dockerrt

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"

	"github.com/ocnaibill/cobogo/internal/domain"
)

const (
	LabelManaged    = "cobogo.managed"
	LabelDeployment = "cobogo.deployment"
)

const mib = 1 << 20

// Runtime encapsula o ciclo de vida de build e execução de uma release.
type Runtime struct {
	cli    *client.Client
	limits domain.Limits
}

// WithLimits devolve um Runtime que usa os limites dados (campos zerados
// valem o padrão de fábrica). O original não muda: o worker escolhe os
// limites de cada deploy sem afetar os outros (DEC-30 item 6).
func (r *Runtime) WithLimits(l domain.Limits) *Runtime {
	return &Runtime{cli: r.cli, limits: l.WithDefaults()}
}

// New conecta ao daemon indicado por DOCKER_HOST, ou ao socket padrão do
// host quando a variável não está definida.
func New() (*Runtime, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("dockerrt: conectar ao daemon: %w", err)
	}
	return &Runtime{cli: cli, limits: domain.DefaultLimits()}, nil
}

// excludedFromContext nunca entra no contexto de build (RN-11, §11.3).
var excludedFromContext = map[string]bool{
	".git": true,
}

func isExcluded(relPath string) bool {
	first := strings.SplitN(relPath, string(filepath.Separator), 2)[0]
	if excludedFromContext[first] {
		return true
	}
	base := filepath.Base(relPath)
	return base == ".env" || strings.HasPrefix(base, ".env.") && base != ".env.example"
}

// buildContextTar empacota o diretório do workspace e substitui/injeta o
// Dockerfile gerado pelo planner, sem depender de um arquivo já existente
// no repositório (RN-14: o Dockerfile do usuário não é sobrescrito no Git,
// apenas um Dockerfile equivalente é usado no contexto de build local).
func buildContextTar(rootDir, dockerfile string) (io.Reader, error) {
	return buildContext(rootDir, dockerfile, nil)
}

// buildContext é buildContextTar com um canal para avisos (DEC-29): links que
// apontam para fora do repositório não entram, e quem constrói precisa saber.
//
// O conteúdo nunca é lido por um link simbólico. Links que ficam dentro do
// repositório entram como links; os que escapam e os arquivos que não são
// regulares (FIFO, soquete, dispositivo) são deixados de fora.
func buildContext(rootDir, dockerfile string, warn func(string)) (io.Reader, error) {
	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	defer tw.Close()

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if isExcluded(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		switch mode := info.Mode(); {
		case mode.IsDir():
			return nil
		case mode&os.ModeSymlink != 0:
			return addLinkToTar(tw, path, rel, info, warn)
		case mode.IsRegular():
			return addFileToTar(tw, path, rel, info)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("dockerrt: montar contexto de build: %w", err)
	}

	dockerfileBytes := []byte(dockerfile)
	if err := tw.WriteHeader(&tar.Header{
		Name: "Dockerfile.cobogo",
		Mode: 0o644,
		Size: int64(len(dockerfileBytes)),
	}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(dockerfileBytes); err != nil {
		return nil, err
	}

	return buf, nil
}

func addFileToTar(tw *tar.Writer, fullPath, relPath string, info os.FileInfo) error {
	f, err := os.Open(fullPath)
	if err != nil {
		return err
	}
	defer f.Close()

	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(relPath)
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// addLinkToTar grava um link simbólico como link, sem abrir o alvo. Um alvo que
// sai do repositório (absoluto, ou relativo que passa da raiz) é omitido com um
// aviso (DEC-29 itens 8 e 9).
func addLinkToTar(tw *tar.Writer, fullPath, relPath string, info os.FileInfo, warn func(string)) error {
	target, err := os.Readlink(fullPath)
	if err != nil {
		return err
	}
	if !linkStaysInside(relPath, target) {
		if warn != nil {
			warn(fmt.Sprintf("aviso: o link simbólico %q aponta para fora do repositório e não foi incluído no build", filepath.ToSlash(relPath)))
		}
		return nil
	}
	hdr, err := tar.FileInfoHeader(info, target)
	if err != nil {
		return err
	}
	hdr.Name = filepath.ToSlash(relPath)
	hdr.Size = 0
	return tw.WriteHeader(hdr)
}

// linkStaysInside diz se o alvo de um link, resolvido a partir do diretório do
// link, continua dentro da raiz do repositório. A conferência é léxica; o
// daemon confina a resolução à raiz do contexto.
func linkStaysInside(linkRel, target string) bool {
	if target == "" || filepath.IsAbs(target) {
		return false
	}
	resolved := filepath.Join(filepath.Dir(linkRel), target)
	return resolved != ".." && !strings.HasPrefix(resolved, ".."+string(filepath.Separator))
}

// BuildResult descreve a imagem produzida.
type BuildResult struct {
	ImageID   string
	ImageTag  string
	BaseImage string
}

// Build constrói a imagem a partir do workspace e do Dockerfile gerado,
// registrando logs do stream em logSink (RF-04, RF-07).
func (r *Runtime) Build(ctx context.Context, workspaceDir, dockerfile, imageTag, baseImage string, logSink func(line string)) (BuildResult, error) {
	return r.BuildWithArgs(ctx, workspaceDir, dockerfile, imageTag, baseImage, nil, nil, logSink)
}

// BuildWithArgs é o Build com argumentos de build (ARG): só configuração
// pública de site estático passa por aqui, porque vai para o bundle de todo
// jeito (DEC-37). Segredos nunca: um ARG fica no histórico da imagem.
//
// labels vão para a imagem final: com o do projeto, a retenção e a exclusão
// acham só as imagens dele (DEC-38 item 3).
func (r *Runtime) BuildWithArgs(ctx context.Context, workspaceDir, dockerfile, imageTag, baseImage string, args map[string]*string, labels map[string]string, logSink func(line string)) (BuildResult, error) {
	buildCtx, err := buildContext(workspaceDir, dockerfile, logSink)
	if err != nil {
		return BuildResult{}, err
	}

	opts := buildOptions(imageTag, r.limits)
	opts.BuildArgs = args
	opts.Labels = labels
	resp, err := r.cli.ImageBuild(ctx, buildCtx, opts)
	if err != nil {
		return BuildResult{}, fmt.Errorf("dockerrt: iniciar build: %w", err)
	}
	defer resp.Body.Close()

	if err := streamBuildLog(resp.Body, logSink); err != nil {
		return BuildResult{}, withMemoryHint(err, r.limits.BuildMemoryMB)
	}

	inspect, _, err := r.cli.ImageInspectWithRaw(ctx, imageTag)
	if err != nil {
		return BuildResult{}, fmt.Errorf("dockerrt: inspecionar imagem construída: %w", err)
	}

	return BuildResult{ImageID: inspect.ID, ImageTag: imageTag, BaseImage: baseImage}, nil
}

// buildOptions monta as opções do build, com os limites de memória e CPU
// (DEC-29 item 6).
func buildOptions(imageTag string, l domain.Limits) types.ImageBuildOptions {
	memory := int64(l.BuildMemoryMB) * mib
	const period = 100000
	return types.ImageBuildOptions{
		Tags:       []string{imageTag},
		Dockerfile: "Dockerfile.cobogo",
		Remove:     true,
		// Não consulta o registry a cada build: um Docker Hub fora do ar não
		// pode derrubar um build cujas imagens já estão no cache. As imagens
		// são garantidas antes (EnsureImage) e atualizadas pelo worker (PullImage).
		PullParent: false,
		Memory:     memory,
		MemorySwap: memory, // igual à memória: sem swap
		CPUPeriod:  period,
		CPUQuota:   int64(l.BuildCPUs * period),
	}
}

// withMemoryHint acrescenta ao erro de um passo de build que terminou com
// código 137 (SIGKILL) a causa provável: o limite de memória do build
// (DEC-30 item 11). O código 137 também pode vir de um kill manual, então o
// texto diz "provavelmente".
func withMemoryHint(err error, buildMemoryMB int) error {
	if err == nil || !strings.Contains(err.Error(), "non-zero code: 137") {
		return err
	}
	return fmt.Errorf("%w — o passo foi encerrado à força (código 137), provavelmente por exceder o limite de memória do build (%d MiB); aumente-o nas configurações", err, buildMemoryMB)
}

type buildStreamMessage struct {
	Stream      string `json:"stream"`
	ErrorDetail *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

func streamBuildLog(body io.Reader, logSink func(line string)) error {
	dec := json.NewDecoder(body)
	for {
		var msg buildStreamMessage
		if err := dec.Decode(&msg); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("dockerrt: ler stream de build: %w", err)
		}
		if msg.Stream != "" && logSink != nil {
			logSink(strings.TrimRight(msg.Stream, "\n"))
		}
		if msg.ErrorDetail != nil {
			return fmt.Errorf("dockerrt: build falhou: %s", msg.ErrorDetail.Message)
		}
	}
}

// RunResult descreve a candidata iniciada.
type RunResult struct {
	ContainerID string
	HostPort    int
}

// appHostConfig é a configuração de host de todo contêiner de aplicação:
// porta só no loopback, limites de recursos, sem capacidades e sem ganho de
// privilégio, e log com tamanho máximo (DEC-29).
func appHostConfig(portSpec nat.Port, l domain.Limits) *container.HostConfig {
	memory := int64(l.AppMemoryMB) * mib
	pids := int64(l.AppPids)
	return &container.HostConfig{
		PortBindings: nat.PortMap{
			portSpec: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "0"}},
		},
		NetworkMode: "bridge",
		AutoRemove:  false,
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges"},
		LogConfig: container.LogConfig{
			Type:   "json-file",
			Config: map[string]string{"max-size": "10m", "max-file": "3"},
		},
		Resources: container.Resources{
			Memory:     memory,
			MemorySwap: memory, // igual à memória: sem swap
			NanoCPUs:   int64(l.AppCPUs * 1e9),
			PidsLimit:  &pids,
		},
	}
}

// Run cria e inicia a candidata com a porta do contêiner publicada em uma
// porta efêmera do host (RF-06). Nenhum bind mount do host é aceito aqui:
// o repositório analisado nunca determina HostConfig (§17.2, invariante 4).
//
// Todo contêiner leva os labels cobogo.managed e cobogo.deployment, que
// identificam o que o Cobogó criou para um deploy: uma retomada (DEC-26) remove
// o que uma tentativa anterior deixou, sem tocar em recursos alheios (RF-18).
func (r *Runtime) Run(ctx context.Context, imageTag, containerName string, containerPort int, env map[string]string, deploymentID string, opts ...RunOption) (RunResult, error) {
	var spec runSpec
	for _, o := range opts {
		o(&spec)
	}
	portSpec := nat.Port(strconv.Itoa(containerPort) + "/tcp")

	envList := make([]string, 0, len(env))
	for k, v := range env {
		envList = append(envList, k+"="+v)
	}

	cfg := &container.Config{
		Image:        imageTag,
		Env:          envList,
		ExposedPorts: nat.PortSet{portSpec: struct{}{}},
		Labels:       map[string]string{},
	}
	for k, v := range spec.labels {
		cfg.Labels[k] = v
	}
	// Os labels do Cobogó vêm por último: nada os substitui (RF-18).
	cfg.Labels[LabelManaged] = "true"
	cfg.Labels[LabelDeployment] = deploymentID
	hostCfg := appHostConfig(portSpec, r.limits)
	if spec.network != "" {
		hostCfg.NetworkMode = container.NetworkMode(spec.network)
	}

	created, err := r.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, containerName)
	if err != nil {
		return RunResult{}, fmt.Errorf("dockerrt: criar contêiner: %w", err)
	}

	// Depois do create, toda falha devolve o ID do contêiner: quem chama
	// precisa dele para ler o log e limpar, por exemplo quando a aplicação cai
	// no boot e o daemon já não publica a porta.
	partial := RunResult{ContainerID: created.ID}
	if err := r.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return partial, fmt.Errorf("dockerrt: iniciar contêiner: %w", err)
	}

	inspect, err := r.cli.ContainerInspect(ctx, created.ID)
	if err != nil {
		return partial, fmt.Errorf("dockerrt: inspecionar contêiner: %w", err)
	}
	bindings, ok := inspect.NetworkSettings.Ports[portSpec]
	if !ok || len(bindings) == 0 {
		if inspect.State != nil && !inspect.State.Running {
			return partial, &ExitedError{ExitCode: inspect.State.ExitCode}
		}
		return partial, fmt.Errorf("dockerrt: porta %s não foi publicada pelo daemon", portSpec)
	}
	hostPort, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		return partial, fmt.Errorf("dockerrt: porta de host inválida: %w", err)
	}

	return RunResult{ContainerID: created.ID, HostPort: hostPort}, nil
}

// Stop e remove uma candidata que falhou readiness, preservando a anterior
// (RN-11 do pipeline: §15 passo 8).
func (r *Runtime) Remove(ctx context.Context, containerID string) error {
	timeout := 5
	_ = r.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout})
	return r.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
}

// RemoveByDeployment remove, em qualquer estado, os contêineres que o Cobogó
// criou para o deploy. Serve para retomar uma tentativa interrompida sem
// colidir com o que ela deixou e para descartar a candidata de um worker que
// perdeu a posse. Só toca contêineres com os dois labels do Cobogó.
func (r *Runtime) RemoveByDeployment(ctx context.Context, deploymentID string) error {
	list, err := r.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", LabelManaged+"=true"),
			filters.Arg("label", LabelDeployment+"="+deploymentID),
		),
	})
	if err != nil {
		return fmt.Errorf("dockerrt: listar contêineres do deploy: %w", err)
	}
	var firstErr error
	for _, c := range list {
		if err := r.Remove(ctx, c.ID); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("dockerrt: remover contêiner %s: %w", c.ID[:12], err)
		}
	}
	return firstErr
}

// EnsureImage baixa a imagem se ela ainda não está no cache do daemon, para o
// build não depender do registry quando ela já existe (DEC-26).
func (r *Runtime) EnsureImage(ctx context.Context, ref string) error {
	if _, _, err := r.cli.ImageInspectWithRaw(ctx, ref); err == nil {
		return nil
	}
	return r.PullImage(ctx, ref)
}

// PullImage baixa (ou atualiza) a imagem do registry, mesmo que já exista.
func (r *Runtime) PullImage(ctx context.Context, ref string) error {
	body, err := r.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("dockerrt: baixar %s: %w", ref, err)
	}
	defer body.Close()
	if _, err := io.Copy(io.Discard, body); err != nil {
		return fmt.Errorf("dockerrt: baixar %s: %w", ref, err)
	}
	return nil
}

// ExitedError indica que o contêiner terminou: a aplicação caiu em vez de
// ficar no ar. Não vale esperar o prazo da sonda.
type ExitedError struct{ ExitCode int }

func (e *ExitedError) Error() string {
	return fmt.Sprintf("o contêiner terminou com código de saída %d antes de responder", e.ExitCode)
}

// State devolve se o contêiner ainda roda e, se terminou, o código de saída.
func (r *Runtime) State(ctx context.Context, containerID string) (running bool, exitCode int, err error) {
	inspect, err := r.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return false, 0, fmt.Errorf("dockerrt: inspecionar contêiner: %w", err)
	}
	if inspect.State == nil {
		return false, 0, errors.New("dockerrt: o daemon não informou o estado do contêiner")
	}
	return inspect.State.Running, inspect.State.ExitCode, nil
}

// OOMKilled diz se o kernel encerrou o contêiner por falta de memória: o
// daemon informa isso com exatidão, ao contrário do código de saída 137, que
// um kill manual também produz (DEC-30 item 11).
func (r *Runtime) OOMKilled(ctx context.Context, containerID string) (bool, error) {
	inspect, err := r.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return false, fmt.Errorf("dockerrt: inspecionar contêiner: %w", err)
	}
	if inspect.State == nil {
		return false, errors.New("dockerrt: o daemon não informou o estado do contêiner")
	}
	return inspect.State.OOMKilled, nil
}

// LogTail devolve as últimas lines linhas do log do contêiner (stdout e
// stderr juntos), sem tratar o conteúdo. Quem chama redige segredos antes de
// guardá-lo (RN-11).
func (r *Runtime) LogTail(ctx context.Context, containerID string, lines int) (string, error) {
	body, err := r.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(lines),
	})
	if err != nil {
		return "", fmt.Errorf("dockerrt: ler log do contêiner: %w", err)
	}
	defer body.Close()
	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, body); err != nil {
		return "", fmt.Errorf("dockerrt: decodificar log do contêiner: %w", err)
	}
	return out.String(), nil
}

// ProbeResult é a última resposta HTTP da sonda.
type ProbeResult struct {
	Path   string
	Status int
}

// Healthy diz se a resposta foi 2xx; qualquer outra abaixo de 500 passou na
// sonda, mas merece aviso (DEC-26 item 8).
func (p ProbeResult) Healthy() bool { return p.Status >= 200 && p.Status < 300 }

const probeConsecutive = 3

// Variáveis só para os testes poderem encurtar a sonda.
var (
	probeInterval    = time.Second
	probeRequestTime = 3 * time.Second
)

// ProbeReady consulta path na porta publicada até três respostas HTTP
// consecutivas com status abaixo de 500 dentro do prazo (§15, "probe
// inicial"). Redirecionamentos não são seguidos: o que se mede é a resposta do
// próprio contêiner. O que a sonda prova é que há um servidor HTTP respondendo
// na porta certa, não que a aplicação funcione (RN-01).
//
// alive, quando não nulo, é consultado a cada rodada; se o contêiner terminou,
// a sonda falha na hora com *ExitedError em vez de esperar o prazo.
func ProbeReady(ctx context.Context, hostPort int, path string, timeout time.Duration, alive func(context.Context) (bool, int, error)) (ProbeResult, error) {
	return ProbeReadyAt(ctx, "127.0.0.1", hostPort, path, timeout, alive)
}

// ProbeReadyAt é ProbeReady para um endereço qualquer: o loopback do host (a
// porta publicada) ou o nome do contêiner na rede do projeto (DEC-32 item 5).
func ProbeReadyAt(ctx context.Context, host string, port int, path string, timeout time.Duration, alive func(context.Context) (bool, int, error)) (ProbeResult, error) {
	if path == "" {
		path = "/"
	}
	url := fmt.Sprintf("http://%s%s", net.JoinHostPort(host, strconv.Itoa(port)), path)
	client := &http.Client{
		Timeout:       probeRequestTime,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	deadline := time.Now().Add(timeout)
	consecutive := 0
	last := ProbeResult{Path: path}
	lastObservation := "sem resposta"

	for time.Now().Before(deadline) {
		if alive != nil {
			running, exitCode, err := alive(ctx)
			if err == nil && !running {
				return last, &ExitedError{ExitCode: exitCode}
			}
		}

		resp, err := client.Get(url)
		switch {
		case err != nil:
			consecutive = 0
			lastObservation = "conexão recusada ou sem resposta"
		case resp.StatusCode < 500:
			resp.Body.Close()
			last.Status = resp.StatusCode
			consecutive++
			if consecutive >= probeConsecutive {
				return last, nil
			}
			select {
			case <-ctx.Done():
				return last, ctx.Err()
			case <-time.After(probeInterval / 2):
			}
			continue
		default:
			resp.Body.Close()
			consecutive = 0
			last.Status = resp.StatusCode
			lastObservation = fmt.Sprintf("respondeu %d", resp.StatusCode)
		}

		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(probeInterval):
		}
	}
	return last, fmt.Errorf("dockerrt: readiness não confirmada em %s para %s (%s)", timeout, url, lastObservation)
}
