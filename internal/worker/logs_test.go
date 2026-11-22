package worker

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/redact"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

func TestCleanLineRedactsCleansAndLimits(t *testing.T) {
	got := cleanLine("\x1b[31merro\x1b[0m: token=valor-canario-123 e \x00lixo", []string{"valor-canario-123"})
	if strings.Contains(got, "valor-canario-123") || strings.Contains(got, "\x1b") || strings.Contains(got, "\x00") || !strings.Contains(got, redact.Placeholder) {
		t.Errorf("clean = %q", got)
	}
	long := cleanLine(strings.Repeat("é", domain.LogLineMax), nil)
	if len(long) > domain.LogLineMax+len(logLineSuffix) || !strings.HasSuffix(long, logLineSuffix) || strings.ContainsRune(long, '�') {
		t.Errorf("a long line must be cut on a rune boundary and marked (%d bytes)", len(long))
	}
	if short := cleanLine("curta", nil); short != "curta" {
		t.Errorf("short = %q", short)
	}
}

// Captura de verdade (DEC-46): stdout e stderr de um contêiner de projeto chegam
// ao banco redigidos e com a origem certa, o limite de taxa descarta e avisa, e
// religar o laço retoma sem repetir. Precisa de Docker (COBOGO_TEST_DOCKER=1).
func TestRuntimeLogCaptureEndToEnd(t *testing.T) {
	if os.Getenv("COBOGO_TEST_DOCKER") != "1" {
		t.Skip("defina COBOGO_TEST_DOCKER=1 para rodar testes com Docker")
	}
	rt, err := dockerrt.New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := rt.EnsureImage(ctx, "node:26-slim"); err != nil {
		t.Fatal(err)
	}
	const secret = "valor-secreto-do-log-7788"
	script := `sleep 3; echo inicio; echo erro-no-stderr >&2; echo "segredo=` + secret + `"; i=0; while [ $i -lt 3000 ]; do echo flood-$i; i=$((i+1)); done; sleep 1; echo fim; sleep 120`
	build := exec.CommandContext(ctx, "docker", "build", "-q", "-t", "cobogo-test/logger:1", "-")
	build.Stdin = strings.NewReader("FROM node:26-slim\nCMD [\"sh\", \"-c\", " + jsonString(script) + "]\n")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %s %v", out, err)
	}

	st := storetest.New(t)
	admin, err := st.CreateAdmin(ctx, "dono-logs", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, admin.ID, "logs", "https://github.com/x/logs", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutEnvVariable(ctx, p.ID, "SEGREDO", secret, domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}
	oldRec, oldFlush := logReconcileEvery, logFlushEvery
	logReconcileEvery, logFlushEvery = 300*time.Millisecond, 300*time.Millisecond
	t.Cleanup(func() { logReconcileEvery, logFlushEvery = oldRec, oldFlush })
	w := &Worker{store: st, runtime: rt, log: quiet}

	capture := func() (stop func()) {
		cctx, stopCtx := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); w.runLogCapture(cctx) }()
		return func() { stopCtx(); <-done }
	}
	read := func() []domain.LogLine {
		page, err := st.ListRuntimeLogsFor(ctx, p.ID, admin.ID, "", 0, 0, 100000)
		if err != nil {
			t.Fatal(err)
		}
		return page.Lines
	}
	has := func(lines []domain.LogLine, text string) bool {
		for _, l := range lines {
			if strings.Contains(l.Line, text) {
				return true
			}
		}
		return false
	}

	stop := capture()
	dep := uuid.New()
	res, err := rt.Run(ctx, "cobogo-test/logger:1", "cobogo-test-logger", 3000, nil, dep.String(), dockerrt.WithLabels(map[string]string{dockerrt.LabelProject: p.ID.String()}))
	if res.ContainerID != "" {
		t.Cleanup(func() { _ = rt.Remove(context.Background(), res.ContainerID) })
	}
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(60 * time.Second)
	for !has(read(), "fim") && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
	}
	lines := read()
	if !has(lines, "fim") {
		t.Fatalf("the captured log never reached the last line (%d lines)", len(lines))
	}
	var stderrSeen, dropNote bool
	count := map[string]int{}
	for _, l := range lines {
		count[l.Line]++
		if l.Line == "erro-no-stderr" && l.Stream == domain.LogStderr {
			stderrSeen = true
		}
		if l.Stream == domain.LogSystem && strings.Contains(l.Line, "descartadas") {
			dropNote = true
		}
		if l.Origin != domain.LogOriginApp || l.ContainerName != "cobogo-test-logger" || l.DeploymentID == nil || *l.DeploymentID != dep {
			t.Fatalf("line metadata = %+v", l)
		}
	}
	if has(lines, secret) || !has(lines, "segredo="+redact.Placeholder) {
		t.Errorf("the secret must be redacted in the stored log")
	}
	if !stderrSeen || count["inicio"] != 1 {
		t.Errorf("stderr=%v, inicio=%d", stderrSeen, count["inicio"])
	}
	if !dropNote {
		t.Error("the rate limit must drop lines and say how many")
	}
	stop()

	// Religar o laço retoma de onde parou: nada se repete.
	before := len(read())
	stop = capture()
	time.Sleep(3 * time.Second)
	stop()
	if after := read(); len(after) != before || count["inicio"] != 1 {
		t.Errorf("resuming must not repeat lines: %d -> %d", before, len(after))
	}
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
