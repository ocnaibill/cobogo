// Comando backup: o backup do banco interno do Cobogó (DEC-47, parte A). Todo dia,
// à hora marcada, faz um dump consistente com `pg_dump`, confere que ele abre,
// guarda num volume e aplica a retenção. Conecta com um role só de leitura.
//
//	cobogo-backup            # o serviço: roda no horário, sem parar
//	cobogo-backup once ROTULO # um backup agora (o update.sh usa pre-<commit>) e sai
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ocnaibill/cobogo/internal/backup"
	"github.com/ocnaibill/cobogo/internal/settings"
)

const (
	defaultDir = "/backups"
	defaultAt  = "03:00"
	// retryAfter é a espera depois de uma falha, até a próxima tentativa.
	retryAfter = time.Hour
	// catchUpAfter: se o último backup automático tem mais que isto, roda ao subir.
	catchUpAfter = 24 * time.Hour
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Error("DATABASE_URL é obrigatória (o role cobogo_backup)")
		os.Exit(1)
	}
	dir := strings.TrimSpace(os.Getenv("COBOGO_BACKUP_DIR"))
	if dir == "" {
		dir = defaultDir
	}
	at, err := parseClock(envOr("COBOGO_BACKUP_AT", defaultAt))
	if err != nil {
		log.Error("COBOGO_BACKUP_AT inválida: use HH:MM, como 03:00", "erro", err)
		os.Exit(1)
	}
	cfg, err := pgEnv(dsn)
	if err != nil {
		log.Error("DATABASE_URL inválida", "erro", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	svc := &service{log: log, dir: dir, dsn: dsn, env: cfg}
	// A pasta dos backups de projeto (DEC-47 B): o worker, que roda com outro usuário,
	// cria as subpastas dentro dela. Os backups da plataforma ficam na raiz.
	if err := ensureProjectsDir(dir); err != nil {
		log.Warn("criar a pasta dos backups de projeto", "erro", err)
	}

	if len(os.Args) > 1 && os.Args[1] == "once" {
		label := ""
		if len(os.Args) > 2 {
			label = os.Args[2]
		}
		if err := svc.run(ctx, label); err != nil {
			log.Error("backup falhou", "erro", err)
			os.Exit(1)
		}
		return
	}
	svc.serve(ctx, at)
}

func envOr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// parseClock lê "HH:MM".
func parseClock(s string) (time.Duration, error) {
	h, m, ok := strings.Cut(s, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("%q não é HH:MM", s)
	}
	return time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute, nil
}

// nextRun é o próximo horário marcado depois de now (no fuso do contêiner).
func nextRun(now time.Time, at time.Duration) time.Time {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	t := day.Add(at)
	if !t.After(now) {
		t = day.AddDate(0, 0, 1).Add(at)
	}
	return t
}

// pgEnv traduz a DATABASE_URL nas variáveis que o pg_dump lê. A senha vai por
// ambiente, nunca na linha de comando.
func pgEnv(dsn string) ([]string, error) {
	c, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	env := []string{"PGHOST=" + c.Host, "PGPORT=" + strconv.Itoa(int(c.Port)), "PGUSER=" + c.User, "PGPASSWORD=" + c.Password, "PGDATABASE=" + c.Database}
	if c.TLSConfig == nil {
		env = append(env, "PGSSLMODE=disable")
	}
	return env, nil
}

type service struct {
	log *slog.Logger
	dir string
	dsn string
	env []string
}

func (s *service) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), s.env...)
	return cmd
}

// dump roda o pg_dump em formato custom: uma transação só, com um retrato
// consistente do banco, sem travar a API nem o worker.
func (s *service) dump(ctx context.Context, w io.Writer) error {
	cmd := s.command(ctx, "pg_dump", "--format=custom", "--no-owner", "--no-acl")
	cmd.Stdout = w
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// verify confere que o arquivo é um dump legível.
func (s *service) verify(ctx context.Context, path string) error {
	cmd := s.command(ctx, "pg_restore", "--list", path)
	cmd.Stdout = io.Discard
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_restore --list: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// retention lê a política do banco; um valor ausente ou inválido vale o padrão.
func (s *service) retention(ctx context.Context) (daily, weekly int) {
	daily, weekly = 7, 4
	conn, err := pgx.Connect(ctx, s.dsn)
	if err != nil {
		s.log.Warn("ler a retenção das configurações; vale o padrão", "erro", err)
		return
	}
	defer conn.Close(context.WithoutCancel(ctx))
	rows, err := conn.Query(ctx, `SELECT key, value FROM settings WHERE key = ANY($1)`, []string{settings.BackupKeepDaily, settings.BackupKeepWeekly})
	if err != nil {
		s.log.Warn("ler a retenção das configurações; vale o padrão", "erro", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			continue
		}
		def, ok := settings.Lookup(k)
		if !ok {
			continue
		}
		norm, err := def.Normalize(v)
		if err != nil {
			continue
		}
		n, _ := strconv.Atoi(norm)
		switch k {
		case settings.BackupKeepDaily:
			daily = n
		case settings.BackupKeepWeekly:
			weekly = n
		}
	}
	return
}

// run faz um backup, aplica a retenção e grava o estado.
func (s *service) run(ctx context.Context, label string) error {
	started := time.Now().UTC()
	daily, weekly := s.retention(ctx)
	st, _ := backup.ReadStatus(s.dir)
	st.LastRunAt, st.KeepDaily, st.KeepWeekly = &started, daily, weekly

	file, err := backup.Run(ctx, backup.Config{Dir: s.dir, Dump: s.dump, Verify: s.verify, KeepDaily: daily, KeepWeekly: weekly}, label)
	if err != nil {
		st.LastError = err.Error()
		if werr := backup.WriteStatus(s.dir, st); werr != nil {
			s.log.Error("gravar o estado do backup", "erro", werr)
		}
		return err
	}
	ok := time.Now().UTC()
	st.LastOKAt, st.LastFile, st.LastError = &ok, file.Name, ""
	s.log.Info("backup concluído", "arquivo", file.Name, "bytes", file.Size, "sha256", file.SHA256[:12])
	// A retenção só roda depois de um backup bom: nunca apaga sem ter o novo.
	if gone, err := backup.Prune(s.dir, time.Now(), daily, weekly); err != nil {
		s.log.Warn("aplicar a retenção", "erro", err)
	} else if len(gone) > 0 {
		s.log.Info("backups antigos apagados pela retenção", "quantos", len(gone))
	}
	if err := backup.WriteStatus(s.dir, st); err != nil {
		return fmt.Errorf("gravar o estado: %w", err)
	}
	return nil
}

// serve roda no horário marcado, sem parar. Ao subir, se o último backup
// automático é velho (ou não há), faz um na hora. Uma falha tenta de novo em
// uma hora, sem esperar o dia seguinte.
func (s *service) serve(ctx context.Context, at time.Duration) {
	s.log.Info("serviço de backup no ar", "diretório", s.dir, "horário", envOr("COBOGO_BACKUP_AT", defaultAt))
	next := nextRun(time.Now(), at)
	if files, err := backup.List(s.dir); err == nil {
		fresh := false
		for _, f := range files {
			if !f.Labeled() && time.Since(f.At) < catchUpAfter {
				fresh = true
				break
			}
		}
		if !fresh {
			next = time.Now()
		}
	}
	for {
		st, _ := backup.ReadStatus(s.dir)
		st.NextRunAt = &next
		_ = backup.WriteStatus(s.dir, st)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		if err := s.run(ctx, ""); err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return
			}
			s.log.Error("backup falhou; tento de novo em uma hora", "erro", err)
			next = time.Now().Add(retryAfter)
			continue
		}
		next = nextRun(time.Now(), at)
	}
}

// ensureProjectsDir cria <dir>/projects aberta para escrita.
func ensureProjectsDir(dir string) error {
	p := dir + "/projects"
	if err := os.MkdirAll(p, 0o777); err != nil {
		return err
	}
	return os.Chmod(p, 0o777)
}
