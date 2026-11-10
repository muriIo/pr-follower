package httpadapter

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/prfiles"
)

// ListPullRequestFiles serves the drawer's changed-files preview for a PR of a followed
// repository, read from GitHub with the user's token and flagged by CODEOWNERS ownership.
func (h *Handler) ListPullRequestFiles(w http.ResponseWriter, r *http.Request) {
	if h.deps.FilesUC == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "github_files_not_configured"})
		return
	}
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil || number <= 0 {
		writeDomainError(w, entity.ErrInvalidInput)
		return
	}
	ctx, release := h.beginOperation(r)
	defer release()

	fullName := chi.URLParam(r, "owner") + "/" + chi.URLParam(r, "name")
	files, err := h.deps.FilesUC.List(ctx, *currentUser(r), fullName, number)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if files == nil {
		files = []prfiles.File{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}
