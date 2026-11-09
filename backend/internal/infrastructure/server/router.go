package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	httpadapter "github.com/murilo/pr-follower/backend/internal/adapter/http"
)

func NewChiRouter(h *httpadapter.Handler, allowedOrigin string) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(allowedOrigin))

	r.Get("/healthz", h.Healthz)

	r.Route("/api/v1", func(api chi.Router) {
		api.Post("/auth/login", h.AuthLogin)
		api.Get("/auth/me", h.AuthMe)
		api.Post("/auth/logout", h.AuthLogout)

		api.Get("/workspace", h.GetWorkspace)
		api.Get("/events/stream", h.ServeSSE)
		api.Post("/demo/seed", h.SeedDemoData)
		api.Post("/repositories/follow", h.FollowRepository)
		api.Post("/webhooks/github", h.GitHubWebhook)

		api.Post("/prs/batch-merge", h.BatchMergePRs)
		api.Post("/prs/{id}/approve", h.ApprovePR)
		api.Post("/prs/{id}/merge", h.MergePR)
		api.Post("/prs/{id}/snooze", h.SnoozePR)
		api.Post("/prs/{id}/claim", h.ClaimPR)
		api.Post("/prs/{id}/dependabot-command", h.DependabotCommand)

		api.Delete("/operations/{id}", h.CancelOperation)
	})

	return r
}

func corsMiddleware(allowedOrigin string) func(http.Handler) http.Handler {
	if allowedOrigin == "" {
		allowedOrigin = "*"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-Operation-ID, X-User-ID")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
