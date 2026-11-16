package httpadapter

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

// ADR-0010: GitHub-only sign-in with server-side sessions.
const (
	sessionCookieName = "pf_session"
	oauthCookieName   = "pf_oauth"
	oauthCookiePath   = "/api/v1/auth/github"
	oauthFlowTTL      = 10 * time.Minute
)

type ctxKey int

const userCtxKey ctxKey = iota

// AuthGitHubLogin starts the GitHub web application flow (state + PKCE).
func (h *Handler) AuthGitHubLogin(w http.ResponseWriter, r *http.Request) {
	if h.deps.AuthUC == nil {
		redirectWithAuthError(w, r, "not_configured")
		return
	}
	challenge, err := h.deps.AuthUC.BeginLogin(r.Context())
	if err != nil {
		redirectWithAuthError(w, r, "login_failed")
		return
	}
	// Secure follows PUBLIC_URL: on for https, off only for local http development.
	http.SetCookie(w, &http.Cookie{ // nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
		Name:     oauthCookieName,
		Value:    challenge.State + "." + challenge.CodeVerifier,
		Path:     oauthCookiePath,
		MaxAge:   int(oauthFlowTTL.Seconds()),
		HttpOnly: true,
		Secure:   h.deps.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, challenge.AuthorizeURL, http.StatusFound)
}

// AuthGitHubCallback finishes the flow: verifies state, exchanges the code and opens a session.
func (h *Handler) AuthGitHubCallback(w http.ResponseWriter, r *http.Request) {
	h.clearCookie(w, oauthCookieName, oauthCookiePath)
	if h.deps.AuthUC == nil {
		redirectWithAuthError(w, r, "not_configured")
		return
	}

	query := r.URL.Query()
	state, verifier, ok := readOAuthFlowCookie(r)
	if !ok || subtle.ConstantTimeCompare([]byte(state), []byte(query.Get("state"))) != 1 {
		redirectWithAuthError(w, r, "state_mismatch")
		return
	}
	if ghErr := query.Get("error"); ghErr != "" {
		redirectWithAuthError(w, r, "access_denied")
		return
	}

	result, err := h.deps.AuthUC.CompleteLogin(r.Context(), query.Get("code"), verifier)
	if err != nil {
		redirectWithAuthError(w, r, "login_failed")
		return
	}
	// Secure follows PUBLIC_URL: on for https, off only for local http development.
	http.SetCookie(w, &http.Cookie{ // nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
		Name:     sessionCookieName,
		Value:    result.SessionToken,
		Path:     "/",
		Expires:  result.ExpiresAt,
		MaxAge:   int(time.Until(result.ExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   h.deps.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// AuthMe returns the signed-in user. GitHub tokens are excluded by the entity's JSON tags.
func (h *Handler) AuthMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": currentUser(r)})
}

// AuthLogout ends the session (if any) and expires the cookie.
func (h *Handler) AuthLogout(w http.ResponseWriter, r *http.Request) {
	if h.deps.AuthUC != nil {
		if c, err := r.Cookie(sessionCookieName); err == nil {
			_ = h.deps.AuthUC.Logout(r.Context(), c.Value)
		}
	}
	h.clearCookie(w, sessionCookieName, "/")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// RequireSession rejects requests without a valid session and puts the user in the context.
func (h *Handler) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.deps.AuthUC == nil {
			writeDomainError(w, entity.ErrUnauthorized)
			return
		}
		c, err := r.Cookie(sessionCookieName)
		if err != nil {
			writeDomainError(w, entity.ErrUnauthorized)
			return
		}
		user, err := h.deps.AuthUC.Authenticate(r.Context(), c.Value)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userCtxKey, user)))
	})
}

// currentUser is only valid behind RequireSession.
func currentUser(r *http.Request) *entity.User {
	user, _ := r.Context().Value(userCtxKey).(*entity.User)
	return user
}

func currentUserID(r *http.Request) uint64 {
	if user := currentUser(r); user != nil {
		return user.ID
	}
	return 0
}

func readOAuthFlowCookie(r *http.Request) (state, verifier string, ok bool) {
	c, err := r.Cookie(oauthCookieName)
	if err != nil {
		return "", "", false
	}
	state, verifier, ok = strings.Cut(c.Value, ".")
	return state, verifier, ok && state != "" && verifier != ""
}

func (h *Handler) clearCookie(w http.ResponseWriter, name, path string) {
	// Secure follows PUBLIC_URL: on for https, off only for local http development.
	http.SetCookie(w, &http.Cookie{ // nosemgrep: go.lang.security.audit.net.cookie-missing-secure.cookie-missing-secure
		Name:     name,
		Value:    "",
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.deps.SecureCookies,
		SameSite: http.SameSiteLaxMode,
	})
}

func redirectWithAuthError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/?auth_error="+code, http.StatusFound)
}
