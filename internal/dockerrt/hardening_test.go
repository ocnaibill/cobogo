package dockerrt

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/go-connections/nat"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func TestDefaultLimitsAreTheApprovedOnes(t *testing.T) {
	l := domain.DefaultLimits()
	if l.AppMemoryMB != 1024 || l.AppCPUs != 1 || l.AppPids != 256 || l.BuildMemoryMB != 4096 || l.BuildCPUs != 2 {
		t.Errorf("DefaultLimits = %+v (DEC-30 item 4)", l)
	}
	if got := (domain.Limits{AppMemoryMB: 2048}).WithDefaults(); got.AppMemoryMB != 2048 || got.AppPids != 256 || got.BuildCPUs != 2 {
		t.Errorf("WithDefaults must keep set fields and fill the rest: %+v", got)
	}
}

func TestWithLimitsDoesNotChangeTheOriginal(t *testing.T) {
	rt := &Runtime{limits: domain.DefaultLimits()}
	other := rt.WithLimits(domain.Limits{AppMemoryMB: 128})
	if rt.limits.AppMemoryMB != 1024 || other.limits.AppMemoryMB != 128 || other.limits.AppPids != 256 {
		t.Errorf("original %+v, derived %+v", rt.limits, other.limits)
	}
}

func TestBuildMemoryHintOnlyForExit137(t *testing.T) {
	oom := errors.New("dockerrt: build falhou: The command '/bin/sh -c npm ci' returned a non-zero code: 137")
	if got := withMemoryHint(oom, 4096).Error(); !strings.Contains(got, "4096 MiB") || !strings.Contains(got, "provavelmente") || !errors.Is(withMemoryHint(oom, 4096), oom) {
		t.Errorf("hint = %q", got)
	}
	other := errors.New("dockerrt: build falhou: The command '/bin/sh -c npm ci' returned a non-zero code: 1")
	if got := withMemoryHint(other, 4096); got != other {
		t.Errorf("an unrelated failure must pass through untouched: %v", got)
	}
	if withMemoryHint(nil, 1) != nil {
		t.Error("nil must stay nil")
	}
}

func TestAppHostConfigIsHardened(t *testing.T) {
	hc := appHostConfig(nat.Port("3000/tcp"), domain.DefaultLimits())

	if len(hc.CapDrop) != 1 || hc.CapDrop[0] != "ALL" {
		t.Errorf("CapDrop = %v, want [ALL]", hc.CapDrop)
	}
	if len(hc.SecurityOpt) != 1 || hc.SecurityOpt[0] != "no-new-privileges" {
		t.Errorf("SecurityOpt = %v", hc.SecurityOpt)
	}
	if hc.Privileged || len(hc.Binds) != 0 || len(hc.Mounts) != 0 {
		t.Errorf("never privileged and never a host mount: %+v", hc)
	}
	if hc.Memory != 1024<<20 || hc.MemorySwap != hc.Memory {
		t.Errorf("memory = %d swap = %d: the limit must be real, with no swap", hc.Memory, hc.MemorySwap)
	}
	if hc.NanoCPUs != 1_000_000_000 {
		t.Errorf("NanoCPUs = %d", hc.NanoCPUs)
	}
	if hc.PidsLimit == nil || *hc.PidsLimit != 256 {
		t.Errorf("PidsLimit = %v", hc.PidsLimit)
	}
	if hc.LogConfig.Type != "json-file" || hc.LogConfig.Config["max-size"] != "10m" || hc.LogConfig.Config["max-file"] != "3" {
		t.Errorf("LogConfig = %+v", hc.LogConfig)
	}
	if hc.RestartPolicy.Name != "" {
		t.Errorf("RestartPolicy = %+v: a restart would hide the boot failure the probe must see (DEC-29 item 4)", hc.RestartPolicy)
	}
	if hc.ReadonlyRootfs {
		t.Error("a read-only root filesystem is not a default (DEC-29 item 4)")
	}
	binding := hc.PortBindings["3000/tcp"]
	if len(binding) != 1 || binding[0].HostIP != "127.0.0.1" {
		t.Errorf("the port must stay on the loopback: %+v", binding)
	}

	custom := appHostConfig(nat.Port("80/tcp"), domain.Limits{AppMemoryMB: 128, AppCPUs: 0.5, AppPids: 64}.WithDefaults())
	if custom.Memory != 128<<20 || custom.NanoCPUs != 500_000_000 || *custom.PidsLimit != 64 {
		t.Errorf("custom limits = %d %d %d", custom.Memory, custom.NanoCPUs, *custom.PidsLimit)
	}
}

func TestBuildOptionsCarryTheBuildLimits(t *testing.T) {
	o := buildOptions("img:1", domain.DefaultLimits())
	if o.Memory != 4096<<20 || o.MemorySwap != o.Memory {
		t.Errorf("memory = %d swap = %d", o.Memory, o.MemorySwap)
	}
	if o.CPUPeriod != 100000 || o.CPUQuota != 200000 {
		t.Errorf("CPU period/quota = %d/%d, want 100000/200000 (2 CPUs)", o.CPUPeriod, o.CPUQuota)
	}
	if o.PullParent || o.Dockerfile != "Dockerfile.cobogo" || len(o.Tags) != 1 || o.Tags[0] != "img:1" {
		t.Errorf("options = %+v", o)
	}
}

func nodeCmd(t *testing.T, script string) string {
	t.Helper()
	argv, err := json.Marshal([]string{"node", "-e", script})
	if err != nil {
		t.Fatal(err)
	}
	return string(argv)
}

// waitExit espera o contêiner terminar e devolve o código de saída.
func waitExit(t *testing.T, rt *Runtime, id string) int {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		running, code, err := rt.State(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if !running {
			return code
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the container did not exit in time")
	return 0
}

// Com Docker de verdade: a configuração chega ao contêiner, o processo roda
// sem capacidades e sem poder ganhá-las, e uma aplicação sem root ainda serve
// a porta 80 (DEC-29 itens 2 e 5).
func TestRunAppliesLimitsAndDropsPrivileges(t *testing.T) {
	rt := dockerForTest(t)
	script := `const fs=require('fs');
for (const l of fs.readFileSync('/proc/self/status','utf8').split('\n')) if (/^(CapEff|CapBnd|NoNewPrivs)/.test(l)) console.log(l);
require('http').createServer((q,s)=>s.end('ok')).listen(80)`
	buildTestImage(t, rt, "cobogo-test/hardening:1", "FROM node:26-slim\nUSER node\nCMD "+nodeCmd(t, script)+"\n")

	res, err := rt.Run(context.Background(), "cobogo-test/hardening:1", "cobogo-test-hardening", 80, nil, "deploy-hardening-test")
	t.Cleanup(func() { _ = rt.Remove(context.Background(), res.ContainerID) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProbeReady(context.Background(), res.HostPort, "/", 20*time.Second, func(c context.Context) (bool, int, error) {
		return rt.State(c, res.ContainerID)
	}); err != nil {
		t.Fatalf("a non-root app without capabilities must still serve port 80: %v", err)
	}

	logs, err := rt.LogTail(context.Background(), res.ContainerID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CapEff:\t0000000000000000", "CapBnd:\t0000000000000000", "NoNewPrivs:\t1"} {
		if !strings.Contains(logs, want) {
			t.Errorf("the process must report %q, got:\n%s", want, logs)
		}
	}

	inspect, err := rt.cli.ContainerInspect(context.Background(), res.ContainerID)
	if err != nil {
		t.Fatal(err)
	}
	hc := inspect.HostConfig
	if hc.Memory != 1024<<20 || hc.MemorySwap != 1024<<20 || hc.NanoCPUs != 1e9 || hc.PidsLimit == nil || *hc.PidsLimit != 256 {
		t.Errorf("the daemon did not apply the limits: mem %d swap %d cpus %d pids %v", hc.Memory, hc.MemorySwap, hc.NanoCPUs, hc.PidsLimit)
	}
	if hc.LogConfig.Config["max-size"] != "10m" || hc.Privileged {
		t.Errorf("log %+v privileged %v", hc.LogConfig, hc.Privileged)
	}
}

// O limite de memória é real: passar dele mata o processo (código 137).
func TestAppMemoryLimitKillsTheApp(t *testing.T) {
	rt := dockerForTest(t)
	rt = rt.WithLimits(domain.Limits{AppMemoryMB: 96})
	buildTestImage(t, rt, "cobogo-test/memory:1", "FROM node:26-slim\nCMD "+nodeCmd(t, "const b=Buffer.alloc(400*1024*1024,1);console.log('sobreviveu',b.length)")+"\n")

	res, err := rt.Run(context.Background(), "cobogo-test/memory:1", "cobogo-test-memory", 3000, nil, "deploy-memory-test")
	t.Cleanup(func() { _ = rt.Remove(context.Background(), res.ContainerID) })
	if res.ContainerID == "" {
		t.Fatal(err)
	}
	if code := waitExit(t, rt, res.ContainerID); code != 137 {
		t.Errorf("exit code = %d, want 137 (killed for exceeding the memory limit)", code)
	}
	if oom, err := rt.OOMKilled(context.Background(), res.ContainerID); err != nil || !oom {
		t.Errorf("OOMKilled = %v, %v: the daemon must report the memory kill exactly", oom, err)
	}
	if logs, _ := rt.LogTail(context.Background(), res.ContainerID, 10); strings.Contains(logs, "sobreviveu") {
		t.Error("the app allocated past its limit")
	}
}

// O limite de processos contém uma bifurcação descontrolada.
func TestAppPidsLimitStopsAForkStorm(t *testing.T) {
	rt := dockerForTest(t)
	rt = rt.WithLimits(domain.Limits{AppPids: 60})
	script := `const cp=require('child_process');let ok=0,fail=0;
for (let i=0;i<300;i++){const c=cp.spawn('sleep',['30']);c.on('spawn',()=>ok++);c.on('error',()=>fail++)}
setTimeout(()=>{console.log('ok='+ok+' fail='+fail);process.exit(0)},3000)`
	buildTestImage(t, rt, "cobogo-test/pids:1", "FROM node:26-slim\nCMD "+nodeCmd(t, script)+"\n")

	res, err := rt.Run(context.Background(), "cobogo-test/pids:1", "cobogo-test-pids", 3000, nil, "deploy-pids-test")
	t.Cleanup(func() { _ = rt.Remove(context.Background(), res.ContainerID) })
	if res.ContainerID == "" {
		t.Fatal(err)
	}
	waitExit(t, rt, res.ContainerID)
	logs, _ := rt.LogTail(context.Background(), res.ContainerID, 10)
	if !strings.Contains(logs, "ok=") || strings.Contains(logs, "fail=0") {
		t.Errorf("the pids limit must make some spawns fail, got %q", logs)
	}
}

// O build também tem teto de memória.
func TestBuildMemoryLimitStopsAGreedyStep(t *testing.T) {
	rt := dockerForTest(t)
	rt = rt.WithLimits(domain.Limits{BuildMemoryMB: 128})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// O comentário único dá a cada execução uma chave de cache própria: um
	// passo guloso construído antes, sem limite, não pode ser reaproveitado.
	nonce := time.Now().Format(time.RFC3339Nano)
	dockerfile := "FROM node:26-slim\nRUN node -e \"const b=Buffer.alloc(600*1024*1024,1);console.log(b.length) // " + nonce + "\"\n"
	_, err := rt.Build(ctx, t.TempDir(), dockerfile, "cobogo-test/greedy:1", "node:26-slim", nil)
	if err == nil {
		_, _ = rt.cli.ImageRemove(context.Background(), "cobogo-test/greedy:1", image.RemoveOptions{Force: true})
		t.Fatal("a build step that exceeds the memory limit must fail")
	}
}
