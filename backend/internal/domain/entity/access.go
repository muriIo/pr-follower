package entity

// RepositoryAccess tracks whether the GitHub App can still reach a followed repository
// (ADR-0010). Installation webhooks move it between states.
type RepositoryAccess string

const (
	RepositoryAccessActive    RepositoryAccess = "active"
	RepositoryAccessRemoved   RepositoryAccess = "removed"   // app uninstalled or repository deselected
	RepositoryAccessSuspended RepositoryAccess = "suspended" // installation suspended by the account owner
)

// HasAccess reports whether the app can reach the repository. Empty means active (rows
// created before access was tracked).
func (r Repository) HasAccess() bool {
	return r.Access == "" || r.Access == RepositoryAccessActive
}

// ApplyAccess moves the repository to access and, when installationID > 0, to that
// installation. It reports whether anything changed.
func (r *Repository) ApplyAccess(access RepositoryAccess, installationID int64) bool {
	current := r.Access
	if current == "" {
		current = RepositoryAccessActive
	}
	changed := current != access
	r.Access = access
	if installationID > 0 && installationID != r.InstallationID {
		r.InstallationID = installationID
		changed = true
	}
	return changed
}
