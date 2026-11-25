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
		// Public: the API health check (the panel forwards /api/*, so it is reachable from outside),
		// the GitHub sign-in flow, logout, and HMAC-verified GitHub webhooks (ADR-0010).
		api.Get("/healthz", h.APIHealthz)
		api.Get("/auth/github/login", h.AuthGitHubLogin)
		api.Get("/auth/github/callback", h.AuthGitHubCallback)
		api.Post("/auth/logout", h.AuthLogout)
		api.Post("/webhooks/github", h.GitHubWebhook)

		// Everything else requires a session; the user comes from the session only.
		api.Group(func(private chi.Router) {
			private.Use(h.RequireSession)

			private.Get("/auth/me", h.AuthMe)
			private.Get("/workspace", h.GetWorkspace)
			private.Get("/events/stream", h.ServeSSE)
			private.Get("/github/installations", h.ListGitHubInstallations)
			private.Get("/github/installations/{installationID}/repositories", h.ListInstallationRepositories)
			private.Post("/repositories/import", h.ImportRepository)
			private.Post("/sync", h.StartSync)
			private.Get("/sync", h.GetSync)
			private.Delete("/sync", h.CancelSync)
			private.Delete("/repositories/{id}/follow", h.UnfollowRepository)
			private.Get("/repositories/{owner}/{name}/pulls/{number}/files", h.ListPullRequestFiles)

			private.Post("/prs/batch-merge", h.BatchMergePRs)
			private.Post("/prs/{id}/approve", h.ApprovePR)
			private.Post("/prs/{id}/merge", h.MergePR)
			private.Post("/prs/{id}/snooze", h.SnoozePR)
			private.Post("/prs/{id}/claim", h.ClaimPR)
			private.Post("/prs/{id}/dependabot-command", h.DependabotCommand)
			private.Post("/prs/{id}/analyze", h.AnalyzePR)

			private.Get("/notifications", h.ListNotifications)
			private.Post("/notifications/read", h.MarkNotificationsRead)
			private.Get("/notifications/settings", h.GetNotificationSettings)
			private.Put("/notifications/settings", h.SaveNotificationSettings)

			private.Delete("/operations/{id}", h.CancelOperation)
		})
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
			w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-Operation-ID")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
