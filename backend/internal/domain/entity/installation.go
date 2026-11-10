package entity

// Installation is a GitHub App installation the signed-in user can access (ADR-0010):
// one per user or organization account where the app is installed.
type Installation struct {
	ID                  int64  `json:"id"`
	AccountLogin        string `json:"account_login"`
	AccountType         string `json:"account_type"` // "User" or "Organization"
	AccountAvatarURL    string `json:"account_avatar_url"`
	RepositorySelection string `json:"repository_selection"` // "all" or "selected"
	SettingsURL         string `json:"settings_url"`         // where the user adjusts the installation
}

// InstallationRepository is a repository reachable through an installation by the user.
type InstallationRepository struct {
	GitHubRepoID   int64  `json:"github_repo_id"`
	InstallationID int64  `json:"installation_id"`
	Owner          string `json:"owner"`
	Name           string `json:"name"`
	FullName       string `json:"full_name"`
	DefaultBranch  string `json:"default_branch"`
	Private        bool   `json:"private"`
}

// ToRepository maps an importable repository to the persisted entity, keeping any
// CODEOWNERS rules already cached for it.
func (r InstallationRepository) ToRepository(cachedRules []CodeownerRule) *Repository {
	branch := r.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	return &Repository{
		GitHubRepoID:          r.GitHubRepoID,
		InstallationID:        r.InstallationID,
		Owner:                 r.Owner,
		Name:                  r.Name,
		FullName:              r.FullName,
		DefaultBranch:         branch,
		CachedCodeownersRules: cachedRules,
	}
}
