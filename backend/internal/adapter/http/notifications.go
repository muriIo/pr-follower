package httpadapter

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/notify"
)

// center answers 503 when the server runs without the notification center.
func (h *Handler) center(w http.ResponseWriter) *notify.Center {
	if h.deps.Notifications == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "notifications are not available", "code": "unavailable"})
	}
	return h.deps.Notifications
}

// ListNotifications serves a page of the signed-in user's notifications, newest first
// (GET /notifications?before=<id>&limit=<n>).
func (h *Handler) ListNotifications(w http.ResponseWriter, r *http.Request) {
	center := h.center(w)
	if center == nil {
		return
	}
	before, _ := strconv.ParseUint(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := center.List(r.Context(), currentUserID(r), before, limit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// MarkNotificationsRead marks the given notifications, or all of them, read
// (POST /notifications/read with {"ids": [...]} or {"all": true}).
func (h *Handler) MarkNotificationsRead(w http.ResponseWriter, r *http.Request) {
	center := h.center(w)
	if center == nil {
		return
	}
	var req struct {
		IDs []uint64 `json:"ids"`
		All bool     `json:"all"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDomainError(w, entity.ErrInvalidInput)
		return
	}
	if err := center.MarkRead(r.Context(), currentUserID(r), req.IDs, req.All); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GetNotificationSettings returns the user's settings with what this server offers: its delivery
// channels and the kinds that can be switched.
func (h *Handler) GetNotificationSettings(w http.ResponseWriter, r *http.Request) {
	center := h.center(w)
	if center == nil {
		return
	}
	settings, err := center.Settings(r.Context(), currentUserID(r))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	h.writeSettings(w, r, center, settings)
}

// SaveNotificationSettings replaces the user's settings (PUT /notifications/settings).
func (h *Handler) SaveNotificationSettings(w http.ResponseWriter, r *http.Request) {
	center := h.center(w)
	if center == nil {
		return
	}
	var req entity.NotificationSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeDomainError(w, entity.ErrInvalidInput)
		return
	}
	settings, err := center.SaveSettings(r.Context(), currentUserID(r), req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	h.writeSettings(w, r, center, settings)
}

func (h *Handler) writeSettings(w http.ResponseWriter, r *http.Request, center *notify.Center, settings entity.NotificationSettings) {
	opts, err := center.Options(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings, "channels": opts.Channels, "kinds": opts.Kinds})
}
