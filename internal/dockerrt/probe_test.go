package dockerrt

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func init() {
	probeInterval = 10 * time.Millisecond
	probeRequestTime = time.Second
}

func portOf(t *testing.T, srv *httptest.Server) int {
	t.Helper()
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

func serve(t *testing.T, h http.HandlerFunc) int {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return portOf(t, srv)
}

func TestProbeAcceptsAnyStatusBelow500(t *testing.T) {
	for _, tc := range []struct {
		status  int
		healthy bool
	}{{200, true}, {204, true}, {301, false}, {401, false}, {403, false}, {404, false}, {499, false}} {
		port := serve(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status) })
		got, err := ProbeReady(context.Background(), port, "/", 5*time.Second, nil)
		if err != nil {
			t.Errorf("status %d: %v", tc.status, err)
			continue
		}
		if got.Status != tc.status || got.Healthy() != tc.healthy || got.Path != "/" {
			t.Errorf("status %d: result %+v, healthy want %v", tc.status, got, tc.healthy)
		}
	}
}

func TestProbeRejectsServerErrors(t *testing.T) {
	port := serve(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	got, err := ProbeReady(context.Background(), port, "/", 200*time.Millisecond, nil)
	if err == nil || !strings.Contains(err.Error(), "respondeu 503") {
		t.Errorf("err = %v, want a timeout that says what the server answered", err)
	}
	if got.Status != 503 {
		t.Errorf("last status = %d", got.Status)
	}
}

func TestProbeNeedsConsecutiveSuccesses(t *testing.T) {
	var calls atomic.Int32
	port := serve(t, func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 2 { // uma falha no meio zera a contagem
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(200)
	})
	if _, err := ProbeReady(context.Background(), port, "/", 5*time.Second, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 5 {
		t.Errorf("%d requests: a failure in the middle must restart the count of 3 consecutive successes", calls.Load())
	}
}

// Redirecionamentos não são seguidos: o que se mede é a resposta do contêiner.
func TestProbeDoesNotFollowRedirects(t *testing.T) {
	port := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		w.WriteHeader(500) // se fosse seguido, a sonda falharia
	})
	got, err := ProbeReady(context.Background(), port, "/", 5*time.Second, nil)
	if err != nil || got.Status != http.StatusFound {
		t.Errorf("result %+v, err %v; want the 302 itself", got, err)
	}
}

func TestProbeUsesTheConfiguredPath(t *testing.T) {
	var paths atomic.Value
	paths.Store("")
	port := serve(t, func(w http.ResponseWriter, r *http.Request) {
		paths.Store(r.URL.RequestURI())
		if r.URL.Path != "/healthz" {
			w.WriteHeader(500)
		}
	})
	got, err := ProbeReady(context.Background(), port, "/healthz?full=1", 5*time.Second, nil)
	if err != nil || got.Path != "/healthz?full=1" || paths.Load() != "/healthz?full=1" {
		t.Errorf("result %+v, err %v, requested %v", got, err, paths.Load())
	}
}

func TestProbeFailsWhenNothingListens(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	port := portOf(t, srv)
	srv.Close()
	if _, err := ProbeReady(context.Background(), port, "/", 150*time.Millisecond, nil); err == nil || !strings.Contains(err.Error(), "readiness não confirmada") {
		t.Errorf("err = %v", err)
	}
}

// Se o contêiner terminou, não adianta esperar o prazo.
func TestProbeFailsFastWhenTheContainerExited(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	port := portOf(t, srv)
	srv.Close()

	start := time.Now()
	_, err := ProbeReady(context.Background(), port, "/", time.Minute, func(context.Context) (bool, int, error) { return false, 3, nil })
	var exited *ExitedError
	if !errors.As(err, &exited) || exited.ExitCode != 3 {
		t.Fatalf("err = %v, want ExitedError with code 3", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %s: it must not wait for the timeout", time.Since(start))
	}
	if !strings.Contains(err.Error(), "código de saída 3") {
		t.Errorf("message = %q", err)
	}
}

func TestProbeIgnoresAFailingAliveCheck(t *testing.T) {
	port := serve(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := ProbeReady(context.Background(), port, "/", 5*time.Second, func(context.Context) (bool, int, error) {
		return false, 0, errors.New("daemon indisponível")
	})
	if err != nil {
		t.Errorf("an error asking the daemon must not be read as 'exited': %v", err)
	}
}

func TestProbeStopsOnContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	port := portOf(t, srv)
	srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	start := time.Now()
	_, err := ProbeReady(ctx, port, "/", time.Minute, nil)
	if !errors.Is(err, context.Canceled) || time.Since(start) > 2*time.Second {
		t.Errorf("err = %v after %s", err, time.Since(start))
	}
}

func TestProbeReadyAtUsesTheGivenHost(t *testing.T) {
	port := serve(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	res, err := ProbeReadyAt(context.Background(), "127.0.0.1", port, "/", 5*time.Second, nil)
	if err != nil || res.Status != 200 {
		t.Fatalf("probe = %+v, %v", res, err)
	}
	// Um nome que não resolve nunca passa: a sonda tem de falhar, não aprovar.
	if _, err := ProbeReadyAt(context.Background(), "contêiner-que-nao-existe.invalid", port, "/", 1500*time.Millisecond, nil); err == nil {
		t.Error("an unresolvable host must not pass the probe")
	}
}
