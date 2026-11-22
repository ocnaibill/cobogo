package dockerrt

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Logs de execução (DEC-46): o worker segue o stdout e o stderr dos contêineres do
// Cobogó pela mesma rota de logs que o diagnóstico de falha já usa.

// LogEntry é uma linha de log com a hora que o Docker registrou.
type LogEntry struct {
	At     time.Time
	Stream string
	Line   string
}

// lineSplitter junta os pedaços que o Docker entrega e emite uma linha por vez.
type lineSplitter struct {
	stream string
	buf    []byte
	emit   func(LogEntry)
}

func (l *lineSplitter) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := indexNewline(l.buf)
		if i < 0 {
			break
		}
		l.line(string(l.buf[:i]))
		l.buf = l.buf[i+1:]
	}
	// Uma "linha" sem fim que cresce sem parar não pode comer a memória.
	if len(l.buf) > 4*domain.LogLineMax {
		l.line(string(l.buf))
		l.buf = l.buf[:0]
	}
	return len(p), nil
}

func (l *lineSplitter) flush() {
	if len(l.buf) > 0 {
		l.line(string(l.buf))
		l.buf = nil
	}
}

func indexNewline(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}

// line separa o carimbo de hora (RFC 3339 com nanossegundos) do texto.
func (l *lineSplitter) line(raw string) {
	raw = strings.TrimSuffix(raw, "\r")
	at := time.Now().UTC()
	if sp := strings.IndexByte(raw, ' '); sp > 0 {
		if t, err := time.Parse(time.RFC3339Nano, raw[:sp]); err == nil {
			at, raw = t.UTC(), raw[sp+1:]
		}
	}
	l.emit(LogEntry{At: at, Stream: l.stream, Line: raw})
}

// FollowLogs lê o log de um contêiner do Cobogó, numa chamada que só volta quando
// o fluxo acaba (o contêiner parou, foi removido, a conexão caiu ou ctx terminou).
// since, se não for zero, começa na hora dada (exclusive: a hora já vista não se
// repete); sem since, tail diz quantas linhas do fim ler. follow=false lê o que há e
// termina.
func (r *Runtime) FollowLogs(ctx context.Context, containerID string, since time.Time, tail int, follow bool, emit func(LogEntry)) error {
	inspect, err := r.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("dockerrt: inspecionar contêiner para o log: %w", err)
	}
	if inspect.Config == nil || inspect.Config.Labels[LabelManaged] != "true" || inspect.Config.Labels[LabelProject] == "" {
		return fmt.Errorf("dockerrt: o contêiner %s não é de um projeto no Cobogó", shortID(containerID))
	}
	opts := container.LogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: true, Follow: follow}
	if !since.IsZero() {
		s := since.Add(time.Microsecond)
		opts.Since = strconv.FormatInt(s.Unix(), 10) + "." + fmt.Sprintf("%09d", s.Nanosecond())
		opts.Tail = "all"
	} else {
		opts.Tail = strconv.Itoa(tail)
	}
	body, err := r.cli.ContainerLogs(ctx, containerID, opts)
	if err != nil {
		return fmt.Errorf("dockerrt: seguir o log do contêiner: %w", err)
	}
	defer body.Close()
	out := &lineSplitter{stream: domain.LogStdout, emit: emit}
	errw := &lineSplitter{stream: domain.LogStderr, emit: emit}
	_, err = stdcopy.StdCopy(out, errw, body)
	out.flush()
	errw.flush()
	if err != nil && err != io.EOF && ctx.Err() == nil {
		return fmt.Errorf("dockerrt: ler o log do contêiner: %w", err)
	}
	return nil
}
