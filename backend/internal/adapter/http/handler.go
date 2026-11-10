package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/murilo/pr-follower/backend/internal/adapter/github"
	"github.com/murilo/pr-follower/backend/internal/adapter/http/operation"
	"github.com/murilo/pr-follower/backend/internal/adapter/sse"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/usecase/action"
	"github.com/murilo/pr-follower/backend/internal/usecase/auth"
	"github.com/murilo/pr-follower/backend/internal/usecase/dependabot"
	"github.com/murilo/pr-follower/backend/internal/usecase/repoimport"
	"github.com/murilo/pr-follower/backend/internal/usecase/triage"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

const StatusClientClosedRequest = 499

type HandlerDeps struct {
	UserRepo      port.UserRepository
	RepoRepo      port.RepositoryRepository
	PRRepo        port.PullRequestRepository
	TriageUC      *triage.UseCase
	ActionUC      *action.UseCase
	AnalyzeUC     *dependabot.AnalyzeUseCase
	WebhookUC     *webhook.IngestUseCase
	SSEHub        *sse.Hub
	OpRegistry    *operation.Registry
	WebhookSecret string
	AuthUC        *auth.UseCase
	SecureCookies bool
	ImportUC      *repoimport.UseCase
	InstallURL    string
	// InstallationUC syncs repository access from installation webhooks (ADR-0010).
	InstallationUC *webhook.InstallationUseCase
	// Follows scopes the live event stream to followed repositories. Nil sends nothing.
	Follows port.FollowChecker
}

type Handler struct {
	deps HandlerDeps
}

func NewHandler(deps HandlerDeps) *Handler {
	if deps.OpRegistry == nil {
		deps.OpRegistry = operation.NewRegistry()
	}
	return &Handler{deps: deps}
}

func (h *Handler) Healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ServeSSE streams live activity, scoped to the repositories the signed-in user follows:
// with open GitHub sign-in, no user may see another account's repository or PR activity.
func (h *Handler) ServeSSE(w http.ResponseWriter, r *http.Request) {
	userID := currentUserID(r)
	h.deps.SSEHub.Stream(w, r, func(ctx context.Context, ev entity.ActivityEvent) bool {
		if h.deps.Follows == nil || ev.RepositoryID == 0 {
			return false
		}
		ok, err := h.deps.Follows.IsFollowing(ctx, userID, ev.RepositoryID)
		return err == nil && ok
	})
}

func (h *Handler) GetWorkspace(w http.ResponseWriter, r *http.Request) {
	ctx, release := h.beginOperation(r)
	defer release()

	userID := currentUserID(r)
	snapshot, err := h.deps.TriageUC.GetWorkspaceSnapshot(ctx, userID)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"snapshot": snapshot,
		"timeline": h.deps.SSEHub.RecentEvents(),
	})
}

func (h *Handler) ApprovePR(w http.ResponseWriter, r *http.Request) {
	ctx, release := h.beginOperation(r)
	defer release()

	prID, err := parseUintParam(r, "id")
	if err != nil {
		writeDomainError(w, err)
		return
	}

	var req struct {
		Body string `json:"body"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := h.deps.ActionUC.ApprovePR(ctx, currentUserID(r), prID, req.Body); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pr_id": prID})
}

func (h *Handler) MergePR(w http.ResponseWriter, r *http.Request) {
	ctx, release := h.beginOperation(r)
	defer release()

	prID, err := parseUintParam(r, "id")
	if err != nil {
		writeDomainError(w, err)
		return
	}

	var req struct {
		Method string `json:"method"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if err := h.deps.ActionUC.MergePR(ctx, currentUserID(r), prID, req.Method); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pr_id": prID})
}

func (h *Handler) BatchMergePRs(w http.ResponseWriter, r *http.Request) {
	ctx, release := h.beginOperation(r)
	defer release()

	var req struct {
		PRIDs  []uint64 `json:"pr_ids"`
		Method string   `json:"method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDomainError(w, entity.ErrInvalidInput)
		return
	}

	completed, err := h.deps.ActionUC.BatchApproveAndMerge(ctx, currentUserID(r), req.PRIDs, req.Method)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "merged_ids": completed})
}

func (h *Handler) SnoozePR(w http.ResponseWriter, r *http.Request) {
	ctx, release := h.beginOperation(r)
	defer release()

	prID, err := parseUintParam(r, "id")
	if err != nil {
		writeDomainError(w, err)
		return
	}

	var req struct {
		Condition string `json:"condition"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	cond := entity.SnoozeCondition(req.Condition)
	if cond == "" {
		cond = entity.SnoozeConditionNextCommit
	}

	if err := h.deps.TriageUC.SnoozePR(ctx, currentUserID(r), prID, cond); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pr_id": prID, "condition": cond})
}

func (h *Handler) ClaimPR(w http.ResponseWriter, r *http.Request) {
	ctx, release := h.beginOperation(r)
	defer release()

	prID, err := parseUintParam(r, "id")
	if err != nil {
		writeDomainError(w, err)
		return
	}

	if err := h.deps.TriageUC.ClaimPR(ctx, currentUserID(r), prID); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pr_id": prID})
}

func (h *Handler) DependabotCommand(w http.ResponseWriter, r *http.Request) {
	ctx, release := h.beginOperation(r)
	defer release()

	prID, err := parseUintParam(r, "id")
	if err != nil {
		writeDomainError(w, err)
		return
	}

	var req struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDomainError(w, entity.ErrInvalidInput)
		return
	}

	if err := h.deps.ActionUC.RunDependabotCommand(ctx, currentUserID(r), prID, req.Command); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pr_id": prID})
}

func (h *Handler) CancelOperation(w http.ResponseWriter, r *http.Request) {
	opID := chi.URLParam(r, "id")
	if !h.deps.OpRegistry.Cancel(opID) {
		writeDomainError(w, entity.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"canceled": true, "operation_id": opID})
}

func (h *Handler) GitHubWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := io.ReadAll(io.LimitReader(r.Body, 5*1024*1024))
	if err != nil {
		writeDomainError(w, entity.ErrInvalidInput)
		return
	}

	sig := r.Header.Get("X-Hub-Signature-256")
	if err := github.VerifyWebhookSignature(h.deps.WebhookSecret, sig, payload); err != nil {
		writeDomainError(w, err)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	if eventType == "installation" || eventType == "installation_repositories" {
		if err := h.ingestInstallationWebhook(r.Context(), eventType, payload); err != nil {
			writeDomainError(w, err)
			return
		}
	}
	if eventType == "check_run" {
		var raw struct {
			CheckRun struct {
				ID         int64  `json:"id"`
				Name       string `json:"name"`
				HeadSHA    string `json:"head_sha"`
				Status     string `json:"status"`
				Conclusion string `json:"conclusion"`
			} `json:"check_run"`
			Repository struct {
				ID int64 `json:"id"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(payload, &raw); err != nil {
			writeDomainError(w, entity.ErrInvalidInput)
			return
		}
		if err := h.deps.WebhookUC.IngestCheckRunEvent(r.Context(), webhook.CheckRunEventPayload{
			CheckRunID:   raw.CheckRun.ID,
			GitHubRepoID: raw.Repository.ID,
			HeadSHA:      raw.CheckRun.HeadSHA,
			Name:         raw.CheckRun.Name,
			Status:       raw.CheckRun.Status,
			Conclusion:   raw.CheckRun.Conclusion,
		}); err != nil {
			writeDomainError(w, err)
			return
		}
	}
	if eventType == "pull_request" {
		var raw struct {
			Action      string `json:"action"`
			Number      int    `json:"number"`
			PullRequest struct {
				ID             int64  `json:"id"`
				Number         int    `json:"number"`
				Title          string `json:"title"`
				State          string `json:"state"`
				Draft          bool   `json:"draft"`
				HTMLURL        string `json:"html_url"`
				MergeableState string `json:"mergeable_state"`
				User           struct {
					Login string `json:"login"`
				} `json:"user"`
				Head struct {
					SHA string `json:"sha"`
				} `json:"head"`
				Base struct {
					Ref string `json:"ref"`
				} `json:"base"`
			} `json:"pull_request"`
			Repository struct {
				ID       int64  `json:"id"`
				FullName string `json:"full_name"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(payload, &raw); err != nil {
			writeDomainError(w, entity.ErrInvalidInput)
			return
		}

		state := entity.PRStateOpen
		if strings.EqualFold(raw.PullRequest.State, "closed") {
			state = entity.PRStateClosed
		}

		if err := h.deps.WebhookUC.IngestPullRequestEvent(r.Context(), webhook.PullRequestEventPayload{
			Action:         raw.Action,
			GitHubRepoID:   raw.Repository.ID,
			GitHubPRID:     raw.PullRequest.ID,
			Number:         raw.PullRequest.Number,
			Title:          raw.PullRequest.Title,
			AuthorLogin:    raw.PullRequest.User.Login,
			HTMLURL:        raw.PullRequest.HTMLURL,
			IsDraft:        raw.PullRequest.Draft,
			State:          state,
			HeadSHA:        raw.PullRequest.Head.SHA,
			BaseRef:        raw.PullRequest.Base.Ref,
			MergeableState: raw.PullRequest.MergeableState,
		}); err != nil {
			writeDomainError(w, err)
			return
		}
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func (h *Handler) beginOperation(r *http.Request) (context.Context, func()) {
	opID := strings.TrimSpace(r.Header.Get("X-Operation-ID"))
	return h.deps.OpRegistry.Begin(r.Context(), opID)
}

func parseUintParam(r *http.Request, key string) (uint64, error) {
	raw := chi.URLParam(r, key)
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, entity.ErrInvalidInput
	}
	return id, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeDomainError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, entity.ErrOperationCanceled), errors.Is(err, context.Canceled):
		status = StatusClientClosedRequest
	case errors.Is(err, entity.ErrInvalidInput):
		status = http.StatusBadRequest
	case errors.Is(err, entity.ErrUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, entity.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, entity.ErrConflict):
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
