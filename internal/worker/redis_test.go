package worker

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/settings"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// O Redis gerenciado de verdade (DEC-50 C): sobe com a imagem fixada, exige a senha, persiste em
// AOF no volume (os dados sobrevivem a um reinício do contêiner), nunca descarta uma chave por
// falta de memória e convive com o banco relacional do mesmo projeto. Precisa de Docker.
func TestRedisManagedEndToEnd(t *testing.T) {
	if os.Getenv("COBOGO_TEST_DOCKER") != "1" {
		t.Skip("defina COBOGO_TEST_DOCKER=1 para rodar testes com Docker")
	}
	rt, err := dockerrt.New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	st := storetest.New(t)
	admin, err := st.CreateAdmin(ctx, "dono-rd", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, admin.ID, "redis-real", "https://github.com/x/redis-real", "")
	if err != nil {
		t.Fatal(err)
	}
	pid := p.ID.String()
	const pass = "senha-do-redis-7788"
	host := domain.ServiceContainerNameFor(pid, domain.ServiceRedis)
	if host != "cobogo-redis-"+strings.ReplaceAll(pid, "-", "")[:12] {
		t.Fatalf("nome do contêiner = %s", host)
	}
	svc, err := st.ChooseDatabaseFor(ctx, domain.ServiceRedis, p.ID, admin.ID, domain.ServiceManaged,
		map[string]string{"url": "CELERY_BROKER_URL"}, map[string]string{"url": "redis://:" + pass + "@" + host + ":6379/0"})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Engine != domain.ServiceRedis || svc.TLS || svc.VolumeName != domain.ServiceVolumeNameFor(pid, domain.ServiceRedis) {
		t.Fatalf("serviço = %+v", svc)
	}
	w := &Worker{store: st, runtime: rt, log: quiet, settings: settings.NewResolver(st, settings.Env{})}
	net := dockerrt.ProjectNetwork(pid)
	t.Cleanup(func() {
		_ = rt.RemoveService(context.Background(), pid, svc.ContainerName, svc.VolumeName)
		_ = rt.RemoveProjectNetwork(context.Background(), pid)
	})
	if _, err := w.provisionService(ctx, rt, p, svc); err != nil {
		t.Fatalf("provisionar: %v", err)
	}
	cli := func(args ...string) (string, error) {
		out, err := exec.Command("docker", append([]string{"run", "--rm", "--network", net, dockerrt.RedisImage, "redis-cli", "--no-auth-warning", "-h", host}, args...)...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	// Sem a senha, nada entra.
	if out, _ := cli("ping"); !strings.Contains(out, "NOAUTH") {
		t.Errorf("sem senha: %q (esperava NOAUTH)", out)
	}
	if out, _ := cli("-a", "outra-senha", "ping"); !strings.Contains(out, "WRONGPASS") {
		t.Errorf("senha errada: %q", out)
	}
	if out, err := cli("-a", pass, "ping"); err != nil || out != "PONG" {
		t.Fatalf("com a senha: %q %v", out, err)
	}
	if out, _ := cli("-a", pass, "config", "get", "appendonly"); !strings.Contains(out, "yes") {
		t.Errorf("AOF = %q", out)
	}
	if out, _ := cli("-a", pass, "config", "get", "maxmemory-policy"); !strings.Contains(out, "noeviction") {
		t.Errorf("política de memória = %q", out)
	}
	// O servidor não roda como root.
	if out, err := exec.Command("docker", "exec", host, "sh", "-c", "ps -o user= -C redis-server 2>/dev/null || ps | grep redis-server | head -1").CombinedOutput(); err == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "root") && !strings.Contains(string(out), "redis") {
		t.Errorf("redis-server como root: %s", out)
	}
	// Sem porta publicada.
	if out, _ := exec.Command("docker", "port", host).CombinedOutput(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("o Redis publicou uma porta: %s", out)
	}
	// Os dados sobrevivem a um reinício do contêiner (AOF no volume).
	if out, err := cli("-a", pass, "set", "tarefa:1", "enviar o certificado"); err != nil || out != "OK" {
		t.Fatalf("set: %q %v", out, err)
	}
	time.Sleep(2 * time.Second) // appendfsync everysec
	if out, err := exec.Command("docker", "restart", host).CombinedOutput(); err != nil {
		t.Fatalf("restart: %v %s", err, out)
	}
	if err := rt.WaitHealthy(ctx, host, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	if out, _ := cli("-a", pass, "get", "tarefa:1"); out != "enviar o certificado" {
		t.Errorf("depois do reinício: %q", out)
	}
	// A restauração e o backup por dump não valem para o Redis.
	if err := rt.DumpService(ctx, domain.ServiceRedis, pid, "b", host, pass, net, "x", "", "x", time.Second); err == nil {
		t.Error("um dump de Redis foi aceito")
	}
}
