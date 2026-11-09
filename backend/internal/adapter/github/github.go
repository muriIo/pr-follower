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
}

type Gateway struct {
	baseURL    string
	token      string
	httpClient *http.Client
	backoff    resilience.BackoffConfig
	fileGroup  resilience.SingleFlightGroup[[]byte]
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
	}
}

func (g *Gateway) GetFileContent(ctx context.Context, repo entity.Repository, ref, filePath string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	key := fmt.Sprintf("file:%d:%s:%s", repo.ID, ref, filePath)
	return g.fileGroup.Do(ctx, key, func(callCtx context.Context) ([]byte, error) {
		url := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s", g.baseURL, repo.Owner, repo.Name, strings.TrimPrefix(filePath, "/"), ref)
		return resilience.Retry(callCtx, g.backoff, func(reqCtx context.Context) ([]byte, error) {
			req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
			if err != nil {
				return nil, resilience.Permanent(err)
			}
			req.Header.Set("Accept", "application/vnd.github.v3.raw")
			g.applyAuth(req)

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

func (g *Gateway) ListPRChangedFiles(ctx context.Context, repo entity.Repository, prNumber int) ([]entity.ChangedFile, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/files", g.baseURL, repo.Owner, repo.Name, prNumber)
	return resilience.Retry(ctx, g.backoff, func(reqCtx context.Context) ([]entity.ChangedFile, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
		if err != nil {
			return nil, resilience.Permanent(err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		g.applyAuth(req)

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

		files := make([]entity.ChangedFile, 0, len(raw))
		for _, item := range raw {
			files = append(files, entity.ChangedFile{
				Path:      item.Filename,
				Additions: item.Additions,
				Deletions: item.Deletions,
				Patch:     item.Patch,
			})
		}
		return files, nil
	})
}

func (g *Gateway) StreamRepoTarball(ctx context.Context, repo entity.Repository, ref string) (io.ReadCloser, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/tarball/%s", g.baseURL, repo.Owner, repo.Name, ref)
	return resilience.Retry(ctx, g.backoff, func(reqCtx context.Context) (io.ReadCloser, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
		if err != nil {
			return nil, resilience.Permanent(err)
		}
		g.applyAuth(req)

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

func (g *Gateway) ApprovePullRequest(ctx context.Context, repo entity.Repository, prNumber int, body string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/reviews", g.baseURL, repo.Owner, repo.Name, prNumber)
	payload, _ := json.Marshal(map[string]string{
		"event": "APPROVE",
		"body":  body,
	})
	_, err := resilience.Retry(ctx, g.backoff, func(reqCtx context.Context) (bool, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return false, resilience.Permanent(err)
		}
		req.Header.Set("Content-Type", "application/json")
		g.applyAuth(req)

		resp, err := g.httpClient.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if err := classifyHTTPStatus(resp); err != nil {
			return false, err
		}
		return true, nil
	})
	return err
}

func (g *Gateway) MergePullRequest(ctx context.Context, repo entity.Repository, prNumber int, method string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/merge", g.baseURL, repo.Owner, repo.Name, prNumber)
	payload, _ := json.Marshal(map[string]string{
		"merge_method": method,
	})
	_, err := resilience.Retry(ctx, g.backoff, func(reqCtx context.Context) (bool, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPut, url, bytes.NewReader(payload))
		if err != nil {
			return false, resilience.Permanent(err)
		}
		req.Header.Set("Content-Type", "application/json")
		g.applyAuth(req)

		resp, err := g.httpClient.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if err := classifyHTTPStatus(resp); err != nil {
			return false, err
		}
		return true, nil
	})
	return err
}

func (g *Gateway) CreatePRComment(ctx context.Context, repo entity.Repository, prNumber int, comment string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", g.baseURL, repo.Owner, repo.Name, prNumber)
	payload, _ := json.Marshal(map[string]string{
		"body": comment,
	})
	_, err := resilience.Retry(ctx, g.backoff, func(reqCtx context.Context) (bool, error) {
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			return false, resilience.Permanent(err)
		}
		req.Header.Set("Content-Type", "application/json")
		g.applyAuth(req)

		resp, err := g.httpClient.Do(req)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		if err := classifyHTTPStatus(resp); err != nil {
			return false, err
		}
		return true, nil
	})
	return err
}

func (g *Gateway) applyAuth(req *http.Request) {
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
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
