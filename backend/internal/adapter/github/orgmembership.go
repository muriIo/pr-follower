package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/infrastructure/resilience"
)

// IsOrgMember implements port.OrgMembershipChecker (ADR-0015). It asks as the GitHub App installed on
// the organization, so private (concealed) members count and the user's own token scopes do not
// matter: GET /orgs/{org}/installation (app JWT), then GET /orgs/{org}/members/{login} with that
// installation's token, which needs the organization "Members: read" permission.
//
// 204 is a member. 404 is "not a member" only after GET /user/{id} confirms login still names that
// account; a renamed account is checked again under its current login. Anything else is an error:
// the app not installed there (ErrNotFound), the permission missing or a 302 (the requester cannot
// see the organization's members: ErrForbidden), a limit, or GitHub unavailable.
func (a *AppAuth) IsOrgMember(ctx context.Context, org, login string, githubID int64) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, context.Cause(ctx)
	}
	org, login = strings.TrimSpace(org), strings.TrimSpace(login)
	if org == "" || login == "" {
		return false, fmt.Errorf("%w: an organization and a login are required", entity.ErrInvalidInput)
	}
	installationID, err := a.orgInstallation(ctx, org)
	if err != nil {
		return false, err
	}
	token, err := a.InstallationToken(ctx, installationID)
	if err != nil {
		return false, err
	}
	member, err := a.checkMember(ctx, token, org, login)
	if err != nil || member {
		return member, err
	}
	current, err := a.accountLogin(ctx, token, githubID)
	if err != nil {
		return false, err
	}
	if strings.EqualFold(current, login) {
		return false, nil
	}
	return a.checkMember(ctx, token, org, current)
}

// orgInstallation finds the app's installation on the organization and makes sure it may read members.
func (a *AppAuth) orgInstallation(ctx context.Context, org string) (int64, error) {
	jwt, err := a.signJWT()
	if err != nil {
		return 0, err
	}
	endpoint := fmt.Sprintf("%s/orgs/%s/installation", a.baseURL, url.PathEscape(org))
	type installation struct {
		ID          int64             `json:"id"`
		Permissions map[string]string `json:"permissions"`
	}
	found, err := resilience.Retry(ctx, a.backoff, func(reqCtx context.Context) (installation, error) {
		resp, err := a.get(reqCtx, a.client, endpoint, jwt)
		if err != nil {
			return installation{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			return installation{}, resilience.Permanent(fmt.Errorf("%w: the github app is not installed on the %s organization", entity.ErrNotFound, org))
		}
		if resp.StatusCode == http.StatusUnauthorized {
			return installation{}, resilience.Permanent(fmt.Errorf("%w: github rejected the app credentials", entity.ErrUnauthorized))
		}
		if err := classifyHTTPStatus(resp); err != nil {
			return installation{}, err
		}
		var body installation
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil || body.ID == 0 {
			return installation{}, resilience.Permanent(fmt.Errorf("%w: github returned an unreadable installation", entity.ErrGitHubUnavailable))
		}
		return body, nil
	})
	if err != nil {
		return 0, err
	}
	if p := found.Permissions["members"]; p != "read" && p != "write" {
		return 0, fmt.Errorf("%w: the github app installation on %s lacks the organization Members (read) permission; an owner of %s must approve it", entity.ErrForbidden, org, org)
	}
	return found.ID, nil
}

// checkMember is GET /orgs/{org}/members/{login}: 204 a member, 404 not one. Redirects are not followed:
// a 302 means the requester cannot see the organization's members, which is not an answer.
func (a *AppAuth) checkMember(ctx context.Context, token, org, login string) (bool, error) {
	endpoint := fmt.Sprintf("%s/orgs/%s/members/%s", a.baseURL, url.PathEscape(org), url.PathEscape(login))
	noRedirect := *a.client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return resilience.Retry(ctx, a.backoff, func(reqCtx context.Context) (bool, error) {
		resp, err := a.get(reqCtx, &noRedirect, endpoint, token)
		if err != nil {
			return false, err
		}
		defer resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusNoContent:
			return true, nil
		case http.StatusNotFound:
			return false, nil
		case http.StatusFound:
			return false, resilience.Permanent(fmt.Errorf("%w: the github app cannot see the members of %s", entity.ErrForbidden, org))
		}
		if err := classifyHTTPStatus(resp); err != nil {
			return false, err
		}
		return false, resilience.Permanent(fmt.Errorf("%w: unexpected membership answer (%d)", entity.ErrGitHubUnavailable, resp.StatusCode))
	})
}

// accountLogin is the current login of a GitHub account (GET /user/{account_id}).
func (a *AppAuth) accountLogin(ctx context.Context, token string, githubID int64) (string, error) {
	if githubID <= 0 {
		return "", fmt.Errorf("%w: a github account id is required", entity.ErrInvalidInput)
	}
	endpoint := fmt.Sprintf("%s/user/%d", a.baseURL, githubID)
	return resilience.Retry(ctx, a.backoff, func(reqCtx context.Context) (string, error) {
		resp, err := a.get(reqCtx, a.client, endpoint, token)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if err := classifyHTTPStatus(resp); err != nil {
			return "", err
		}
		var body struct {
			Login string `json:"login"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil || body.Login == "" {
			return "", resilience.Permanent(fmt.Errorf("%w: github returned an unreadable account", entity.ErrGitHubUnavailable))
		}
		return body.Login, nil
	})
}

func (a *AppAuth) get(ctx context.Context, client *http.Client, endpoint, bearer string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, resilience.Permanent(err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	setBearer(req, bearer)
	resp, err := client.Do(req)
	if err != nil {
		return nil, noAnswer(ctx, err)
	}
	return resp, nil
}
