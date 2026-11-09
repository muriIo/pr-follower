package httpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/murilo/pr-follower/backend/internal/adapter/github"
	"github.com/murilo/pr-follower/backend/internal/adapter/http/operation"
	"github.com/murilo/pr-follower/backend/internal/adapter/sse"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/usecase/action"
	"github.com/murilo/pr-follower/backend/internal/usecase/dependabot"
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

func (h *Handler) ServeSSE(w http.ResponseWriter, r *http.Request) {
	h.deps.SSEHub.ServeHTTP(w, r)
}

func (h *Handler) GetWorkspace(w http.ResponseWriter, r *http.Request) {
	ctx, release := h.beginOperation(r)
	defer release()

	userID := resolveUserID(r)
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

	if err := h.deps.ActionUC.ApprovePR(ctx, resolveUserID(r), prID, req.Body); err != nil {
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

	if err := h.deps.ActionUC.MergePR(ctx, resolveUserID(r), prID, req.Method); err != nil {
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

	completed, err := h.deps.ActionUC.BatchApproveAndMerge(ctx, resolveUserID(r), req.PRIDs, req.Method)
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

	if err := h.deps.TriageUC.SnoozePR(ctx, resolveUserID(r), prID, cond); err != nil {
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

	if err := h.deps.TriageUC.ClaimPR(ctx, resolveUserID(r), prID); err != nil {
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

	if err := h.deps.ActionUC.RunDependabotCommand(ctx, resolveUserID(r), prID, req.Command); err != nil {
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
	if eventType == "pull_request" {
		var raw struct {
			Action      string `json:"action"`
			Number      int    `json:"number"`
			PullRequest struct {
				ID      int64  `json:"id"`
				Number  int    `json:"number"`
				Title   string `json:"title"`
				State   string `json:"state"`
				Draft   bool   `json:"draft"`
				HTMLURL string `json:"html_url"`
				User    struct {
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
			Action:       raw.Action,
			GitHubRepoID: raw.Repository.ID,
			GitHubPRID:   raw.PullRequest.ID,
			Number:       raw.PullRequest.Number,
			Title:        raw.PullRequest.Title,
			AuthorLogin:  raw.PullRequest.User.Login,
			HTMLURL:      raw.PullRequest.HTMLURL,
			IsDraft:      raw.PullRequest.Draft,
			State:        state,
			HeadSHA:      raw.PullRequest.Head.SHA,
			BaseRef:      raw.PullRequest.Base.Ref,
		}); err != nil {
			writeDomainError(w, err)
			return
		}
	}

	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func (h *Handler) SeedDemoData(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := &entity.User{
		GitHubID:  1001,
		Login:     "murilo",
		Name:      "Murilo",
		AvatarURL: "https://avatars.githubusercontent.com/u/1001",
		Teams:     []string{"@arvore/frontend", "@arvore/core-architects"},
	}
	if err := h.deps.UserRepo.Upsert(ctx, user); err != nil {
		writeDomainError(w, err)
		return
	}

	repos := []entity.Repository{
		{
			GitHubRepoID:  5001,
			Owner:         "arvore",
			Name:          "hive-web",
			FullName:      "arvore/hive-web",
			DefaultBranch: "main",
			CachedCodeownersRules: []entity.CodeownerRule{
				{Pattern: "/src/app/", Owners: []string{"@murilo", "@arvore/frontend"}},
			},
		},
		{
			GitHubRepoID:  5002,
			Owner:         "arvore",
			Name:          "checkout-api",
			FullName:      "arvore/checkout-api",
			DefaultBranch: "main",
			CachedCodeownersRules: []entity.CodeownerRule{
				{Pattern: "*", Owners: []string{"@arvore/core-architects"}},
			},
		},
	}

	for i := range repos {
		if err := h.deps.RepoRepo.Upsert(ctx, &repos[i]); err != nil {
			writeDomainError(w, err)
			return
		}
		_ = h.deps.RepoRepo.SetUserFollow(ctx, user.ID, repos[i].ID, true)
	}

	pr1 := &entity.PullRequest{
		RepositoryID:   repos[0].ID,
		RepositoryName: repos[0].FullName,
		GitHubPRID:     80001,
		Number:         142,
		Title:          "feat(seats): add real-time CI status pulse to repo tiles",
		AuthorLogin:    "bianca-dev",
		HTMLURL:        "https://github.com/arvore/hive-web/pull/142",
		IsDependabot:   false,
		IsDraft:        false,
		State:          entity.PRStateOpen,
		MergeableState: entity.MergeableStateClean,
		CIStatus:       entity.CIStatusSuccess,
		HeadSHA:        "7f8a9b1",
		BaseRef:        "main",
	}
	_ = h.deps.PRRepo.Upsert(ctx, pr1)
	_ = h.deps.PRRepo.UpsertUserPRState(ctx, &entity.UserPRState{
		PullRequestID: pr1.ID,
		UserID:        user.ID,
		TurnState:     entity.TurnStateWaitingOnMe,
		OwnedFilesChanged: []entity.ChangedFile{
			{
				Path:      "src/app/features/seats-grid/seat-card.component.ts",
				Additions: 24,
				Deletions: 3,
				Patch:     "@@ -12,3 +12,24 @@\n+  readonly ciPulse = computed(() => this.seat().failingCiCount === 0);",
			},
		},
	})

	pr2 := &entity.PullRequest{
		RepositoryID:   repos[0].ID,
		RepositoryName: repos[0].FullName,
		GitHubPRID:     80002,
		Number:         145,
		Title:          "chore(deps-dev): bump vitest from 2.1.1 to 2.1.4",
		AuthorLogin:    "dependabot[bot]",
		HTMLURL:        "https://github.com/arvore/hive-web/pull/145",
		IsDependabot:   true,
		State:          entity.PRStateOpen,
		MergeableState: entity.MergeableStateClean,
		CIStatus:       entity.CIStatusSuccess,
		HeadSHA:        "9c0d1e2",
		BaseRef:        "main",
	}
	_ = h.deps.PRRepo.Upsert(ctx, pr2)
	_ = h.deps.PRRepo.SaveAnalysis(ctx, &entity.DependabotAnalysis{
		PullRequestID:     pr2.ID,
		HeadSHA:           pr2.HeadSHA,
		Ecosystem:         "npm",
		PackageName:       "vitest",
		FromVersion:       "2.1.1",
		ToVersion:         "2.1.4",
		SemverBumpType:    entity.SemverBumpPatch,
		IsDirectDep:       true,
		DepScope:          entity.DependencyScopeDev,
		RiskVerdict:       entity.RiskVerdictLow,
		RiskScore:         12,
		VerdictSummaryKey: "dependabot.verdict.low.dev_safe",
		DependencyPathGraph: []entity.DependencyNode{
			{Name: "hive-web", Version: "root", Scope: entity.DependencyScopeProd},
			{Name: "vitest", Version: "2.1.4", Scope: entity.DependencyScopeDev},
		},
	})

	pr3 := &entity.PullRequest{
		RepositoryID:   repos[1].ID,
		RepositoryName: repos[1].FullName,
		GitHubPRID:     80003,
		Number:         89,
		Title:          "chore(deps): bump axios from 1.7.7 to 2.0.0",
		AuthorLogin:    "dependabot[bot]",
		HTMLURL:        "https://github.com/arvore/checkout-api/pull/89",
		IsDependabot:   true,
		State:          entity.PRStateOpen,
		MergeableState: entity.MergeableStateClean,
		CIStatus:       entity.CIStatusSuccess,
		HeadSHA:        "3a4b5c6",
		BaseRef:        "main",
	}
	_ = h.deps.PRRepo.Upsert(ctx, pr3)
	_ = h.deps.PRRepo.SaveAnalysis(ctx, &entity.DependabotAnalysis{
		PullRequestID:     pr3.ID,
		HeadSHA:           pr3.HeadSHA,
		Ecosystem:         "npm",
		PackageName:       "axios",
		FromVersion:       "1.7.7",
		ToVersion:         "2.0.0",
		SemverBumpType:    entity.SemverBumpMajor,
		IsDirectDep:       true,
		DepScope:          entity.DependencyScopeProd,
		RiskVerdict:       entity.RiskVerdictHigh,
		RiskScore:         88,
		VerdictSummaryKey: "dependabot.verdict.high.direct_prod_major",
		DependencyPathGraph: []entity.DependencyNode{
			{Name: "checkout-api", Version: "root", Scope: entity.DependencyScopeProd},
			{Name: "axios", Version: "2.0.0", Scope: entity.DependencyScopeProd},
		},
		BlastRadiusUsages: []entity.BlastRadiusUsage{
			{
				FilePath:    "src/infra/payment-gateway.client.ts",
				LineNumber:  4,
				ImportStmt:  "import axios, { AxiosInstance } from 'axios';",
				CodeSnippet: "import { Injectable } from '@nestjs/common';\nimport axios, { AxiosInstance } from 'axios';\n\n@Injectable()",
			},
		},
		AdvisoriesAndNotes: []entity.AdvisoryAndNote{
			{
				Severity:       "HIGH",
				Summary:        "Major v2.0.0 drops legacy CommonJS default export behavior",
				IsBreakingNote: true,
			},
		},
	})

	_ = h.deps.SSEHub.Publish(ctx, entity.ActivityEvent{
		ID:             "seed-1",
		Type:           "workspace.seeded",
		RepositoryID:   repos[0].ID,
		RepositoryName: repos[0].FullName,
		ActorLogin:     "murilo",
		Summary:        "Workspace demo data initialized",
		OccurredAt:     time.Now().UTC(),
	})

	writeJSON(w, http.StatusOK, map[string]any{"seeded": true, "user_id": user.ID})
}

func (h *Handler) beginOperation(r *http.Request) (context.Context, func()) {
	opID := strings.TrimSpace(r.Header.Get("X-Operation-ID"))
	return h.deps.OpRegistry.Begin(r.Context(), opID)
}

func resolveUserID(r *http.Request) uint64 {
	if raw := strings.TrimSpace(r.Header.Get("X-User-ID")); raw != "" {
		if id, err := strconv.ParseUint(raw, 10, 64); err == nil && id > 0 {
			return id
		}
	}
	return 1
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
