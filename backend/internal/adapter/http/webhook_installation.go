package httpadapter

import (
	"context"
	"encoding/json"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

// ingestInstallationWebhook maps GitHub's `installation` and `installation_repositories`
// payloads (ADR-0010). Signature verification happens before this is called.
func (h *Handler) ingestInstallationWebhook(ctx context.Context, eventType string, payload []byte) error {
	if h.deps.InstallationUC == nil {
		return nil
	}
	type repoRef struct {
		ID int64 `json:"id"`
	}
	ids := func(refs []repoRef) []int64 {
		out := make([]int64, 0, len(refs))
		for _, r := range refs {
			out = append(out, r.ID)
		}
		return out
	}

	switch eventType {
	case "installation":
		var raw struct {
			Action       string `json:"action"`
			Installation struct {
				ID      int64 `json:"id"`
				Account struct {
					Login string `json:"login"`
				} `json:"account"`
			} `json:"installation"`
			Repositories []repoRef `json:"repositories"`
		}
		if err := json.Unmarshal(payload, &raw); err != nil {
			return entity.ErrInvalidInput
		}
		return h.deps.InstallationUC.IngestInstallationEvent(ctx, webhook.InstallationEventPayload{
			Action:         raw.Action,
			InstallationID: raw.Installation.ID,
			AccountLogin:   raw.Installation.Account.Login,
			RepositoryIDs:  ids(raw.Repositories),
		})
	case "installation_repositories":
		var raw struct {
			Action       string `json:"action"`
			Installation struct {
				ID int64 `json:"id"`
			} `json:"installation"`
			Added   []repoRef `json:"repositories_added"`
			Removed []repoRef `json:"repositories_removed"`
		}
		if err := json.Unmarshal(payload, &raw); err != nil {
			return entity.ErrInvalidInput
		}
		return h.deps.InstallationUC.IngestInstallationRepositoriesEvent(ctx, webhook.InstallationRepositoriesEventPayload{
			Action:         raw.Action,
			InstallationID: raw.Installation.ID,
			AddedIDs:       ids(raw.Added),
			RemovedIDs:     ids(raw.Removed),
		})
	}
	return nil
}
