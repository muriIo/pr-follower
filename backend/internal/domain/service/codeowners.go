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

func matchCodeownerPattern(pattern, filePath string) bool {
	cleanPath := strings.TrimPrefix(filePath, "/")
	if pattern == "*" {
		return true
	}

	if strings.HasSuffix(pattern, "/") {
		dirPrefix := strings.TrimPrefix(pattern, "/")
		return strings.HasPrefix(cleanPath, dirPrefix) || strings.Contains("/"+cleanPath, "/"+dirPrefix)
	}

	if strings.HasPrefix(pattern, "*.") {
		matched, _ := path.Match(pattern, path.Base(cleanPath))
		return matched
	}

	cleanPattern := strings.TrimPrefix(pattern, "/")
	if matched, _ := path.Match(cleanPattern, cleanPath); matched {
		return true
	}
	return cleanPath == cleanPattern || strings.HasPrefix(cleanPath, cleanPattern+"/")
}

func normalizeHandle(handle string) string {
	h := strings.TrimSpace(strings.ToLower(handle))
	if !strings.HasPrefix(h, "@") {
		return "@" + h
	}
	return h
}
