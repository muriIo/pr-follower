package webhook_test

import (
	"context"
	"errors"
	"testing"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/usecase/webhook"
)

func statusEvent(id int64, state string) webhook.StatusEventPayload {
	return webhook.StatusEventPayload{
		StatusID: id, GitHubRepoID: 9001, SHA: "sha-1", Context: "Vercel", State: state,
		Description: "Vercel is deploying your app", TargetURL: "https://vercel.com/deploy/1",
	}
}

func TestIngestStatusEvent_ACommitStatusCountsLikeAnyOtherCheck(t *testing.T) {
	t.Parallel()
	uc, checks, pub := checkRunSetup()
	ctx := context.Background()

	// The passing check run alone would make the PR green...
	if err := uc.IngestCheckRunEvent(ctx, run(1, "completed", "success")); err != nil {
		t.Fatal(err)
	}
	if got := ciOf(checks); got != entity.CIStatusSuccess {
		t.Fatalf("only a passing check run so far: %q", got)
	}
	// ...until Vercel reports a pending status for the same commit: it is not green any more.
	if err := uc.IngestStatusEvent(ctx, statusEvent(100, "pending")); err != nil {
		t.Fatal(err)
	}
	if got := ciOf(checks); got != entity.CIStatusPending {
		t.Fatalf("a pending status holds CI back: %q", got)
	}
	stored := checks.statuses[100]
	if stored.Context != "Vercel" || stored.Description != "Vercel is deploying your app" || stored.TargetURL != "https://vercel.com/deploy/1" || stored.RepositoryID != 10 {
		t.Fatalf("unexpected stored status: %+v", stored)
	}
	// The deployment finishes: a new status (new id), same context.
	if err := uc.IngestStatusEvent(ctx, statusEvent(101, "success")); err != nil {
		t.Fatal(err)
	}
	if got := ciOf(checks); got != entity.CIStatusSuccess {
		t.Fatalf("the latest status of the context decides: %q", got)
	}
	// A failure fails it.
	failed := statusEvent(102, "failure")
	failed.Context = "jenkins/pr-validate-api-iam"
	if err := uc.IngestStatusEvent(ctx, failed); err != nil {
		t.Fatal(err)
	}
	if got := ciOf(checks); got != entity.CIStatusFailure {
		t.Fatalf("a failing status fails CI: %q", got)
	}
	types := []string{}
	for _, e := range pub.events {
		types = append(types, e.Type)
	}
	if len(types) == 0 || types[len(types)-1] != "ci.failed" {
		t.Fatalf("the change is announced: %v", types)
	}
}

func TestIngestStatusEvent_RedeliverChangesNothingAndAnnouncesNothing(t *testing.T) {
	t.Parallel()
	uc, checks, pub := checkRunSetup()
	ctx := context.Background()

	for range 3 {
		if err := uc.IngestStatusEvent(ctx, statusEvent(100, "pending")); err != nil {
			t.Fatal(err)
		}
	}
	if checks.statusWrites != 1 || len(pub.events) != 1 {
		t.Fatalf("a redelivered status converges: %d writes, %d events", checks.statusWrites, len(pub.events))
	}
}

func TestIngestStatusEvent_IgnoresWhatDoesNotConcernAFollowedPR(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// A repository nobody follows.
	unknown := webhook.NewIngestUseCase(&fakeRepoRepo{err: entity.ErrNotFound}, &fakePRRepo{}, newFakeChecks(), &fakePub{}, nil)
	if err := unknown.IngestStatusEvent(ctx, statusEvent(1, "pending")); err != nil {
		t.Fatalf("not a followed repository: %v", err)
	}

	// A status of a commit no open PR has as head is kept (the PR may get that commit) but moves no PR.
	uc, checks, pub := checkRunSetup()
	other := statusEvent(5, "failure")
	other.SHA = "sha-elsewhere"
	if err := uc.IngestStatusEvent(ctx, other); err != nil {
		t.Fatal(err)
	}
	if got := ciOf(checks); got != entity.CIStatusNone || len(pub.events) != 0 {
		t.Fatalf("another commit's status must not touch this PR: %q %v", got, pub.events)
	}
}

func TestIngestStatusEvent_PropagatesFailuresAndCancellation(t *testing.T) {
	t.Parallel()
	boom := errors.New("db down")
	uc, checks, _ := checkRunSetup()
	checks.upsertErr = boom
	if err := uc.IngestStatusEvent(context.Background(), statusEvent(1, "pending")); !errors.Is(err, boom) {
		t.Fatalf("expected the store error, got %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	fresh, _, _ := checkRunSetup()
	if err := fresh.IngestStatusEvent(canceled, statusEvent(2, "pending")); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
