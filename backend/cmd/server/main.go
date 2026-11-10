package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/analyzer/blastradius"
	"github.com/murilo/pr-follower/backend/internal/adapter/analyzer/nodejs"
	"github.com/murilo/pr-follower/backend/internal/adapter/crypto"
	"github.com/murilo/pr-follower/backend/internal/adapter/github"
	httpadapter "github.com/murilo/pr-follower/backend/internal/adapter/http"
	"github.com/murilo/pr-follower/backend/internal/adapter/http/operation"
	"github.com/murilo/pr-follower/backend/internal/adapter/persistence"
	"github.com/murilo/pr-follower/backend/internal/adapter/sse"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/config"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/database"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/server"
	"github.com/murilo/pr-follower/backend/internal/usecase/action"
	"github.com/murilo/pr-follower/backend/internal/usecase/auth"
	"github.com/murilo/pr-follower/backend/internal/usecase/dependabot"
	"github.com/murilo/pr-follower/backend/internal/usecase/prfiles"
	"github.com/murilo/pr-follower/backend/internal/usecase/repoimport"
	"github.com/murilo/pr-follower/backend/internal/usecase/triage"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.LoadFromEnv()
	if err != nil {
		slog.Error("failed to load configuration", "err", err)
		os.Exit(1)
	}

	db, err := database.Connect(ctx, database.Options{
		DSN:          cfg.DatabaseURL,
		MaxOpenConns: cfg.DBMaxOpenConns,
		MaxIdleConns: cfg.DBMaxIdleConns,
		Backoff:      resilience.DefaultBackoffConfig(),
	})
	if err != nil {
		slog.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}

	tokenCipher, err := newTokenCipher(cfg.TokenEncryptionKey)
	if err != nil {
		slog.Error("invalid TOKEN_ENCRYPTION_KEY", "err", err)
		os.Exit(1)
	}

	userRepo := persistence.NewUserRepository(db, tokenCipher)
	sessionRepo := persistence.NewSessionRepository(db)
	repoRepo := persistence.NewRepositoryRepository(db)
	prRepo := persistence.NewPullRequestRepository(db)

	sseHub := sse.NewHub(100)
	opReg := operation.NewRegistry()
	ghGateway := github.NewGateway(github.GatewayConfig{
		Backoff: resilience.DefaultBackoffConfig(),
	})

	triageUC := triage.NewUseCase(userRepo, repoRepo, prRepo, sseHub)
	actionUC := action.NewUseCase(prRepo, repoRepo, ghGateway, sseHub)
	analyzeUC := dependabot.NewAnalyzeUseCase(
		prRepo,
		repoRepo,
		ghGateway,
		[]port.DependencyAnalyzer{nodejs.NewAnalyzer()},
		blastradius.NewScanner(),
		sseHub,
	)
	webhookUC := webhook.NewIngestUseCase(repoRepo, prRepo, prRepo, sseHub)
	installationUC := webhook.NewInstallationUseCase(repoRepo, sseHub)

	// ADR-0010: GitHub is the only identity provider. Without client credentials the
	// login endpoint redirects with auth_error=not_configured and every API route is 401.
	var authUC *auth.UseCase
	var importUC *repoimport.UseCase
	var filesUC *prfiles.UseCase
	if cfg.GitHubLoginConfigured() {
		authUC = auth.NewUseCase(auth.Deps{
			OAuth: github.NewOAuthClient(github.OAuthConfig{
				ClientID:     cfg.GitHubClientID,
				ClientSecret: cfg.GitHubClientSecret,
				RedirectURL:  cfg.GitHubCallbackURL(),
				Backoff:      resilience.DefaultBackoffConfig(),
			}),
			Users:    userRepo,
			Sessions: sessionRepo,
		})
		userAPI := github.NewUserClient(github.UserClientConfig{Backoff: resilience.DefaultBackoffConfig()})
		importUC = repoimport.NewUseCase(authUC, userAPI, repoRepo, sseHub)
		filesUC = prfiles.NewUseCase(authUC, userAPI, repoRepo, prRepo)
	} else {
		slog.Warn("GitHub sign-in disabled: set GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET")
	}

	handler := httpadapter.NewHandler(httpadapter.HandlerDeps{
		UserRepo:       userRepo,
		RepoRepo:       repoRepo,
		PRRepo:         prRepo,
		TriageUC:       triageUC,
		ActionUC:       actionUC,
		AnalyzeUC:      analyzeUC,
		WebhookUC:      webhookUC,
		SSEHub:         sseHub,
		OpRegistry:     opReg,
		WebhookSecret:  cfg.GitHubWebhookSecret,
		AuthUC:         authUC,
		SecureCookies:  cfg.SecureCookies(),
		ImportUC:       importUC,
		InstallURL:     cfg.GitHubAppInstallURL(),
		InstallationUC: installationUC,
		Follows:        repoRepo,
		FilesUC:        filesUC,
	})

	router := server.NewChiRouter(handler, cfg.AllowedOrigin)
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("PR Follower Go v1.27 server listening", "port", cfg.Port)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

// newTokenCipher uses TOKEN_ENCRYPTION_KEY, or an ephemeral key for local development:
// tokens encrypted with it become unreadable on restart and users simply sign in again.
func newTokenCipher(base64Key string) (*crypto.AESGCMCipher, error) {
	if base64Key == "" {
		slog.Warn("TOKEN_ENCRYPTION_KEY not set: using an ephemeral key, GitHub sign-ins will not survive a restart")
		generated, err := crypto.GenerateKey()
		if err != nil {
			return nil, err
		}
		base64Key = generated
	}
	return crypto.NewAESGCMCipher(base64Key)
}
