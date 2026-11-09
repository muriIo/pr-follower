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
	"github.com/murilo/pr-follower/backend/internal/usecase/dependabot"
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

	userRepo := persistence.NewUserRepository(db)
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
	webhookUC := webhook.NewIngestUseCase(repoRepo, prRepo, sseHub)

	handler := httpadapter.NewHandler(httpadapter.HandlerDeps{
		UserRepo:      userRepo,
		RepoRepo:      repoRepo,
		PRRepo:        prRepo,
		TriageUC:      triageUC,
		ActionUC:      actionUC,
		AnalyzeUC:     analyzeUC,
		WebhookUC:     webhookUC,
		SSEHub:        sseHub,
		OpRegistry:    opReg,
		WebhookSecret: cfg.GitHubWebhookSecret,
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
