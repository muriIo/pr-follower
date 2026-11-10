package httpadapter

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

// Vercel-style repository import (ADR-0010 §5). Every listing and import is checked against
// GitHub with the signed-in user's token.

func (h *Handler) importConfigured(w http.ResponseWriter) bool {
	if h.deps.ImportUC == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "github_import_not_configured"})
		return false
	}
	return true
}

// ListGitHubInstallations returns the accounts where the app is installed and the user has access,
// plus the URL for installing it on another account.
func (h *Handler) ListGitHubInstallations(w http.ResponseWriter, r *http.Request) {
	if !h.importConfigured(w) {
		return
	}
	ctx, release := h.beginOperation(r)
	defer release()

	accounts, err := h.deps.ImportUC.ListAccounts(ctx, currentUserID(r))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if accounts == nil {
		accounts = []entity.Installation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts, "install_url": h.deps.InstallURL})
}

func (h *Handler) ListInstallationRepositories(w http.ResponseWriter, r *http.Request) {
	if !h.importConfigured(w) {
		return
	}
	installationID, err := strconv.ParseInt(chi.URLParam(r, "installationID"), 10, 64)
	if err != nil || installationID <= 0 {
		writeDomainError(w, entity.ErrInvalidInput)
		return
	}
	ctx, release := h.beginOperation(r)
	defer release()

	repos, err := h.deps.ImportUC.ListRepositories(ctx, currentUserID(r), installationID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": repos})
}

func (h *Handler) ImportRepository(w http.ResponseWriter, r *http.Request) {
	if !h.importConfigured(w) {
		return
	}
	var req struct {
		InstallationID int64 `json:"installation_id"`
		GitHubRepoID   int64 `json:"github_repo_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDomainError(w, entity.ErrInvalidInput)
		return
	}
	ctx, release := h.beginOperation(r)
	defer release()

	repo, err := h.deps.ImportUC.Import(ctx, currentUserID(r), req.InstallationID, req.GitHubRepoID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repository": repo})
}

func (h *Handler) UnfollowRepository(w http.ResponseWriter, r *http.Request) {
	if !h.importConfigured(w) {
		return
	}
	repoID, err := parseUintParam(r, "id")
	if err != nil {
		writeDomainError(w, err)
		return
	}
	ctx, release := h.beginOperation(r)
	defer release()

	if err := h.deps.ImportUC.Unfollow(ctx, currentUserID(r), repoID); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
