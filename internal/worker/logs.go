package worker

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/redact"
)

// Logs de execução (DEC-46). Um laço próprio, fora do dos deploys, segue o stdout
// e o stderr de cada contêiner de projeto (aplicações e bancos gerenciados),
// redige os valores das variáveis e grava em lotes. Só o worker decifra as
// variáveis (DEC-33), por isso a captura é dele.

// Ajustáveis só pelos testes.
var (
	logReconcileEvery = 5 * time.Second
	logFlushEvery     = time.Second
	logPruneEvery     = time.Minute
	logSecretsTTL     = time.Minute
)

const (
	// logBacklog é quantas linhas do fim se pegam de um contêiner que ainda não tem log guardado.
	logBacklog = 500
	// logBatch é o tamanho do lote que força a gravação antes do relógio.
	logBatch = 200
	// Limite de taxa por contêiner: média de linhas por segundo e rajada.
	logRatePerSecond = 200.0
	logBurst         = 2000.0
	// logLineSuffix marca uma linha cortada no limite.
	logLineSuffix = " […]"
)

type secretSet struct {
	values []string
	at     time.Time
}

// logCapture é o estado do laço: quem está sendo seguido e os valores a redigir.
type logCapture struct {
	w  *Worker
	rt *dockerrt.Runtime
	wg sync.WaitGroup

	mu          sync.Mutex
	following   map[string]bool
	seenStopped map[string]bool
	secrets     map[uuid.UUID]secretSet
}

// runLogCapture é o laço da captura e da retenção.
func (w *Worker) runLogCapture(ctx context.Context) {
	c := &logCapture{w: w, rt: w.runtime, following: map[string]bool{}, seenStopped: map[string]bool{}, secrets: map[uuid.UUID]secretSet{}}
	nextPrune := time.Now()
	for {
		c.reconcile(ctx)
		if time.Now().After(nextPrune) {
			nextPrune = time.Now().Add(logPruneEvery)
			if n, err := w.store.PruneRuntimeLogs(ctx, domain.LogRetention, domain.LogRetentionMax); err != nil && ctx.Err() == nil {
				w.log.Warn("varrer os logs de execução", "erro", err)
			} else if n > 0 {
				w.log.Debug("logs de execução apagados pela retenção", "linhas", n)
			}
		}
		select {
		case <-ctx.Done():
			c.wg.Wait()
			return
		case <-time.After(logReconcileEvery):
		}
	}
}

// reconcile liga um seguidor a cada contêiner de projeto em execução e lê uma
// vez o que há num contêiner parado que ainda não foi lido.
func (c *logCapture) reconcile(ctx context.Context) {
	list, err := c.rt.ListManaged(ctx, "")
	if err != nil {
		if ctx.Err() == nil {
			c.w.log.Warn("listar contêineres para os logs", "erro", err)
		}
		return
	}
	present := map[string]bool{}
	for _, m := range list {
		if m.Role != "" || m.ProjectID == "" {
			continue // passageiros (comando, posse de volume, certificado) não têm log de projeto
		}
		present[m.ID] = true
		c.mu.Lock()
		busy := c.following[m.ID]
		if m.Running {
			delete(c.seenStopped, m.ID)
		}
		done := !m.Running && c.seenStopped[m.ID]
		if !busy && !done {
			c.following[m.ID] = true
			if !m.Running {
				c.seenStopped[m.ID] = true
			}
			c.wg.Add(1)
			go func(m dockerrt.ManagedContainer) {
				defer c.wg.Done()
				defer func() { c.mu.Lock(); delete(c.following, m.ID); c.mu.Unlock() }()
				c.follow(ctx, m)
			}(m)
		}
		c.mu.Unlock()
	}
	c.mu.Lock()
	for id := range c.seenStopped {
		if !present[id] {
			delete(c.seenStopped, id)
		}
	}
	c.mu.Unlock()
}

// secretsOf devolve os valores das variáveis do projeto, relidos a cada logSecretsTTL.
func (c *logCapture) secretsOf(ctx context.Context, projectID uuid.UUID) []string {
	c.mu.Lock()
	s, ok := c.secrets[projectID]
	c.mu.Unlock()
	if ok && time.Since(s.at) < logSecretsTTL {
		return s.values
	}
	values, _, err := c.w.store.RevealEnvValues(ctx, projectID)
	if err != nil {
		if ctx.Err() == nil {
			c.w.log.Warn("ler as variáveis para redigir os logs", "projeto", projectID, "erro", err)
		}
		return s.values // o que já se tinha vale mais que nada
	}
	list := make([]string, 0, len(values))
	for _, v := range values {
		list = append(list, v)
	}
	c.mu.Lock()
	c.secrets[projectID] = secretSet{values: list, at: time.Now()}
	c.mu.Unlock()
	return list
}

// cleanLine limpa, redige e limita uma linha.
func cleanLine(line string, secrets []string) string {
	line = redact.Values(redact.Clean(line), secrets)
	if len(line) <= domain.LogLineMax {
		return line
	}
	cut := line[:domain.LogLineMax]
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[:len(cut)-1]
	}
	return cut + logLineSuffix
}

// follow captura o log de um contêiner até o fluxo acabar.
func (c *logCapture) follow(ctx context.Context, m dockerrt.ManagedContainer) {
	projectID, err := uuid.Parse(m.ProjectID)
	if err != nil {
		return
	}
	origin := domain.LogOriginApp
	switch m.Service {
	case domain.ServicePostgres:
		origin = domain.LogOriginPostgres
	case domain.ServiceMySQL:
		origin = domain.LogOriginMySQL
	case domain.ServiceRedis:
		origin = domain.LogOriginRedis
	}
	var deployment *uuid.UUID
	if id, err := uuid.Parse(m.DeploymentID); err == nil {
		deployment = &id
	}

	since, have, err := c.w.store.LastRuntimeLogAt(ctx, m.ID)
	if err != nil {
		if ctx.Err() == nil {
			c.w.log.Warn("retomar o log do contêiner", "contêiner", m.Name, "erro", err)
		}
		return
	}
	if !have {
		since = time.Time{}
	}

	var (
		mu      sync.Mutex
		batch   []domain.LogLine
		tokens  = logBurst
		refill  = time.Now()
		dropped int
	)
	flush := func() {
		mu.Lock()
		out := batch
		batch = nil
		mu.Unlock()
		if len(out) == 0 {
			return
		}
		// A gravação não pode ser perdida com o laço terminando: o contexto dela é próprio.
		save, done := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
		defer done()
		if err := c.w.store.AppendRuntimeLogs(save, out); err != nil {
			c.w.log.Warn("gravar logs de execução", "contêiner", m.Name, "erro", err)
		}
	}
	add := func(e dockerrt.LogEntry) {
		mu.Lock()
		now := time.Now()
		tokens += now.Sub(refill).Seconds() * logRatePerSecond
		if tokens > logBurst {
			tokens = logBurst
		}
		refill = now
		var note *domain.LogLine
		if tokens < 1 {
			dropped++
			mu.Unlock()
			return
		}
		if dropped > 0 {
			note = &domain.LogLine{ProjectID: projectID, ContainerID: m.ID, ContainerName: m.Name, DeploymentID: deployment, Origin: origin, Stream: domain.LogSystem,
				At: e.At, Line: "[" + strconv.Itoa(dropped) + " linhas descartadas pelo limite de taxa]"}
			dropped = 0
		}
		tokens--
		mu.Unlock()
		line := domain.LogLine{ProjectID: projectID, ContainerID: m.ID, ContainerName: m.Name, DeploymentID: deployment, Origin: origin, Stream: e.Stream, At: e.At,
			Line: cleanLine(e.Line, c.secretsOf(ctx, projectID))}
		mu.Lock()
		if note != nil {
			batch = append(batch, *note)
		}
		batch = append(batch, line)
		full := len(batch) >= logBatch
		mu.Unlock()
		if full {
			flush()
		}
	}

	stop := make(chan struct{})
	ticked := make(chan struct{})
	go func() {
		defer close(ticked)
		t := time.NewTicker(logFlushEvery)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				flush()
			}
		}
	}()
	err = c.rt.FollowLogs(ctx, m.ID, since, logBacklog, m.Running, add)
	close(stop)
	<-ticked
	// O que sobrou descartado no fim também é dito.
	mu.Lock()
	if dropped > 0 {
		batch = append(batch, domain.LogLine{ProjectID: projectID, ContainerID: m.ID, ContainerName: m.Name, DeploymentID: deployment, Origin: origin, Stream: domain.LogSystem,
			At: time.Now().UTC(), Line: "[" + strconv.Itoa(dropped) + " linhas descartadas pelo limite de taxa]"})
	}
	mu.Unlock()
	flush()
	if err != nil && ctx.Err() == nil && !strings.Contains(err.Error(), "No such container") {
		c.w.log.Debug("o seguimento do log terminou", "contêiner", m.Name, "erro", err)
	}
}
