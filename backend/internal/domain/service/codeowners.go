package service

import (
	"path"
	"strings"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
)

type CodeownerEvaluation struct {
	OwnedFiles []entity.ChangedFile
	CoOwners   []string
}

func (e CodeownerEvaluation) HasCoOwner(owner string) bool {
	norm := normalizeHandle(owner)
	for _, co := range e.CoOwners {
		if normalizeHandle(co) == norm {
			return true
		}
	}
	return false
}

func (e CodeownerEvaluation) IsTeammateOwner(owner string) bool {
	return e.HasCoOwner(owner)
}

// CodeownersPaths is where GitHub looks for a CODEOWNERS file, in its order of precedence: the
// first one that exists is the one in force.
var CodeownersPaths = []string{".github/CODEOWNERS", "CODEOWNERS", "docs/CODEOWNERS"}

// IsCodeownersPath reports whether a repository path is one of the CODEOWNERS locations.
func IsCodeownersPath(path string) bool {
	path = strings.TrimPrefix(path, "/")
	for _, candidate := range CodeownersPaths {
		if path == candidate {
			return true
		}
	}
	return false
}

func ParseCodeowners(raw string) []entity.CodeownerRule {
	var rules []entity.CodeownerRule
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if idx := strings.Index(trimmed, "#"); idx != -1 {
			trimmed = strings.TrimSpace(trimmed[:idx])
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		rules = append(rules, entity.CodeownerRule{
			Pattern: fields[0],
			Owners:  fields[1:],
		})
	}
	return rules
}

func EvaluateCodeownersForUser(
	rules []entity.CodeownerRule,
	changedFiles []entity.ChangedFile,
	user entity.User,
) CodeownerEvaluation {
	userHandles := make(map[string]struct{}, len(user.Teams)+1)
	if user.Login != "" {
		userHandles[normalizeHandle(user.Login)] = struct{}{}
	}
	for _, team := range user.Teams {
		userHandles[normalizeHandle(team)] = struct{}{}
	}

	var ownedFiles []entity.ChangedFile
	coOwnerSet := make(map[string]struct{})

	for _, file := range changedFiles {
		var winningOwners []string
		for _, rule := range rules {
			if matchCodeownerPattern(rule.Pattern, file.Path) {
				winningOwners = rule.Owners
			}
		}
		if len(winningOwners) == 0 {
			continue
		}

		userOwnsFile := false
		for _, owner := range winningOwners {
			if _, ok := userHandles[normalizeHandle(owner)]; ok {
				userOwnsFile = true
				break
			}
		}

		if userOwnsFile {
			ownedFiles = append(ownedFiles, file)
			for _, owner := range winningOwners {
				coOwnerSet[owner] = struct{}{}
			}
		}
	}

	coOwners := make([]string, 0, len(coOwnerSet))
	for owner := range coOwnerSet {
		coOwners = append(coOwners, owner)
	}

	return CodeownerEvaluation{
		OwnedFiles: ownedFiles,
		CoOwners:   coOwners,
	}
}

// matchCodeownerPattern applies GitHub's CODEOWNERS pattern rules, which follow gitignore:
//   - a leading slash, or a slash in the middle, anchors the pattern at the repository root;
//     otherwise it matches at any depth ("*.js", "apps/", "Makefile");
//   - a trailing slash matches a directory and everything under it, never a file of that name;
//   - a pattern that matches a directory owns everything under it, except when it ends in "/*",
//     which owns only the files directly in that directory;
//   - "**" matches any number of directories.
func matchCodeownerPattern(pattern, filePath string) bool {
	dirOnly := strings.HasSuffix(pattern, "/")
	trimmed := strings.TrimSuffix(pattern, "/")
	anchored := strings.Contains(trimmed, "/")
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "" {
		return false
	}
	patternSegs := strings.Split(trimmed, "/")
	pathSegs := strings.Split(strings.TrimPrefix(filePath, "/"), "/")
	directFilesOnly := patternSegs[len(patternSegs)-1] == "*"

	lastStart := 0
	if !anchored {
		lastStart = len(pathSegs) - 1
	}
	for start := 0; start <= lastStart; start++ {
		rest := pathSegs[start:]
		for n := 1; n <= len(rest); n++ {
			if !matchPathSegments(patternSegs, rest[:n]) {
				continue
			}
			if n == len(rest) { // the pattern matched the file itself
				if !dirOnly {
					return true
				}
				continue
			}
			if !directFilesOnly { // the pattern matched a directory above the file
				return true
			}
		}
	}
	return false
}

// matchPathSegments matches whole path segments; "**" stands for zero or more of them.
func matchPathSegments(pattern, segs []string) bool {
	if len(pattern) == 0 {
		return len(segs) == 0
	}
	if pattern[0] == "**" {
		for skip := 0; skip <= len(segs); skip++ {
			if matchPathSegments(pattern[1:], segs[skip:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if ok, _ := path.Match(pattern[0], segs[0]); !ok {
		return false
	}
	return matchPathSegments(pattern[1:], segs[1:])
}

func normalizeHandle(handle string) string {
	h := strings.TrimSpace(strings.ToLower(handle))
	if !strings.HasPrefix(h, "@") {
		return "@" + h
	}
	return h
}
