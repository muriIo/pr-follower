package service

import "github.com/murilo/pr-follower/backend/internal/domain/entity"

type TurnStateInput struct {
	PR                        entity.PullRequest
	OwnedFiles                []entity.ChangedFile
	ExistingState             entity.UserPRState
	ApprovedByMeSHA           string
	TeammateApprovedAtSHA     string
	UserActedAtSHA            string
	AuthorRepliedAfterUserAct bool
}

func ComputeTurnState(in TurnStateInput) entity.TurnState {
	if in.PR.State == entity.PRStateMerged || in.PR.State == entity.PRStateClosed {
		return entity.TurnStateMergedOrClosed
	}

	if in.ExistingState.IsSnoozedFor(in.PR.HeadSHA, in.PR.CIStatus) {
		return entity.TurnStateSnoozed
	}

	if !in.PR.IsReadyForReviewGate() || len(in.OwnedFiles) == 0 {
		return entity.TurnStateHiddenGate
	}

	if in.ApprovedByMeSHA != "" && in.ApprovedByMeSHA == in.PR.HeadSHA {
		return entity.TurnStateApprovedByMe
	}

	if in.TeammateApprovedAtSHA != "" && in.TeammateApprovedAtSHA == in.PR.HeadSHA {
		return entity.TurnStateTeammateApproved
	}

	if in.UserActedAtSHA != "" && in.UserActedAtSHA == in.PR.HeadSHA && !in.AuthorRepliedAfterUserAct {
		return entity.TurnStateWaitingOnAuthor
	}

	return entity.TurnStateWaitingOnMe
}
