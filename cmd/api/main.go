package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"

	"github.com/ocnaibill/cobogo/internal/auth"
	"github.com/ocnaibill/cobogo/internal/config"
	"github.com/ocnaibill/cobogo/internal/httpapi"
	"github.com/ocnaibill/cobogo/internal/settings"
	"github.com/ocnaibill/cobogo/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// `cobogo-api migrate`: aplica as migrations e a matriz de privilégios com o
	// role dono, em passo próprio (DEC-33 item 5). Só precisa de DATABASE_URL.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := migrateCommand(log); err != nil {
			log.Error("migrar o banco", "erro", err)
			os.Exit(1)
		}
		return
	}

	// `cobogo-api rotate-secrets [--check]`: recifra as variáveis com a chave
	// atual do chaveiro, com o role dono (DEC-39 item 6).
	if len(os.Args) > 1 && os.Args[1] == "rotate-secrets" {
		if err := rotateSecretsCommand(log, os.Args[2:]); err != nil {
			log.Error("rotacionar a chave", "erro", err)
			os.Exit(1)
		}
		return
	}

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

	if err := store.Prepare(ctx, pool, cfg.AutoMigrate); err != nil {
		log.Error("preparar o esquema do banco", "erro", err)
		os.Exit(1)
	}

	st := store.New(pool).WithCipher(cfg.Keyring)

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
		Settings:       settings.NewResolver(st, settings.Env{Limits: cfg.Limits, GitHosts: cfg.ExtraGitHosts, AppsDomain: cfg.AppsDomain, AppsScheme: cfg.AppsScheme}),
		SetupToken:     setupToken,
		Hasher:         hasher,
		Limiter:        auth.NewLoginLimiter(),
	})
	if err != nil {
		log.Error("montar a API", "erro", err)
		os.Exit(1)
	}

	go cleanSessions(ctx, st, log)
	serveRouting(ctx, st, cfg, log)

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

// serveRouting sobe o endpoint de rotas do Traefik em um listener interno
// próprio (DEC-31 item 8). Sem token configurado, o roteamento por hostname
// fica desligado e nada é aberto.
func serveRouting(ctx context.Context, st *store.Store, cfg config.Config, log *slog.Logger) {
	if cfg.RouterToken == "" {
		log.Info("COBOGO_ROUTER_TOKEN ausente: o endpoint de rotas do Traefik não sobe (roteamento por hostname desligado)")
		return
	}
	var panel *httpapi.PanelRoute
	if cfg.PanelTarget != "" {
		u, err := url.Parse(cfg.PublicURL)
		if err != nil || u.Hostname() == "" {
			log.Error("COBOGO_PUBLIC_URL inválida para a rota do painel", "erro", err)
			os.Exit(1)
		}
		panel = &httpapi.PanelRoute{Host: strings.ToLower(u.Hostname()), Target: cfg.PanelTarget}
	}
	handler, err := httpapi.NewRoutingHandler(st, cfg.RouterToken, log, panel)
	if err != nil {
		log.Error("montar o endpoint de rotas", "erro", err)
		os.Exit(1)
	}
	srv := &http.Server{
		Addr:              cfg.RouterAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	go func() {
		log.Info("endpoint de rotas do Traefik no ar (só na rede de controle; não o publique)", "addr", cfg.RouterAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("endpoint de rotas encerrou", "erro", err)
			os.Exit(1)
		}
	}()
}

// migrateCommand aplica as migrations e, se os roles de aplicação existem, a
// matriz de privilégios. Sai com erro se qualquer passo falhar, o que impede a
// API e o worker de subirem sobre um esquema pela metade.
func migrateCommand(log *slog.Logger) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL é obrigatória (o role dono do esquema)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("conectar ao postgres: %w", err)
	}
	defer pool.Close()

	if err := store.Migrate(ctx, pool); err != nil {
		return err
	}
	applied, err := store.ApplyGrants(ctx, pool, store.DefaultRoles)
	if err != nil {
		return err
	}
	if applied {
		log.Info("migrations e privilégios aplicados", "roles", store.DefaultRoles.API+", "+store.DefaultRoles.Worker)
	} else {
		log.Warn("migrations aplicadas; os roles de aplicação não existem, então os privilégios não foram aplicados (esperado só em desenvolvimento)")
	}
	return nil
}

// rotateSecretsCommand recifra cada variável com a chave atual do chaveiro, sem
// mudar a versão da variável (o valor é o mesmo: não pede novo deploy). Com
// --check só conta, por versão de chave, e não altera nada. Roda com o role
// dono: a API e o worker não leem nem reescrevem texto cifrado fora do uso
// normal (DEC-33, DEC-39).
func rotateSecretsCommand(log *slog.Logger, args []string) error {
	check := false
	for _, a := range args {
		if a != "--check" {
			return fmt.Errorf("argumento desconhecido %q (use --check para só contar)", a)
		}
		check = true
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL é obrigatória (o role dono do esquema)")
	}
	ring, err := config.LoadKeyring()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return fmt.Errorf("conectar ao postgres: %w", err)
	}
	defer pool.Close()
	st := store.New(pool).WithCipher(ring)

	counts, err := st.KeyVersionCounts(ctx)
	if err != nil {
		return err
	}
	if check {
		fmt.Println(secretsReport(counts, ring.KeyVersion()))
		return nil
	}
	report, err := st.RotateSecrets(ctx)
	if err != nil {
		return err
	}
	total := 0
	for _, n := range report.Rotated {
		total += n
	}
	log.Info("variáveis recifradas", "total", total, "chave_atual", report.Current)
	after, err := st.KeyVersionCounts(ctx)
	if err != nil {
		return err
	}
	fmt.Println(secretsReport(after, ring.KeyVersion()))
	return nil
}

// secretsReport diz, por versão de chave, quantas variáveis há e se alguma
// chave antiga já pode sair do ambiente.
func secretsReport(counts map[int]int, current int) string {
	versions := make([]int, 0, len(counts))
	for v := range counts {
		versions = append(versions, v)
	}
	sort.Ints(versions)
	var b strings.Builder
	old := 0
	for _, v := range versions {
		mark := ""
		if v == current {
			mark = " (atual)"
		} else {
			old += counts[v]
		}
		fmt.Fprintf(&b, "chave versão %d%s: %d variável(is)\n", v, mark, counts[v])
	}
	if len(versions) == 0 {
		b.WriteString("nenhuma variável cifrada\n")
	}
	if old == 0 {
		fmt.Fprintf(&b, "nenhuma variável usa chave antiga: as chaves diferentes da versão %d já podem sair de COBOGO_SECRET_KEYS", current)
	} else {
		fmt.Fprintf(&b, "%d variável(is) ainda usam chave antiga: rode `cobogo-api rotate-secrets` e só depois retire a chave antiga", old)
	}
	return b.String()
}
