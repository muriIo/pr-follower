package httpadapter_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

func (ts testServer) userID(t *testing.T, session *http.Cookie) uint64 {
	t.Helper()
	var me struct {
		User struct {
			ID uint64 `json:"id"`
		} `json:"user"`
	}
	rec := ts.do(t, http.MethodGet, "/api/v1/auth/me", "", session)
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil || me.User.ID == 0 {
		t.Fatalf("auth/me: %s", rec.Body.String())
	}
	return me.User.ID
}

func TestNotifications_ListReadAndSettingsForTheSignedInUserOnly(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	session := ts.signIn(t)
	me := ts.userID(t, session)
	ctx := context.Background()
	at := time.Date(2026, 11, 23, 9, 0, 0, 0, time.UTC)
	created, err := ts.notes.CreateNotifications(ctx, []entity.Notification{
		{UserID: me, Kind: entity.NotificationKindTurn, Reason: entity.NotificationReasonReviewRequested, RepositoryID: 1, PullRequestID: 7, PRNumber: 35, PRTitle: "Fix login", CommitSHA: "s1", CreatedAt: at},
		{UserID: me, Kind: entity.NotificationKindRisk, Reason: entity.NotificationReasonHighRisk, RepositoryID: 1, PullRequestID: 8, PRNumber: 36, CommitSHA: "d1", CreatedAt: at},
		{UserID: me + 100, Kind: entity.NotificationKindTurn, Reason: entity.NotificationReasonReviewRequested, RepositoryID: 1, PullRequestID: 7, PRNumber: 35, CommitSHA: "s1", CreatedAt: at},
	})
	if err != nil || len(created) != 3 {
		t.Fatalf("seed: %v %v", created, err)
	}

	var page struct {
		Notifications []entity.Notification `json:"notifications"`
		Unread        int                   `json:"unread"`
		NextBefore    uint64                `json:"next_before"`
	}
	rec := ts.do(t, http.MethodGet, "/api/v1/notifications?limit=1", "", session)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &page) != nil {
		t.Fatalf("GET notifications: %d %s", rec.Code, rec.Body.String())
	}
	if len(page.Notifications) != 1 || page.Notifications[0].ID != created[1].ID || page.Unread != 2 || page.NextBefore != created[1].ID {
		t.Fatalf("newest first, my unread count, a cursor: %s", rec.Body.String())
	}
	rec = ts.do(t, http.MethodGet, "/api/v1/notifications?before="+strconv.FormatUint(page.NextBefore, 10), "", session)
	page.NextBefore = 0
	_ = json.Unmarshal(rec.Body.Bytes(), &page)
	if len(page.Notifications) != 1 || page.Notifications[0].PRTitle != "Fix login" || page.NextBefore != 0 {
		t.Fatalf("another user's notification must never show: %s", rec.Body.String())
	}

	if rec := ts.do(t, http.MethodPost, "/api/v1/notifications/read", `{"ids":[`+strconv.FormatUint(created[0].ID, 10)+`,`+strconv.FormatUint(created[2].ID, 10)+`]}`, session); rec.Code != http.StatusOK {
		t.Fatalf("read: %d %s", rec.Code, rec.Body.String())
	}
	if n, _ := ts.notes.CountUnreadNotifications(ctx, me+100); n != 1 {
		t.Fatal("marking read never touches another user's notifications")
	}
	if rec := ts.do(t, http.MethodPost, "/api/v1/notifications/read", `{"all":true}`, session); rec.Code != http.StatusOK {
		t.Fatalf("read all: %d", rec.Code)
	}
	if n, _ := ts.notes.CountUnreadNotifications(ctx, me); n != 0 {
		t.Fatalf("unread after read all = %d", n)
	}
	for _, body := range []string{`{}`, `not json`, `{"ids":[1],"all":true}`} {
		if rec := ts.do(t, http.MethodPost, "/api/v1/notifications/read", body, session); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d", body, rec.Code)
		}
	}

	rec = ts.do(t, http.MethodPut, "/api/v1/notifications/settings", `{"paused":true,"popups_off":["risk"]}`, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT settings: %d %s", rec.Code, rec.Body.String())
	}
	var settings struct {
		Settings entity.NotificationSettings `json:"settings"`
		Channels []struct {
			Name string `json:"name"`
		} `json:"channels"`
		Kinds []string `json:"kinds"`
	}
	rec = ts.do(t, http.MethodGet, "/api/v1/notifications/settings", "", session)
	if json.Unmarshal(rec.Body.Bytes(), &settings) != nil || !settings.Settings.Paused || len(settings.Settings.PopupsOff) != 1 ||
		len(settings.Channels) != 1 || settings.Channels[0].Name != "panel" || len(settings.Kinds) != 3 {
		t.Fatalf("GET settings: %s", rec.Body.String())
	}
	if rec := ts.do(t, http.MethodPut, "/api/v1/notifications/settings", `{"popups_off":["email"]}`, session); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown kind: want 400, got %d", rec.Code)
	}
}

func TestNotifications_RequireASession(t *testing.T) {
	t.Parallel()
	ts := setupTestRouter(t)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/notifications"},
		{http.MethodPost, "/api/v1/notifications/read"},
		{http.MethodGet, "/api/v1/notifications/settings"},
		{http.MethodPut, "/api/v1/notifications/settings"},
	} {
		if rec := ts.do(t, route.method, route.path, "", nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d", route.method, route.path, rec.Code)
		}
	}
}
