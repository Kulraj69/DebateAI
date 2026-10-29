package debate

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
)

const (
	testDebateID = "debate-1"
	testPollQ    = "Which side won?"
)

// newTestPollStore returns a PollStore backed by an in-process Redis fake, so
// the poll tests do not need a running Redis server.
func newTestPollStore(t *testing.T) *PollStore {
	t.Helper()

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr: server.Addr(),
		// miniredis does not implement the maintenance-notification handshake.
		MaintNotificationsConfig: &maintnotifications.Config{Mode: maintnotifications.ModeDisabled},
	})
	t.Cleanup(func() { _ = client.Close() })

	return &PollStore{rdb: client, ctx: context.Background()}
}

func mustCreatePoll(t *testing.T, ps *PollStore) string {
	t.Helper()

	pollID, err := ps.CreatePoll(testDebateID, "", testPollQ, []string{"For", "Against"})
	if err != nil {
		t.Fatalf("CreatePoll: %v", err)
	}

	return pollID
}

func pollCounts(t *testing.T, ps *PollStore, pollID string) map[string]int64 {
	t.Helper()

	counts, _, _, err := ps.GetPollState(testDebateID)
	if err != nil {
		t.Fatalf("GetPollState: %v", err)
	}

	return counts[pollID]
}

// assertVoteRejected checks that a vote is refused and that nothing moved: the
// spectator can still vote and the counts are untouched.
func assertVoteRejected(t *testing.T, ps *PollStore, pollID, option, spectator string) {
	t.Helper()

	before := pollCounts(t, ps, pollID)

	ok, err := ps.Vote(testDebateID, pollID, option, spectator)
	if err == nil {
		t.Fatalf("Vote(%q) was accepted: want an error, got nil", option)
	}
	if ok {
		t.Fatalf("Vote(%q) reported success", option)
	}

	hasVoted, err := ps.HasVoted(testDebateID, pollID, spectator)
	if err != nil {
		t.Fatalf("HasVoted: %v", err)
	}
	if hasVoted {
		t.Errorf("rejected Vote(%q) marked the spectator as having voted", option)
	}

	after := pollCounts(t, ps, pollID)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Errorf("rejected Vote(%q) changed the poll counts: before %v, after %v", option, before, after)
	}
}

func TestVoteRejectsOptionThatIsNotPartOfThePoll(t *testing.T) {
	ps := newTestPollStore(t)
	pollID := mustCreatePoll(t, ps)

	assertVoteRejected(t, ps, pollID, "totally-fake-option", "spectator-1")

	// The rejected vote must not consume the spectator's single vote.
	ok, err := ps.Vote(testDebateID, pollID, "For", "spectator-1")
	if err != nil {
		t.Fatalf("Vote with a valid option after a rejected one: %v", err)
	}
	if !ok {
		t.Fatal("Vote with a valid option after a rejected one returned false")
	}

	if got := pollCounts(t, ps, pollID)["For"]; got != 1 {
		t.Errorf("count for \"For\" = %d, want 1", got)
	}
}

func TestVoteRejectsUnknownPoll(t *testing.T) {
	ps := newTestPollStore(t)

	assertVoteRejected(t, ps, "no-such-poll", "For", "spectator-1")
}

func TestVoteRejectsPollWithoutOptions(t *testing.T) {
	ps := newTestPollStore(t)
	pollID := mustCreatePoll(t, ps)

	metaKey := fmt.Sprintf("debate:%s:poll:%s:meta", testDebateID, pollID)
	if err := ps.rdb.Del(ps.ctx, metaKey).Err(); err != nil {
		t.Fatalf("deleting poll metadata: %v", err)
	}

	assertVoteRejected(t, ps, pollID, "For", "spectator-1")
}

func TestVoteRejectsUnreadablePollMetadata(t *testing.T) {
	ps := newTestPollStore(t)
	pollID := mustCreatePoll(t, ps)

	metaKey := fmt.Sprintf("debate:%s:poll:%s:meta", testDebateID, pollID)
	if err := ps.rdb.Set(ps.ctx, metaKey, "{not json", 0).Err(); err != nil {
		t.Fatalf("overwriting poll metadata: %v", err)
	}

	assertVoteRejected(t, ps, pollID, "For", "spectator-1")
}

func TestVoteIgnoresInjectedCountFields(t *testing.T) {
	ps := newTestPollStore(t)
	pollID := mustCreatePoll(t, ps)

	// A field written into the counts hash is not an option on its own: the
	// poll's metadata decides. This is the state a pre-fix injection leaves.
	countsKey := fmt.Sprintf("debate:%s:poll:%s:counts", testDebateID, pollID)
	if err := ps.rdb.HSet(ps.ctx, countsKey, "totally-fake-option", 99).Err(); err != nil {
		t.Fatalf("injecting a count field: %v", err)
	}

	assertVoteRejected(t, ps, pollID, "totally-fake-option", "spectator-1")

	ok, err := ps.Vote(testDebateID, pollID, "Against", "spectator-1")
	if err != nil || !ok {
		t.Fatalf("Vote with a defined option = (%v, %v), want (true, nil)", ok, err)
	}

	counts := pollCounts(t, ps, pollID)
	if got := counts["totally-fake-option"]; got != 99 {
		t.Errorf("injected count = %d, want it left at 99", got)
	}
	if got := counts["Against"]; got != 1 {
		t.Errorf("count for \"Against\" = %d, want 1", got)
	}
}

func TestVoteCountsOneVotePerSpectator(t *testing.T) {
	ps := newTestPollStore(t)
	pollID := mustCreatePoll(t, ps)

	ok, err := ps.Vote(testDebateID, pollID, "For", "spectator-1")
	if err != nil || !ok {
		t.Fatalf("first Vote = (%v, %v), want (true, nil)", ok, err)
	}

	ok, err = ps.Vote(testDebateID, pollID, "For", "spectator-1")
	if err != nil {
		t.Fatalf("second Vote: %v", err)
	}
	if ok {
		t.Fatal("the same spectator was allowed to vote twice")
	}

	ok, err = ps.Vote(testDebateID, pollID, "Against", "spectator-2")
	if err != nil || !ok {
		t.Fatalf("Vote from a second spectator = (%v, %v), want (true, nil)", ok, err)
	}

	counts := pollCounts(t, ps, pollID)
	if got := counts["For"]; got != 1 {
		t.Errorf("count for \"For\" = %d, want 1", got)
	}
	if got := counts["Against"]; got != 1 {
		t.Errorf("count for \"Against\" = %d, want 1", got)
	}

	_, voters, _, err := ps.GetPollState(testDebateID)
	if err != nil {
		t.Fatalf("GetPollState: %v", err)
	}
	if got := voters[pollID]; got != 2 {
		t.Errorf("voter count = %d, want 2", got)
	}
}
