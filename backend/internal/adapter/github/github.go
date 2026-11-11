package github

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

func VerifyWebhookSignature(secret, signatureHeader string, payload []byte) error {
	cleanSig := strings.TrimSpace(signatureHeader)
	if cleanSig == "" || !strings.HasPrefix(cleanSig, "sha256=") {
		return fmt.Errorf("%w: missing or malformed X-Hub-Signature-256 header", entity.ErrUnauthorized)
	}
	if strings.TrimSpace(secret) == "" {
		return nil
	}

	gotHex := strings.TrimPrefix(cleanSig, "sha256=")
	gotBytes, err := hex.DecodeString(gotHex)
	if err != nil {
		return fmt.Errorf("%w: invalid signature hex", entity.ErrUnauthorized)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedBytes := mac.Sum(nil)

	if !hmac.Equal(gotBytes, expectedBytes) {
		return fmt.Errorf("%w: webhook signature mismatch", entity.ErrUnauthorized)
	}
	return nil
}

type GatewayConfig struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
	Backoff    resilience.BackoffConfig
	// Installations (optional) authenticates reads as the GitHub App on a repository's
	// installation. Without it, or for a repository with no installation id, Token is used.
	Installations port.InstallationTokenSource
}

type Gateway struct {
	baseURL    string
	token      string
	httpClient *http.Client
	backoff    resilience.BackoffConfig
	fileGroup  resilience.SingleFlightGroup[[]byte]
	apps       port.InstallationTokenSource
}

func NewGateway(cfg GatewayConfig) *Gateway {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	backoff := cfg.Backoff
	if backoff.MaxAttempts <= 0 {
		backoff = resilience.DefaultBackoffConfig()
	}
	return &Gateway{
		baseURL:    baseURL,
		token:      cfg.Token,
		httpClient: client,
		backoff:    backoff,
		apps:       cfg.Installations,
	}
}

// readToken picks the credential for a read of the repository: the app's installation token when
// available, else the configured static token. A failure to get the app's token is returned, never
// papered over with another identity. Writes do not use it: they act as the user.
func (g *Gateway) readToken(ctx context.Context, repo entity.Repository) (string, error) {
	if g.apps != nil && repo.InstallationID != 0 {
		return g.apps.InstallationToken(ctx, repo.InstallationID)
	}
	if g.token == "" {
		return "", fmt.Errorf("%w: no credential to read %s/%s", entity.ErrUnauthorized, repo.Owner, repo.Name)
	}
	return g.token, nil
}

func setBearer(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func (g *Gateway) GetFileContent(ctx context.Context, repo entity.Repository, ref, filePath string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	key := fmt.Sprintf("file:%d:%s:%s", repo.ID, ref, filePath)
	return g.fileGroup.Do(ctx, key, func(callCtx context.Context) ([]byte, error) {
		token, err := g.readToken(callCtx, repo)
		if err != nil {
			return nil, err
		}
		url := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s", g.baseURL, repo.Owner, repo.Name, strings.TrimPrefix(filePath, "/"), ref)
		return resilience.Retry(callCtx, g.backoff, func(reqCtx context.Context) ([]byte, error) {
			req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
			if err != nil {
				return nil, resilience.Permanent(err)
			}
			req.Header.Set("Accept", "application/vnd.github.v3.raw")
			setBearer(req, token)

			resp, err := g.httpClient.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()

			if err := classifyHTTPStatus(resp); err != nil {
				return nil, err
			}
			return io.ReadAll(resp.Body)
		})
	})
}

// prFilesPerPage follows GitHub's limit of 100 per page (prFilesMaxPages, in userapi.go, caps the
// pages at GitHub's 3,000 files).
const prFilesPerPage = 100

func (g *Gateway) ListPRChangedFiles(ctx context.Context, repo entity.Repository, prNumber int) ([]entity.ChangedFile, error) {
	token, err := g.readToken(ctx, repo)
	if err != nil {
		return nil, err
	}
	var files []entity.ChangedFile
	for page := 1; page <= prFilesMaxPages; page++ {
		url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/files?per_page=%d&page=%d", g.baseURL, repo.Owner, repo.Name, prNumber, prFilesPerPage, page)
		batch, err := resilience.Retry(ctx, g.backoff, func(reqCtx context.Context) ([]entity.ChangedFile, error) {
			req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
			if err != nil {
				return nil, resilience.Permanent(err)
			}
			req.Header.Set("Accept", "application/vnd.github+json")
			setBearer(req, token)

			resp, err := g.httpClient.Do(req)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()

			if err := classifyHTTPStatus(resp); err != nil {
				return nil, err
			}

			var raw []struct {
				Filename  string `json:"filename"`
				Additions int    `json:"additions"`
				Deletions int    `json:"deletions"`
				Patch     string `json:"patch"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
				return nil, resilience.Permanent(err)
			}
			out := make([]entity.ChangedFile, 0, len(raw))
			for _, item := range raw {
				out = append(out, entity.ChangedFile{Path: item.Filename, Additions: item.Additions, Deletions: item.Deletions, Patch: item.Patch})
			}
			return out, nil
		})
		if err != nil {
			return nil, err
		}
		files = append(files, batch...)
		if len(batch) < prFilesPerPage {
			break
		}
	}
	return files, nil
}

func (g *Gateway) StreamRepoTarball(ctx context.Context, repo entity.Repository, ref string) (io.ReadCloser, error) {
	token, err := g.readToken(ctx, repo)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/repos/%s/%s/tarball/%s", g.baseURL, repo.Owner, repo.Name, ref)
	return resilience.Retry(ctx, g.backoff, func(reqCtx context.Context) (io.ReadCloser, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
		if err != nil {
			return nil, resilience.Permanent(err)
		}
		setBearer(req, token)

		resp, err := g.httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		if err := classifyHTTPStatus(resp); err != nil {
			_ = resp.Body.Close()
			return nil, err
		}
		return resp.Body, nil
	})
}

// ApprovePullRequest approves the PR as the user. The POST is never repeated blindly (ADR-0011):
// an answer that never came is entity.ErrOutcomeUnknown. It returns the review GitHub recorded.
func (g *Gateway) ApprovePullRequest(ctx context.Context, accessToken string, repo entity.Repository, prNumber int, body string) (entity.PullRequestReview, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/reviews", g.baseURL, repo.Owner, repo.Name, prNumber)
	payload, _ := json.Marshal(map[string]string{"event": "APPROVE", "body": body})
	var review entity.PullRequestReview
	err := g.write(ctx, accessToken, http.MethodPost, url, payload, false, func(respBody io.Reader) {
		var raw struct {
			ID          int64  `json:"id"`
			State       string `json:"state"`
			CommitID    string `json:"commit_id"`
			SubmittedAt string `json:"submitted_at"`
			User        *struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if json.NewDecoder(io.LimitReader(respBody, 1<<20)).Decode(&raw) != nil {
			return
		}
		review = entity.PullRequestReview{GitHubReviewID: raw.ID, State: entity.ReviewStateFromGitHub(raw.State), CommitSHA: raw.CommitID}
		if raw.User != nil {
			review.ReviewerLogin = raw.User.Login
		}
		review.SubmittedAt, _ = time.Parse(time.RFC3339, raw.SubmittedAt)
	})
	return review, err
}

// MergePullRequest merges the PR as the user. PUT merge is idempotent, so it is retried.
func (g *Gateway) MergePullRequest(ctx context.Context, accessToken string, repo entity.Repository, prNumber int, method string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/merge", g.baseURL, repo.Owner, repo.Name, prNumber)
	payload, _ := json.Marshal(map[string]string{"merge_method": method})
	return g.write(ctx, accessToken, http.MethodPut, url, payload, true, nil)
}

// CreatePRComment comments as the user; like a review, the POST is never repeated blindly.
func (g *Gateway) CreatePRComment(ctx context.Context, accessToken string, repo entity.Repository, prNumber int, comment string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", g.baseURL, repo.Owner, repo.Name, prNumber)
	payload, _ := json.Marshal(map[string]string{"body": comment})
	return g.write(ctx, accessToken, http.MethodPost, url, payload, false, nil)
}

// write sends a change to GitHub with the user's token and classifies the answer: a refusal is a
// definite error that keeps GitHub's explanation; no answer (transport failure, 5xx) is
// entity.ErrOutcomeUnknown because the change may have been applied.
func (g *Gateway) write(ctx context.Context, accessToken, method, url string, payload []byte, idempotent bool, onSuccess func(io.Reader)) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if strings.TrimSpace(accessToken) == "" {
		return fmt.Errorf("%w: a signed-in user's token is required to act on github", entity.ErrUnauthorized)
	}
	attempt := func(reqCtx context.Context) (struct{}, error) {
		req, err := http.NewRequestWithContext(reqCtx, method, url, bytes.NewReader(payload))
		if err != nil {
			return struct{}{}, resilience.Permanent(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/vnd.github+json")
		setBearer(req, accessToken)

		resp, err := g.httpClient.Do(req)
		if err != nil {
			if reqCtx.Err() != nil {
				return struct{}{}, err
			}
			return struct{}{}, fmt.Errorf("%w: %v", entity.ErrOutcomeUnknown, err)
		}
		defer resp.Body.Close()
		if err := classifyWrite(resp); err != nil {
			return struct{}{}, err
		}
		if onSuccess != nil {
			onSuccess(resp.Body)
		}
		return struct{}{}, nil
	}
	var err error
	if idempotent {
		_, err = resilience.Retry(ctx, g.backoff, attempt)
	} else {
		_, err = resilience.RetryNonIdempotent(ctx, g.backoff, attempt)
	}
	return err
}

// classifyWrite maps GitHub's answer to a change. Retry-After answers mean "not processed" and
// are retried; the final error of a 4xx is permanent and carries GitHub's message.
func classifyWrite(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	message := githubMessage(resp)
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return resilience.Permanent(fmt.Errorf("%w: %s", entity.ErrUnauthorized, message))
	case resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode == http.StatusForbidden && resp.Header.Get("Retry-After") != ""):
		return classifyHTTPStatus(resp)
	case resp.StatusCode == http.StatusForbidden:
		return resilience.Permanent(fmt.Errorf("%w: %s", entity.ErrForbidden, message))
	case resp.StatusCode == http.StatusNotFound:
		return resilience.Permanent(fmt.Errorf("%w: %s", entity.ErrNotFound, message))
	case resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusUnprocessableEntity:
		return resilience.Permanent(fmt.Errorf("%w: %s", entity.ErrConflict, message))
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: github server error (%d)", entity.ErrOutcomeUnknown, resp.StatusCode)
	default:
		return resilience.Permanent(fmt.Errorf("github refused the change (%d): %s", resp.StatusCode, message))
	}
}

// githubMessage reads the explanation GitHub puts in an error body ({"message": "..."}).
func githubMessage(resp *http.Response) string {
	var body struct {
		Message string `json:"message"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body) != nil || body.Message == "" {
		return http.StatusText(resp.StatusCode)
	}
	return body.Message
}

func classifyHTTPStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
				return resilience.RetryAfter(fmt.Errorf("github rate limited (%d)", resp.StatusCode), time.Duration(secs)*time.Second)
			}
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return fmt.Errorf("github rate limited (429)")
		}
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("github server error (%d)", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound {
		return resilience.Permanent(entity.ErrNotFound)
	}
	return resilience.Permanent(fmt.Errorf("github permanent error (%d)", resp.StatusCode))
}
