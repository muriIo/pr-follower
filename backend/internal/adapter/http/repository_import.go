package httpadapter

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/syncjob"
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

// syncConfigured answers 503 when the sync is not set up (no GitHub sign-in on this server).
func (h *Handler) syncConfigured(w http.ResponseWriter) bool {
	if h.deps.SyncJobs == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "github_import_not_configured", "code": "unavailable"})
		return false
	}
	return true
}

// StartSync begins bringing every repository the user follows in line with GitHub, in the
// background, and answers at once: a sync takes minutes, far too long to hold a request open. If one
// is already running it is joined (started is false). Progress is read with GetSync.
func (h *Handler) StartSync(w http.ResponseWriter, r *http.Request) {
	if !h.syncConfigured(w) {
		return
	}
	status, started, err := h.deps.SyncJobs.Start(r.Context(), currentUserID(r))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": started, "status": syncStatusBody(status)})
}

// GetSync reports the user's latest sync: its state, progress, and result when it is done.
func (h *Handler) GetSync(w http.ResponseWriter, r *http.Request) {
	if !h.syncConfigured(w) {
		return
	}
	status, err := h.deps.SyncJobs.Status(r.Context(), currentUserID(r))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, syncStatusBody(status))
}

// CancelSync stops the user's running sync, whichever replica runs it.
func (h *Handler) CancelSync(w http.ResponseWriter, r *http.Request) {
	if !h.syncConfigured(w) {
		return
	}
	canceled, err := h.deps.SyncJobs.Cancel(r.Context(), currentUserID(r))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"canceled": canceled})
}

func syncStatusBody(st syncjob.Status) map[string]any {
	body := map[string]any{
		"state": st.State, "total": st.Progress.Total, "done": st.Progress.Done, "current": st.Progress.Current,
	}
	if !st.StartedAt.IsZero() {
		body["started_at"] = st.StartedAt.UTC().Format(time.RFC3339)
	}
	if !st.FinishedAt.IsZero() {
		body["finished_at"] = st.FinishedAt.UTC().Format(time.RFC3339)
	}
	if res := st.Result; res != nil {
		failed := make([]map[string]string, 0, len(res.Failed))
		for _, f := range res.Failed {
			_, code := statusAndCode(f.Err)
			failed = append(failed, map[string]string{"repository": f.Repository, "code": code})
		}
		result := map[string]any{
			"repositories": res.Repositories, "added": res.Added, "updated": res.Updated,
			"closed": res.Closed, "merged": res.Merged, "skipped": res.Skipped, "failed": failed,
		}
		if !res.RateLimitedUntil.IsZero() {
			result["rate_limited_until"] = res.RateLimitedUntil.UTC().Format(time.RFC3339)
		}
		body["result"] = result
	}
	if st.Err != nil {
		_, code := statusAndCode(st.Err)
		errBody := map[string]string{"code": code}
		var limited *entity.RateLimitedError
		if errors.As(st.Err, &limited) {
			errBody["retry_at"] = limited.ResetAt.UTC().Format(time.RFC3339)
		}
		body["error"] = errBody
	}
	return body
}
