package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/adapter/github"
	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

func newUserClient(server *httptest.Server) *github.UserClient {
	return github.NewUserClient(github.UserClientConfig{
		APIBaseURL: server.URL,
		HTTPClient: server.Client(),
		Backoff:    resilience.BackoffConfig{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond},
	})
}

func TestUserClient_ListInstallations(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/installations" || r.Header.Get("Authorization") != "Bearer ghu_a" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": 2,
			"installations": []map[string]any{
				{"id": 1, "account": map[string]any{"login": "octocat", "type": "User", "avatar_url": "https://a/1"},
					"repository_selection": "all", "html_url": "https://github.com/settings/installations/1"},
				{"id": 2, "account": map[string]any{"login": "acme", "type": "Organization", "avatar_url": "https://a/2"},
					"repository_selection": "selected", "html_url": "https://github.com/organizations/acme/settings/installations/2"},
			},
		})
	}))
	defer server.Close()

	got, err := newUserClient(server).ListInstallations(context.Background(), "ghu_a")
	if err != nil {
		t.Fatalf("ListInstallations() error: %v", err)
	}
	want := []entity.Installation{
		{ID: 1, AccountLogin: "octocat", AccountType: "User", AccountAvatarURL: "https://a/1", RepositorySelection: "all",
			SettingsURL: "https://github.com/settings/installations/1"},
		{ID: 2, AccountLogin: "acme", AccountType: "Organization", AccountAvatarURL: "https://a/2", RepositorySelection: "selected",
			SettingsURL: "https://github.com/organizations/acme/settings/installations/2"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestUserClient_ListInstallationRepositoriesPaginates(t *testing.T) {
	t.Parallel()
	const total = 150
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/installations/9/repositories" {
			http.NotFound(w, r)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if perPage != 100 {
			t.Errorf("expected per_page=100, got %d", perPage)
		}
		var repos []map[string]any
		for i := (page-1)*perPage + 1; i <= min(page*perPage, total); i++ {
			repos = append(repos, map[string]any{
				"id": i, "name": fmt.Sprintf("r%d", i), "full_name": fmt.Sprintf("acme/r%d", i),
				"owner": map[string]any{"login": "acme"}, "private": i%2 == 0, "default_branch": "main",
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": total, "repositories": repos})
	}))
	defer server.Close()

	got, err := newUserClient(server).ListInstallationRepositories(context.Background(), "ghu_a", 9)
	if err != nil {
		t.Fatalf("ListInstallationRepositories() error: %v", err)
	}
	if len(got) != total {
		t.Fatalf("expected %d repositories across pages, got %d", total, len(got))
	}
	first := got[0]
	if first.GitHubRepoID != 1 || first.InstallationID != 9 || first.Owner != "acme" || first.Name != "r1" ||
		first.FullName != "acme/r1" || first.DefaultBranch != "main" || first.Private {
		t.Fatalf("unexpected mapping: %+v", first)
	}
	if !got[1].Private {
		t.Fatal("expected private flag to be mapped")
	}
}

func TestUserClient_MapsAuthAndNotFoundErrors(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer revoked" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	c := newUserClient(server)

	if _, err := c.ListInstallations(context.Background(), "revoked"); !errors.Is(err, entity.ErrUnauthorized) {
		t.Errorf("revoked token: expected ErrUnauthorized, got %v", err)
	}
	if _, err := c.ListInstallationRepositories(context.Background(), "ghu_a", 404); !errors.Is(err, entity.ErrNotFound) {
		t.Errorf("unknown installation: expected ErrNotFound, got %v", err)
	}
}

func TestUserClient_HonorsCanceledContext(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 0})
	}))
	defer server.Close()
	c := newUserClient(server)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.ListInstallations(ctx, "t"); !errors.Is(err, context.Canceled) {
		t.Errorf("ListInstallations: expected context.Canceled, got %v", err)
	}
	if _, err := c.ListInstallationRepositories(ctx, "t", 1); !errors.Is(err, context.Canceled) {
		t.Errorf("ListInstallationRepositories: expected context.Canceled, got %v", err)
	}
}

func TestUserClient_GetRepositoryFile(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghu_a" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/repos/acme/web/contents/.github/CODEOWNERS" {
			if r.Header.Get("Accept") != "application/vnd.github.raw+json" {
				t.Errorf("expected the raw media type, got %q", r.Header.Get("Accept"))
			}
			_, _ = w.Write([]byte("* @acme/web\n"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	c := newUserClient(server)
	ctx := context.Background()

	got, err := c.GetRepositoryFile(ctx, "ghu_a", "acme", "web", ".github/CODEOWNERS")
	if err != nil || string(got) != "* @acme/web\n" {
		t.Fatalf("GetRepositoryFile() = %q, %v", got, err)
	}
	if _, err := c.GetRepositoryFile(ctx, "ghu_a", "acme", "web", "CODEOWNERS"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("missing file: expected ErrNotFound, got %v", err)
	}
	if _, err := c.GetRepositoryFile(ctx, "revoked", "acme", "web", "CODEOWNERS"); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("revoked token: expected ErrUnauthorized, got %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.GetRepositoryFile(canceled, "ghu_a", "acme", "web", "CODEOWNERS"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: expected context.Canceled, got %v", err)
	}
}

func TestUserClient_ListPullRequestFilesPaginates(t *testing.T) {
	t.Parallel()
	const total = 130
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghu_a" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/repos/acme/web/pulls/7/files" {
			http.NotFound(w, r)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		var files []map[string]any
		for i := (page-1)*100 + 1; i <= min(page*100, total); i++ {
			file := map[string]any{"filename": fmt.Sprintf("src/f%d.ts", i), "status": "modified", "additions": i, "deletions": 1}
			if i == 1 {
				file["patch"] = "@@ -1 +1 @@\n-old\n+new"
				file["status"] = "added"
			}
			files = append(files, file)
		}
		_ = json.NewEncoder(w).Encode(files)
	}))
	defer server.Close()
	c := newUserClient(server)

	files, err := c.ListPullRequestFiles(context.Background(), "ghu_a", "acme", "web", 7)
	if err != nil {
		t.Fatalf("ListPullRequestFiles() error: %v", err)
	}
	if len(files) != total {
		t.Fatalf("expected %d files across pages, got %d", total, len(files))
	}
	first := files[0]
	if first.Path != "src/f1.ts" || first.Status != "added" || first.Additions != 1 || first.Deletions != 1 || first.Patch == "" {
		t.Fatalf("unexpected mapping: %+v", first)
	}
	if files[1].Patch != "" {
		t.Fatal("GitHub omits patches for large or binary files; none must be invented")
	}

	if _, err := c.ListPullRequestFiles(context.Background(), "ghu_a", "acme", "web", 404); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("unknown PR: expected ErrNotFound, got %v", err)
	}
	if _, err := c.ListPullRequestFiles(context.Background(), "revoked", "acme", "web", 7); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("revoked token: expected ErrUnauthorized, got %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ListPullRequestFiles(canceled, "ghu_a", "acme", "web", 7); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: expected context.Canceled, got %v", err)
	}
}

func TestUserClient_ListOpenPullRequestsPaginatesAndMaps(t *testing.T) {
	t.Parallel()
	const total = 130
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghu_a" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/repos/acme/web/pulls" || r.URL.Query().Get("state") != "open" {
			http.NotFound(w, r)
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		var prs []map[string]any
		for i := (page-1)*100 + 1; i <= min(page*100, total); i++ {
			prs = append(prs, map[string]any{
				"id": 5000 + i, "number": i, "title": fmt.Sprintf("PR %d", i), "html_url": fmt.Sprintf("https://github.com/acme/web/pull/%d", i),
				"draft": i == 2, "user": map[string]any{"login": "bia"},
				"head": map[string]any{"sha": fmt.Sprintf("sha%d", i)}, "base": map[string]any{"ref": "main"},
			})
		}
		_ = json.NewEncoder(w).Encode(prs)
	}))
	defer server.Close()
	c := newUserClient(server)

	prs, err := c.ListOpenPullRequests(context.Background(), "ghu_a", "acme", "web")
	if err != nil {
		t.Fatalf("ListOpenPullRequests() error: %v", err)
	}
	if len(prs) != total {
		t.Fatalf("expected %d PRs across pages, got %d", total, len(prs))
	}
	first := prs[0]
	if first.GitHubPRID != 5001 || first.Number != 1 || first.Title != "PR 1" || first.AuthorLogin != "bia" ||
		first.HeadSHA != "sha1" || first.BaseRef != "main" || first.HTMLURL != "https://github.com/acme/web/pull/1" || first.IsDraft {
		t.Fatalf("unexpected mapping: %+v", first)
	}
	if !prs[1].IsDraft {
		t.Fatal("a draft PR must be reported as draft")
	}

	if _, err := c.ListOpenPullRequests(context.Background(), "ghu_a", "acme", "missing"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("unknown repository: expected ErrNotFound, got %v", err)
	}
	if _, err := c.ListOpenPullRequests(context.Background(), "revoked", "acme", "web"); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("revoked token: expected ErrUnauthorized, got %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ListOpenPullRequests(canceled, "ghu_a", "acme", "web"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: expected context.Canceled, got %v", err)
	}
}

func TestUserClient_ListCommitCheckRuns(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghu_a" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/repos/acme/web/commits/abc123/check-runs" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"total_count": 2,
			"check_runs": []map[string]any{
				{"id": 11, "name": "build", "status": "completed", "conclusion": "success", "head_sha": "abc123"},
				{"id": 12, "name": "test", "status": "in_progress", "conclusion": nil, "head_sha": "abc123"},
			},
		})
	}))
	defer server.Close()
	c := newUserClient(server)

	runs, err := c.ListCommitCheckRuns(context.Background(), "ghu_a", "acme", "web", "abc123")
	if err != nil {
		t.Fatalf("ListCommitCheckRuns() error: %v", err)
	}
	if len(runs) != 2 || runs[0] != (entity.CheckRun{ID: 11, HeadSHA: "abc123", Name: "build", Status: "completed", Conclusion: "success"}) ||
		runs[1].Status != "in_progress" || runs[1].Conclusion != "" {
		t.Fatalf("unexpected check runs: %+v", runs)
	}

	if _, err := c.ListCommitCheckRuns(context.Background(), "ghu_a", "acme", "web", "nope"); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("unknown commit: expected ErrNotFound, got %v", err)
	}
	if _, err := c.ListCommitCheckRuns(context.Background(), "revoked", "acme", "web", "abc123"); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("revoked token: expected ErrUnauthorized, got %v", err)
	}
}

func TestUserClient_ListPullRequestReviews(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer ghu_a" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/repos/acme/web/pulls/7/reviews" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "user": map[string]any{"login": "bob"}, "state": "APPROVED", "commit_id": "sha7", "submitted_at": "2026-11-10T09:00:00Z"},
			{"id": 2, "user": map[string]any{"login": "carol"}, "state": "CHANGES_REQUESTED", "commit_id": "sha7", "submitted_at": "2026-11-10T09:05:00Z"},
			{"id": 3, "user": map[string]any{"login": "dave"}, "state": "PENDING", "commit_id": "sha7"},
			{"id": 4, "user": nil, "state": "COMMENTED", "commit_id": "sha7", "submitted_at": "2026-11-10T09:06:00Z"},
		})
	}))
	defer server.Close()
	c := newUserClient(server)

	reviews, err := c.ListPullRequestReviews(context.Background(), "ghu_a", "acme", "web", 7)
	if err != nil {
		t.Fatalf("ListPullRequestReviews() error: %v", err)
	}
	if len(reviews) != 3 {
		t.Fatalf("a draft (PENDING) review is nobody's decision and must be skipped: %+v", reviews)
	}
	first := reviews[0]
	if first.GitHubReviewID != 1 || first.ReviewerLogin != "bob" || first.State != entity.ReviewApproved || first.CommitSHA != "sha7" ||
		!first.SubmittedAt.Equal(time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected mapping: %+v", first)
	}
	if reviews[1].State != entity.ReviewChangesRequested || reviews[2].ReviewerLogin != "" {
		t.Fatalf("unexpected mapping: %+v", reviews)
	}

	if _, err := c.ListPullRequestReviews(context.Background(), "ghu_a", "acme", "web", 8); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("unknown PR: expected ErrNotFound, got %v", err)
	}
	if _, err := c.ListPullRequestReviews(context.Background(), "revoked", "acme", "web", 7); !errors.Is(err, entity.ErrUnauthorized) {
		t.Fatalf("revoked token: expected ErrUnauthorized, got %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ListPullRequestReviews(canceled, "ghu_a", "acme", "web", 7); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: expected context.Canceled, got %v", err)
	}
}
