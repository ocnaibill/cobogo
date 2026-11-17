package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"

	"github.com/ocnaibill/cobogo/internal/auth"
	"github.com/ocnaibill/cobogo/internal/config"
	"github.com/ocnaibill/cobogo/internal/httpapi"
	"github.com/ocnaibill/cobogo/internal/secrets"
	"github.com/ocnaibill/cobogo/internal/settings"
	"github.com/ocnaibill/cobogo/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		log.Error("carregar configuração", "erro", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("conectar ao postgres", "erro", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := store.Migrate(ctx, pool); err != nil {
		log.Error("aplicar migrations", "erro", err)
		os.Exit(1)
	}

	box, err := secrets.NewBox(cfg.SecretKey, 1)
	if err != nil {
		log.Error("preparar cifra de variáveis", "erro", err)
		os.Exit(1)
	}
	st := store.New(pool).WithCipher(box)

	hasher, err := auth.NewHasher(2)
	if err != nil {
		log.Error("preparar hash de senhas", "erro", err)
		os.Exit(1)
	}

	// Subcomando de recuperação: `api admin reset-password --login <login>`.
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		if err := adminCommand(ctx, st, hasher, os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "erro:", err)
			os.Exit(1)
		}
		return
	}

	if err := cfg.ValidateForAPI(); err != nil {
		log.Error("configuração da API", "erro", err)
		os.Exit(1)
	}
	if cfg.CookieInsecure {
		log.Warn("COBOGO_COOKIE_INSECURE=1: o cookie de sessão não é Secure; use só em desenvolvimento")
	}
	if len(cfg.TrustedProxies) == 0 {
		log.Info("nenhum proxy confiável configurado (COBOGO_TRUSTED_PROXIES): cabeçalhos de proxy serão ignorados")
	}

	// Sem administrador, gera o token de configuração de uso único e o imprime
	// uma vez (DEC-27 item 3).
	var setupToken *auth.SetupToken
	users, err := st.CountUsers(ctx)
	if err != nil {
		log.Error("contar usuários", "erro", err)
		os.Exit(1)
	}
	if users == 0 {
		token, plain, err := auth.NewSetupToken()
		if err != nil {
			log.Error("gerar token de configuração", "erro", err)
			os.Exit(1)
		}
		setupToken = token
		fmt.Fprintf(os.Stdout, "\n==================================================================\n"+
			" Nenhum administrador cadastrado.\n"+
			" Abra %s e use este token de configuração (uso único):\n\n"+
			"   %s\n\n"+
			" Ele só aparece agora; reiniciar a API gera outro.\n"+
			"==================================================================\n\n", cfg.PublicURL, plain)
	}

	router, err := httpapi.NewRouter(httpapi.Options{
		Store: st, Log: log,
		PublicURL: cfg.PublicURL, CookieInsecure: cfg.CookieInsecure,
		TrustedProxies: cfg.TrustedProxies,
		Settings:       settings.NewResolver(st, settings.Env{Limits: cfg.Limits, GitHosts: cfg.ExtraGitHosts}),
		SetupToken:     setupToken,
		Hasher:         hasher,
		Limiter:        auth.NewLoginLimiter(),
	})
	if err != nil {
		log.Error("montar a API", "erro", err)
		os.Exit(1)
	}

	go cleanSessions(ctx, st, log)

	// Timeouts contra conexões lentas (DEC-27 item 14).
	srv := &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("API Cobogó no ar", "endereco", cfg.APIAddr, "url_publica", cfg.PublicURL)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("servidor http", "erro", err)
		os.Exit(1)
	}
}

// cleanSessions remove sessões vencidas de tempos em tempos.
func cleanSessions(ctx context.Context, st *store.Store, log *slog.Logger) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := st.DeleteExpiredSessions(ctx, httpapi.SessionIdle); err != nil && ctx.Err() == nil {
				log.Warn("limpar sessões", "erro", err)
			} else if n > 0 {
				log.Info("sessões vencidas removidas", "quantidade", n)
			}
		}
	}
}

// adminCommand implementa `admin reset-password --login <login>`: a recuperação
// de senha é manual, por quem tem acesso ao host (DEC-27 item 3). A senha vem
// do terminal, sem eco, ou da entrada padrão quando não há terminal.
func adminCommand(ctx context.Context, st *store.Store, hasher *auth.Hasher, args []string) error {
	if len(args) == 0 || args[0] != "reset-password" {
		return errors.New("uso: api admin reset-password --login <login>")
	}
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	login := fs.String("login", "", "login do administrador")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	*login = strings.ToLower(strings.TrimSpace(*login))
	if *login == "" {
		return errors.New("informe --login")
	}

	password, err := readPassword()
	if err != nil {
		return err
	}
	if err := auth.CheckPassword(*login, password); err != nil {
		return err
	}
	hash, err := hasher.Hash(ctx, password)
	if err != nil {
		return err
	}
	if err := st.ResetPasswordByLogin(ctx, *login, hash); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("não existe o login %q", *login)
		}
		return err
	}
	fmt.Println("Senha redefinida; todas as sessões de", *login, "foram encerradas.")
	return nil
}

func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "Nova senha: ")
		first, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		fmt.Fprint(os.Stderr, "Repita a senha: ")
		second, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		if string(first) != string(second) {
			return "", errors.New("as senhas não coincidem")
		}
		return string(first), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("não consegui ler a senha da entrada padrão")
	}
	return strings.TrimRight(line, "\r\n"), nil
}
