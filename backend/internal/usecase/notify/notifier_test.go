package notify_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/murilo/pr-follower/backend/internal/domain/entity"
	"github.com/murilo/pr-follower/backend/internal/domain/port"
	"github.com/murilo/pr-follower/backend/internal/usecase/notify"
)

// world is a small followed repository: alice and bob follow arvore/hive-web, whose CODEOWNERS
// gives every file to alice.
type world struct {
	log      *fakeLog
	mem      *fakeMemory
	store    *fakeStore
	repo     entity.Repository
	prs      map[uint64]*entity.PullRequest
	reviews  map[uint64][]entity.PullRequestReview
	analyses map[uint64]entity.RiskVerdict
	states   map[uint64]entity.UserPRState // alice's
	users    map[uint64]entity.User
	follows  []uint64
	woken    int
}

const (
	alice uint64 = 1
	bob   uint64 = 2
)

func newWorld() *world {
	w := &world{
		log:      &fakeLog{},
		mem:      newFakeMemory(),
		store:    &fakeStore{},
		repo:     entity.Repository{ID: 10, FullName: "arvore/hive-web", CodeownersStatus: entity.CodeownersPresent, CachedCodeownersRules: []entity.CodeownerRule{{Pattern: "*", Owners: []string{"@alice"}}}},
		prs:      map[uint64]*entity.PullRequest{},
		reviews:  map[uint64][]entity.PullRequestReview{},
		analyses: map[uint64]entity.RiskVerdict{},
		states:   map[uint64]entity.UserPRState{},
		users:    map[uint64]entity.User{alice: {ID: alice, Login: "alice"}, bob: {ID: bob, Login: "bob"}},
		follows:  []uint64{alice, bob},
	}
	w.mem.follows = []port.Follow{{UserID: alice, RepositoryID: 10}, {UserID: bob, RepositoryID: 10}}
	return w
}

func (w *world) notifier() *notify.Notifier {
	return notify.NewNotifier(notify.NotifierDeps{
		Log: w.log, Memory: w.mem, Store: w.store, Repos: w, Followers: w, PRs: prsOf{w}, Users: usersOf{w}, Files: w, Reviews: w,
		Wake: func(context.Context) { w.woken++ },
	})
}

func (w *world) openPR(id uint64, head string) *entity.PullRequest {
	pr := &entity.PullRequest{ID: id, RepositoryID: 10, RepositoryName: "arvore/hive-web", Number: int(id), Title: "PR " + head, HeadSHA: head, State: entity.PRStateOpen, CIStatus: entity.CIStatusSuccess}
	w.prs[id] = pr
	return pr
}

func (w *world) event(typ string, prID uint64, actor string) {
	w.log.append(entity.ActivityEvent{ID: typ, Type: typ, RepositoryID: 10, RepositoryName: "arvore/hive-web", PullRequestID: prID, PRNumber: int(prID), ActorLogin: actor})
}

func pass(t *testing.T, n *notify.Notifier) {
	t.Helper()
	if err := n.Pass(context.Background()); err != nil {
		t.Fatalf("Pass() error: %v", err)
	}
}

func (w *world) of(user uint64) []entity.Notification {
	var out []entity.Notification
	for _, n := range w.store.created {
		if n.UserID == user {
			out = append(out, n)
		}
	}
	return out
}

func TestNotifier_StartsAtTheNewestActivityAndFollowsSilently(t *testing.T) {
	t.Parallel()
	w := newWorld()
	w.openPR(1, "s1") // already in alice's Minha Vez before notifications existed
	w.event("pr.opened", 1, "carol")
	n := w.notifier()

	pass(t, n)
	if len(w.store.created) != 0 {
		t.Fatalf("the upgrade notifies nobody: %+v", w.store.created)
	}
	if seq, ok, _ := w.mem.LogPosition(context.Background(), notify.NotifierLogPosition); !ok || seq != 1 {
		t.Fatalf("the notifier starts at the newest activity, got %d %v", seq, ok)
	}
	if watch := w.mem.watches[key(alice, 1)]; watch.Turn != entity.TurnStateWaitingOnMe || watch.HeadSHA != "s1" {
		t.Fatalf("the open PRs of a new follow are recorded silently: %+v", watch)
	}
	if !w.mem.baselines[key(alice, 10)] || !w.mem.baselines[key(bob, 10)] {
		t.Fatal("both follows are seen")
	}

	w.event("pr.reviewed", 1, "carol") // still alice's turn: not news
	pass(t, n)
	if len(w.store.created) != 0 {
		t.Fatalf("a PR that stays in Minha Vez does not notify again: %+v", w.store.created)
	}
}

func TestNotifier_ANewPRInMinhaVezNotifiesItsOwnerOnly(t *testing.T) {
	t.Parallel()
	w := newWorld()
	n := w.notifier()
	pass(t, n)

	w.openPR(2, "s2")
	w.event("pr.opened", 2, "carol")
	pass(t, n)
	got := w.of(alice)
	if len(got) != 1 || got[0].Kind != entity.NotificationKindTurn || got[0].Reason != entity.NotificationReasonReviewRequested || got[0].PullRequestID != 2 || got[0].ID == 0 {
		t.Fatalf("alice owns the files: %+v", w.store.created)
	}
	if len(w.of(bob)) != 0 {
		t.Fatalf("bob owns nothing here: %+v", w.of(bob))
	}
	if w.woken != 1 {
		t.Fatalf("the channels are woken once a notification exists, woken %d", w.woken)
	}
}

func TestNotifier_RedeliveredActivityNotifiesOnce(t *testing.T) {
	t.Parallel()
	w := newWorld()
	n := w.notifier()
	pass(t, n)

	w.openPR(2, "s2")
	w.event("pr.opened", 2, "carol")
	pass(t, n)
	for range 2 { // the same webhook again, and a replica reading the same activity after a crash
		w.event("pr.opened", 2, "carol")
		pass(t, n)
	}
	_ = w.mem.SaveLogPosition(context.Background(), notify.NotifierLogPosition, 0)
	pass(t, notify.NewNotifier(notify.NotifierDeps{Log: w.log, Memory: w.mem, Store: w.store, Repos: w, Followers: w, PRs: prsOf{w}, Users: usersOf{w}, Files: w, Reviews: w}))
	if got := w.of(alice); len(got) != 1 {
		t.Fatalf("one notification however often the activity is read: %+v", got)
	}
}

func TestNotifier_MyOwnActionsDoNotNotifyMe(t *testing.T) {
	t.Parallel()
	w := newWorld()
	n := w.notifier()
	pass(t, n)

	w.openPR(2, "s2")
	w.event("pr.opened", 2, "Alice") // alice opened it herself (and owns the files)
	pass(t, n)
	if len(w.store.created) != 0 {
		t.Fatalf("got %+v", w.store.created)
	}
}

func TestNotifier_ReasonsFollowTheTurn(t *testing.T) {
	t.Parallel()
	w := newWorld()
	pr := w.openPR(1, "s1")
	w.states[1] = entity.UserPRState{SnoozedUntilCondition: entity.SnoozeConditionNextCommit, SnoozedAtSHA: "s1"}
	n := w.notifier()
	pass(t, n)

	pr.HeadSHA = "s2" // the commit that ends the snooze
	w.event("pr.synchronize", 1, "carol")
	pass(t, n)
	if got := w.of(alice); len(got) != 1 || got[0].Reason != entity.NotificationReasonSnoozeEnded || got[0].CommitSHA != "s2" {
		t.Fatalf("want snooze_ended: %+v", got)
	}

	w.reviews[1] = []entity.PullRequestReview{{GitHubReviewID: 5, ReviewerLogin: "alice", State: entity.ReviewApproved, CommitSHA: "s2", SubmittedAt: time.Now()}}
	w.event("pr.approved", 1, "alice")
	pass(t, n)
	pr.HeadSHA = "s3"
	w.event("pr.synchronize", 1, "carol")
	pass(t, n)
	if got := w.of(alice); len(got) != 2 || got[1].Reason != entity.NotificationReasonNewCommit {
		t.Fatalf("want new_commit after my approval: %+v", got)
	}
}

func TestNotifier_InvolvedAndRiskyPRs(t *testing.T) {
	t.Parallel()
	w := newWorld()
	pr := w.openPR(1, "s1")
	w.reviews[1] = []entity.PullRequestReview{{GitHubReviewID: 5, ReviewerLogin: "bob", State: entity.ReviewCommented, CommitSHA: "s1", SubmittedAt: time.Now()}}
	dep := w.openPR(3, "d1")
	dep.IsDependabot = true
	n := w.notifier()
	pass(t, n)

	pr.State = entity.PRStateMerged
	w.event("pr.merged", 1, "carol")
	pass(t, n)
	if got := w.of(bob); len(got) != 1 || got[0].Kind != entity.NotificationKindInvolved || got[0].Reason != entity.NotificationReasonMerged {
		t.Fatalf("bob commented, so the merge is his news: %+v", got)
	}
	if got := w.of(alice); len(got) != 0 {
		t.Fatalf("alice never acted on it: %+v", got)
	}

	w.analyses[3] = entity.RiskVerdictHigh
	w.log.append(entity.ActivityEvent{ID: "a", Type: "dependabot.analyzed", RepositoryID: 10, PullRequestID: 3, ActorLogin: "dependabot[bot]", Params: map[string]string{"package": "lodash", "from": "4", "to": "5"}})
	pass(t, n)
	for _, user := range []uint64{alice, bob} {
		got := w.of(user)
		last := got[len(got)-1]
		if last.Kind != entity.NotificationKindRisk || last.Details["package"] != "lodash" {
			t.Fatalf("every follower hears of a high-risk bump: %+v", got)
		}
	}
}

func TestNotifier_StopsOnCancellationAndKeepsItsPlace(t *testing.T) {
	t.Parallel()
	w := newWorld()
	n := w.notifier()
	pass(t, n)
	w.openPR(2, "s2")
	w.event("pr.opened", 2, "carol")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := n.Pass(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancellation, got %v", err)
	}
	pass(t, n)
	if len(w.of(alice)) != 1 {
		t.Fatalf("the activity is handled on the next pass: %+v", w.store.created)
	}
}

// --- fakes ---

func key(a, b uint64) string {
	return strings.Join([]string{string(rune('0' + a)), string(rune('0' + b))}, ":")
}

type fakeLog struct{ events []port.LoggedActivity }

func (l *fakeLog) append(ev entity.ActivityEvent) {
	l.events = append(l.events, port.LoggedActivity{Seq: int64(len(l.events) + 1), Event: ev})
}
func (l *fakeLog) Append(context.Context, entity.ActivityEvent) (int64, error) { return 0, nil }
func (l *fakeLog) ListSince(_ context.Context, after int64, limit int) ([]port.LoggedActivity, error) {
	var out []port.LoggedActivity
	for _, e := range l.events {
		if e.Seq > after && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, nil
}
func (l *fakeLog) LatestSeq(context.Context) (int64, error) { return int64(len(l.events)), nil }
func (l *fakeLog) Recent(context.Context, []uint64, int) ([]entity.ActivityEvent, error) {
	return nil, nil
}
func (l *fakeLog) Prune(context.Context, time.Time) (int64, error) { return 0, nil }

type fakeMemory struct {
	watches   map[string]entity.NotificationWatch
	baselines map[string]bool
	follows   []port.Follow
	positions map[string]int64
}

func newFakeMemory() *fakeMemory {
	return &fakeMemory{watches: map[string]entity.NotificationWatch{}, baselines: map[string]bool{}, positions: map[string]int64{}}
}
func (m *fakeMemory) GetNotificationWatch(_ context.Context, userID, prID uint64) (*entity.NotificationWatch, error) {
	if w, ok := m.watches[key(userID, prID)]; ok {
		return &w, nil
	}
	return nil, nil
}
func (m *fakeMemory) SaveNotificationWatch(_ context.Context, w entity.NotificationWatch) error {
	m.watches[key(w.UserID, w.PullRequestID)] = w
	return nil
}
func (m *fakeMemory) HasNotificationBaseline(_ context.Context, userID, repoID uint64) (bool, error) {
	return m.baselines[key(userID, repoID)], nil
}
func (m *fakeMemory) SaveNotificationBaseline(_ context.Context, userID, repoID uint64, _ time.Time) error {
	m.baselines[key(userID, repoID)] = true
	return nil
}
func (m *fakeMemory) ListFollowsWithoutBaseline(_ context.Context, limit int) ([]port.Follow, error) {
	var out []port.Follow
	for _, f := range m.follows {
		if !m.baselines[key(f.UserID, f.RepositoryID)] && len(out) < limit {
			out = append(out, f)
		}
	}
	return out, nil
}
func (m *fakeMemory) ForgetUnfollowed(context.Context) error { return nil }
func (m *fakeMemory) LogPosition(_ context.Context, name string) (int64, bool, error) {
	seq, ok := m.positions[name]
	return seq, ok, nil
}
func (m *fakeMemory) SaveLogPosition(_ context.Context, name string, seq int64) error {
	m.positions[name] = seq
	return nil
}

type fakeStore struct {
	port.NotificationStore
	created []entity.Notification
}

func (s *fakeStore) CreateNotifications(_ context.Context, ns []entity.Notification) ([]entity.Notification, error) {
	var out []entity.Notification
	for _, n := range ns {
		dup := false
		for _, c := range s.created {
			dup = dup || (c.UserID == n.UserID && c.Key() == n.Key())
		}
		if dup {
			continue
		}
		n.ID = uint64(len(s.created) + 1)
		s.created = append(s.created, n)
		out = append(out, n)
	}
	return out, nil
}

func (w *world) GetByID(_ context.Context, id uint64) (*entity.Repository, error) {
	if id != w.repo.ID {
		return nil, entity.ErrNotFound
	}
	r := w.repo
	return &r, nil
}
func (w *world) ListFollowerIDs(context.Context, uint64) ([]uint64, error) { return w.follows, nil }

type prsOf struct{ *world }

type usersOf struct{ *world }

func (w prsOf) GetByID(_ context.Context, id uint64) (*entity.PullRequest, error) {
	pr, ok := w.prs[id]
	if !ok {
		return nil, entity.ErrNotFound
	}
	c := *pr
	return &c, nil
}
func (w *world) ListOpenByRepoIDs(context.Context, []uint64) ([]entity.PullRequest, error) {
	var out []entity.PullRequest
	for _, pr := range w.prs {
		if pr.State == entity.PRStateOpen {
			out = append(out, *pr)
		}
	}
	return out, nil
}
func (w *world) GetAnalysisByPRAndSHA(_ context.Context, prID uint64, sha string) (*entity.DependabotAnalysis, error) {
	if v, ok := w.analyses[prID]; ok {
		return &entity.DependabotAnalysis{PullRequestID: prID, RiskVerdict: v}, nil
	}
	return nil, entity.ErrNotFound
}
func (w *world) ListUserPRStates(_ context.Context, userID uint64, _ []uint64) (map[uint64]entity.UserPRState, error) {
	if userID != alice {
		return map[uint64]entity.UserPRState{}, nil
	}
	return w.states, nil
}
func (w usersOf) GetByID(_ context.Context, id uint64) (*entity.User, error) {
	u, ok := w.users[id]
	if !ok {
		return nil, entity.ErrNotFound
	}
	return &u, nil
}
func (w *world) ListChangedFiles(_ context.Context, ids []uint64) (map[uint64]entity.PRChangedFiles, error) {
	out := map[uint64]entity.PRChangedFiles{}
	for _, id := range ids {
		if pr, ok := w.prs[id]; ok {
			out[id] = entity.PRChangedFiles{PullRequestID: id, HeadSHA: pr.HeadSHA, Files: []entity.ChangedFile{{Path: "src/app.ts"}}}
		}
	}
	return out, nil
}
func (w *world) ListReviews(_ context.Context, ids []uint64) (map[uint64][]entity.PullRequestReview, error) {
	out := map[uint64][]entity.PullRequestReview{}
	for _, id := range ids {
		out[id] = w.reviews[id]
	}
	return out, nil
}
