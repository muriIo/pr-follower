// Package prfiles lists a PR's changed files for the drawer preview, flagging the ones the
// signed-in user owns through CODEOWNERS. Files come from GitHub with the user's own token.
package prfiles

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/domain/service"
)

// maxCachedPRs bounds the in-memory cache of GitHub file lists.
const maxCachedPRs = 256

// File is a changed file and whether the user owns it.
type File struct {
	entity.ChangedFile
	Owned bool `json:"owned"`
}

// replica-local: a cache of changed files per head commit; a miss on another replica reads GitHub once more.
type UseCase struct {
	tokens  port.UserTokenSource
	github  port.GitHubUserAPI
	follows port.FollowedRepositories
	heads   port.PullRequestHeads

	mu       sync.Mutex
	cache    map[string][]entity.ChangedFile
	inflight map[string]*call
}

type call struct {
	done  chan struct{}
	files []entity.ChangedFile
	err   error
}

func NewUseCase(tokens port.UserTokenSource, github port.GitHubUserAPI, follows port.FollowedRepositories, heads port.PullRequestHeads) *UseCase {
	return &UseCase{
		tokens:   tokens,
		github:   github,
		follows:  follows,
		heads:    heads,
		cache:    map[string][]entity.ChangedFile{},
		inflight: map[string]*call{},
	}
}

// List returns the files changed by PR #number of a repository the user follows, owned files
// first. GitHub's answer is cached per head commit and concurrent requests share one call
// (ADR-0006), so reopening a drawer does not hit GitHub again until a new commit arrives.
func (uc *UseCase) List(ctx context.Context, user entity.User, fullName string, number int) ([]File, error) {
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	if number <= 0 {
		return nil, entity.ErrInvalidInput
	}
	followed, err := uc.follows.ListFollowedByUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	var repo *entity.Repository
	for i := range followed {
		if strings.EqualFold(followed[i].FullName, fullName) {
			repo = &followed[i]
			break
		}
	}
	if repo == nil {
		return nil, entity.ErrNotFound
	}

	key := ""
	if head, ok := uc.heads.HeadSHA(ctx, repo.ID, number); ok && head != "" {
		key = fmt.Sprintf("%d/%d/%s", repo.ID, number, head)
	}
	files, err := uc.fetch(ctx, key, user.ID, *repo, number)
	if err != nil {
		return nil, err
	}
	return markOwned(files, repo.CachedCodeownersRules, user), nil
}

// fetch returns cached files for key, joins an in-flight call for it, or calls GitHub. An empty
// key (head commit unknown) is never cached or shared.
func (uc *UseCase) fetch(ctx context.Context, key string, userID uint64, repo entity.Repository, number int) ([]entity.ChangedFile, error) {
	if key == "" {
		return uc.callGitHub(ctx, userID, repo, number)
	}
	uc.mu.Lock()
	if files, ok := uc.cache[key]; ok {
		uc.mu.Unlock()
		return files, nil
	}
	if c, ok := uc.inflight[key]; ok {
		uc.mu.Unlock()
		select {
		case <-c.done:
			return c.files, c.err
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	c := &call{done: make(chan struct{})}
	uc.inflight[key] = c
	uc.mu.Unlock()

	c.files, c.err = uc.callGitHub(ctx, userID, repo, number)

	uc.mu.Lock()
	delete(uc.inflight, key)
	if c.err == nil {
		if len(uc.cache) >= maxCachedPRs {
			uc.cache = map[string][]entity.ChangedFile{}
		}
		uc.cache[key] = c.files
	}
	uc.mu.Unlock()
	close(c.done)
	return c.files, c.err
}

func (uc *UseCase) callGitHub(ctx context.Context, userID uint64, repo entity.Repository, number int) ([]entity.ChangedFile, error) {
	token, err := uc.tokens.AccessToken(ctx, userID)
	if err != nil {
		return nil, err
	}
	return uc.github.ListPullRequestFiles(ctx, token, repo.Owner, repo.Name, number)
}

func markOwned(files []entity.ChangedFile, rules []entity.CodeownerRule, user entity.User) []File {
	owned := map[string]bool{}
	for _, f := range service.EvaluateCodeownersForUser(rules, files, user).OwnedFiles {
		owned[f.Path] = true
	}
	out := make([]File, len(files))
	for i, f := range files {
		out[i] = File{ChangedFile: f, Owned: owned[f.Path]}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Owned && !out[j].Owned })
	return out
}
