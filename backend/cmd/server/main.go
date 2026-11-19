package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	"github.com/murilo/pr-follower/backend/internal/usecase/changedfiles"
	"github.com/murilo/pr-follower/backend/internal/usecase/codeowners"
	"github.com/murilo/pr-follower/backend/internal/usecase/dependabot"
	"github.com/murilo/pr-follower/backend/internal/usecase/prfiles"
	"github.com/murilo/pr-follower/backend/internal/usecase/repoimport"
	"github.com/murilo/pr-follower/backend/internal/usecase/syncjob"
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

	// Activity lives in the database and replicas wake each other through Postgres, so any number of
	// API replicas share one timeline and one live stream.
	sseHub := newActivityHub(ctx, cfg, db)
	opReg := newOperationRegistry(ctx, cfg, db)
	appAuth := newAppAuth(cfg)
	// An untyped nil interface when the app credentials are not set (a typed nil would not compare nil).
	var installations port.InstallationTokenSource
	if appAuth != nil {
		installations = appAuth
	}
	ghGateway := github.NewGateway(github.GatewayConfig{
		Backoff:       resilience.DefaultBackoffConfig(),
		Installations: installations,
	})

	// The Dependabot analysis reads lockfiles and tarballs as the GitHub App, so it only exists
	// when the app credentials are set; without them PRs stay "not analysed".
	var analyzeUC *dependabot.AnalyzeUseCase
	// A pull_request delivery older than the newest applied is dropped (ADR-0011).
	ingestOpts := []webhook.IngestOption{webhook.WithDeliveryOrdering(prRepo)}
	var importOpts []repoimport.Option
	if installations != nil {
		analyzeUC = dependabot.NewAnalyzeUseCase(
			prRepo,
			repoRepo,
			ghGateway,
			[]port.DependencyAnalyzer{nodejs.NewAnalyzer()},
			blastradius.NewScanner(),
			sseHub,
			dependabot.WithFailureLog(prRepo), // the Radar says why an analysis failed
		)
		runner := dependabot.NewRunner(analyzeUC, dependabot.RunnerConfig{})
		go runner.Run(ctx) // stops with the server
		ingestOpts = append(ingestOpts, webhook.WithAnalysisScheduler(runner))
		importOpts = append(importOpts, repoimport.WithAnalysisScheduler(runner))
	}
	installationUC := webhook.NewInstallationUseCase(repoRepo, sseHub)

	// ADR-0010: GitHub is the only identity provider. Without client credentials the
	// login endpoint redirects with auth_error=not_configured and every API route is 401.
	var authUC *auth.UseCase
	// Acts on GitHub as the signed-in user; nil without sign-in (every API route is 401 then).
	var actionUC *action.UseCase
	var importUC *repoimport.UseCase
	// Runs the "sync with GitHub" in the background, one per user; nil without sign-in.
	var syncJobs *syncjob.Manager
	var filesUC *prfiles.UseCase
	// Reads PR changed files with user tokens; nil without sign-in, which leaves PRs "not evaluated".
	var filesSyncer port.ChangedFilesSyncer
	// Keeps the cached CODEOWNERS current on pushes; nil without sign-in.
	var pushUC *webhook.PushIngestUseCase
	triageOpts := []triage.Option{triage.WithCheckRuns(prRepo), triage.WithAnalysisFailures(prRepo)}
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
			// ADR-0015: sign-in limited to these organizations' members (config refuses an allowlist
			// without app credentials, so appAuth is set whenever the list is).
			AllowedOrgs: cfg.AllowedGitHubOrgs,
			Members:     orgMembership(appAuth),
			Access:      persistence.NewMemberAccessStore(db),
		})
		go authUC.PurgeExpiredSessionsEvery(ctx, time.Hour) // stops with the server
		if len(cfg.AllowedGitHubOrgs) > 0 {
			slog.Info("sign-in restricted to github organization members", "orgs", cfg.AllowedGitHubOrgs)
			// Every replica sweeps: revoking is idempotent (ADR-0011, ADR-0012). Stops with the server.
			go authUC.RecheckMembershipEvery(ctx, membershipRecheckInterval)
		}
		userAPI := github.NewUserClient(github.UserClientConfig{Backoff: resilience.DefaultBackoffConfig()})
		// The turn engine: each PR's changed files, read with a user token, feed CODEOWNERS ownership.
		changedFilesUC := changedfiles.NewUseCase(authUC, userAPI, prRepo, repoRepo, installations)
		filesSyncer = changedFilesUC
		importUC = repoimport.NewUseCase(authUC, userAPI, repoRepo, prRepo, prRepo, userRepo, sseHub, changedFilesUC, prRepo, importOpts...)
		// The job is in the database, so a progress poll or a cancel may reach any replica.
		syncJobs = syncjob.NewManager(ctx, importUC, persistence.NewSyncJobStore(db)) // stops with the server
		filesUC = prfiles.NewUseCase(authUC, userAPI, repoRepo, prRepo)
		// GitHub computes a PR's merge state after the fact and sends no event: a load asks again.
		triageOpts = append(triageOpts, triage.WithMergeStateRefresh(authUC, userAPI))
		pushUC = webhook.NewPushIngestUseCase(repoRepo, codeowners.NewUseCase(repoRepo, userAPI, authUC, repoRepo, installations, sseHub))
		actionUC = action.NewUseCase(action.Deps{
			PRs: prRepo, Repos: repoRepo, Users: userRepo, Tokens: authUC,
			GitHub: ghGateway, State: userAPI, Reviews: prRepo, Pub: sseHub,
		})
	} else {
		slog.Warn("GitHub sign-in disabled: set GITHUB_CLIENT_ID and GITHUB_CLIENT_SECRET")
	}
	triageUC := triage.NewUseCase(userRepo, repoRepo, prRepo, sseHub, prRepo, filesSyncer, prRepo, triageOpts...)
	webhookUC := webhook.NewIngestUseCase(repoRepo, prRepo, prRepo, sseHub, filesSyncer, ingestOpts...)
	reviewUC := webhook.NewReviewIngestUseCase(repoRepo, prRepo, prRepo, sseHub)

	handler := httpadapter.NewHandler(httpadapter.HandlerDeps{
		UserRepo:       userRepo,
		RepoRepo:       repoRepo,
		PRRepo:         prRepo,
		TriageUC:       triageUC,
		ActionUC:       actionUC,
		AnalyzeUC:      analyzeUC,
		WebhookUC:      webhookUC,
		ReviewUC:       reviewUC,
		SyncJobs:       syncJobs,
		PushUC:         pushUC,
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
		Database:       db,
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

// membershipRecheckInterval is how often a user who left the allowed organizations is looked for
// (ADR-0015): the longest a leaver keeps reading stored data. One GitHub call per user and allowed
// organization, as the app's installation.
const membershipRecheckInterval = 15 * time.Minute

// orgMembership is nil (an untyped nil interface) without the GitHub App.
func orgMembership(appAuth *github.AppAuth) port.OrgMembershipChecker {
	if appAuth == nil {
		return nil
	}
	return appAuth
}

// newAppAuth authenticates as the GitHub App for reads that have no signed-in user behind them and
// for organization membership checks. It returns nil when the app credentials are not set, and
// stops the server when they are set but invalid: a broken key must not fail silently later.
func newAppAuth(cfg *config.Config) *github.AppAuth {
	if !cfg.GitHubAppConfigured() {
		if cfg.GitHubAppID != "" || cfg.GitHubAppPrivateKey != "" {
			slog.Warn("GitHub App credentials incomplete: set both GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY (or _FILE)")
		}
		slog.Warn("GitHub App credentials not set: background reads use the token of a signed-in follower")
		return nil
	}
	auth, err := github.NewAppAuth(github.AppAuthConfig{
		AppID:         cfg.GitHubAppID,
		PrivateKeyPEM: cfg.GitHubAppPrivateKey,
		Backoff:       resilience.DefaultBackoffConfig(),
	})
	if err != nil {
		slog.Error("invalid GitHub App credentials", "err", err)
		os.Exit(1)
	}
	return auth
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

// newOperationRegistry lets a user cancel an operation whichever replica runs it: a cancel that lands
// on another replica reaches it through Postgres NOTIFY. SQLite means one process: local is enough.
func newOperationRegistry(ctx context.Context, cfg *config.Config, db *sql.DB) *operation.Registry {
	reg := operation.NewRegistry()
	if !strings.HasPrefix(cfg.DatabaseURL, "sqlite://") {
		reg.Connect(ctx, persistence.NewPostgresReplicaBroadcast(db, cfg.DatabaseURL, persistence.OperationCancelChannel))
	}
	return reg
}

// newActivityHub keeps activity in the database. Replicas wake each other through Postgres
// LISTEN/NOTIFY; a SQLite database (local experiments) has a single process, so an in-process
// wake-up is enough. Without a readable log the server cannot show or stream anything: stop.
func newActivityHub(ctx context.Context, cfg *config.Config, db *sql.DB) *sse.Hub {
	var wake port.ActivitySignal = sse.NewLocalSignal()
	if !strings.HasPrefix(cfg.DatabaseURL, "sqlite://") {
		wake = persistence.NewPostgresActivitySignal(db, cfg.DatabaseURL)
	}
	hub := sse.NewHub(persistence.NewActivityLog(db), wake)
	if err := hub.Start(ctx); err != nil {
		slog.Error("failed to start the activity hub", "err", err)
		os.Exit(1)
	}
	return hub
}
