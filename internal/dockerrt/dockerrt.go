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
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

// Runtime encapsula o ciclo de vida de build e execução de uma release.
type Runtime struct {
	cli *client.Client
}

// New conecta ao daemon indicado por DOCKER_HOST, ou ao socket padrão do
// host quando a variável não está definida.
func New() (*Runtime, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("dockerrt: conectar ao daemon: %w", err)
	}
	return &Runtime{cli: cli}, nil
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
		if info.IsDir() {
			return nil
		}
		return addFileToTar(tw, path, rel, info)
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

// BuildResult descreve a imagem produzida.
type BuildResult struct {
	ImageID   string
	ImageTag  string
	BaseImage string
}

// Build constrói a imagem a partir do workspace e do Dockerfile gerado,
// registrando logs do stream em logSink (RF-04, RF-07).
func (r *Runtime) Build(ctx context.Context, workspaceDir, dockerfile, imageTag, baseImage string, logSink func(line string)) (BuildResult, error) {
	buildCtx, err := buildContextTar(workspaceDir, dockerfile)
	if err != nil {
		return BuildResult{}, err
	}

	resp, err := r.cli.ImageBuild(ctx, buildCtx, types.ImageBuildOptions{
		Tags:       []string{imageTag},
		Dockerfile: "Dockerfile.cobogo",
		Remove:     true,
		PullParent: true,
	})
	if err != nil {
		return BuildResult{}, fmt.Errorf("dockerrt: iniciar build: %w", err)
	}
	defer resp.Body.Close()

	if err := streamBuildLog(resp.Body, logSink); err != nil {
		return BuildResult{}, err
	}

	inspect, _, err := r.cli.ImageInspectWithRaw(ctx, imageTag)
	if err != nil {
		return BuildResult{}, fmt.Errorf("dockerrt: inspecionar imagem construída: %w", err)
	}

	return BuildResult{ImageID: inspect.ID, ImageTag: imageTag, BaseImage: baseImage}, nil
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

// Run cria e inicia a candidata com a porta do contêiner publicada em uma
// porta efêmera do host (RF-06). Nenhum bind mount do host é aceito aqui:
// o repositório analisado nunca determina HostConfig (§17.2, invariante 4).
func (r *Runtime) Run(ctx context.Context, imageTag, containerName string, containerPort int, env map[string]string) (RunResult, error) {
	portSpec := nat.Port(strconv.Itoa(containerPort) + "/tcp")

	envList := make([]string, 0, len(env))
	for k, v := range env {
		envList = append(envList, k+"="+v)
	}

	cfg := &container.Config{
		Image:        imageTag,
		Env:          envList,
		ExposedPorts: nat.PortSet{portSpec: struct{}{}},
		Labels: map[string]string{
			"cobogo.managed": "true",
		},
	}
	hostCfg := &container.HostConfig{
		PortBindings: nat.PortMap{
			portSpec: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "0"}},
		},
		NetworkMode: "bridge",
		AutoRemove:  false,
	}

	created, err := r.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, containerName)
	if err != nil {
		return RunResult{}, fmt.Errorf("dockerrt: criar contêiner: %w", err)
	}

	if err := r.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return RunResult{}, fmt.Errorf("dockerrt: iniciar contêiner: %w", err)
	}

	inspect, err := r.cli.ContainerInspect(ctx, created.ID)
	if err != nil {
		return RunResult{}, fmt.Errorf("dockerrt: inspecionar contêiner: %w", err)
	}
	bindings, ok := inspect.NetworkSettings.Ports[portSpec]
	if !ok || len(bindings) == 0 {
		return RunResult{}, fmt.Errorf("dockerrt: porta %s não foi publicada pelo daemon", portSpec)
	}
	hostPort, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		return RunResult{}, fmt.Errorf("dockerrt: porta de host inválida: %w", err)
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

// ProbeReady consulta repetidamente path na porta publicada até três
// respostas válidas consecutivas dentro do prazo (§15, "probe inicial").
func ProbeReady(ctx context.Context, hostPort int, path string, expectedStatus int, timeout time.Duration) error {
	if path == "" {
		path = "/"
	}
	if expectedStatus == 0 {
		expectedStatus = http.StatusOK
	}
	url := fmt.Sprintf("http://127.0.0.1:%d%s", hostPort, path)

	deadline := time.Now().Add(timeout)
	consecutive := 0
	httpClient := &http.Client{Timeout: 3 * time.Second}

	for time.Now().Before(deadline) {
		resp, err := httpClient.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == expectedStatus {
				consecutive++
				if consecutive >= 3 {
					return nil
				}
				time.Sleep(500 * time.Millisecond)
				continue
			}
		}
		consecutive = 0
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("dockerrt: readiness não confirmada em %s para %s", timeout, url)
}
