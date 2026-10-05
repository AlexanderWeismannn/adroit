package ci

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name    string
		entries []rollupEntry
		want    State
		passed  int
		failed  int
		pending int
	}{
		{
			name:    "empty rollup has no verdict",
			entries: nil,
			want:    StateUnknown,
		},
		{
			name: "all conclusions passed",
			entries: []rollupEntry{
				{Status: "COMPLETED", Conclusion: "SUCCESS"},
				{Status: "COMPLETED", Conclusion: "NEUTRAL"},
			},
			want:   StateSuccess,
			passed: 2,
		},
		{
			// The reason the switch is ordered worst-first: a red check is the
			// thing the badge exists to surface, even mid-run.
			name: "a failure outranks checks still running",
			entries: []rollupEntry{
				{Status: "COMPLETED", Conclusion: "FAILURE"},
				{Status: "IN_PROGRESS"},
				{Status: "COMPLETED", Conclusion: "SUCCESS"},
			},
			want:    StateFailure,
			passed:  1,
			failed:  1,
			pending: 1,
		},
		{
			name: "pending outranks success",
			entries: []rollupEntry{
				{Status: "QUEUED"},
				{Status: "COMPLETED", Conclusion: "SUCCESS"},
			},
			want:    StatePending,
			passed:  1,
			pending: 1,
		},
		{
			// Path-filtered jobs and superseded runs are the normal case on a
			// healthy branch; counting them red would leave most branches red.
			name: "skipped and cancelled are ignored, not failures",
			entries: []rollupEntry{
				{Status: "COMPLETED", Conclusion: "SKIPPED"},
				{Status: "COMPLETED", Conclusion: "CANCELLED"},
				{Status: "COMPLETED", Conclusion: "SUCCESS"},
			},
			want:   StateSuccess,
			passed: 1,
		},
		{
			name: "a rollup of nothing but skips has no verdict",
			entries: []rollupEntry{
				{Status: "COMPLETED", Conclusion: "SKIPPED"},
				{Status: "COMPLETED", Conclusion: "CANCELLED"},
			},
			want: StateUnknown,
		},
		{
			name: "timed out and action required count as failures",
			entries: []rollupEntry{
				{Status: "COMPLETED", Conclusion: "TIMED_OUT"},
				{Status: "COMPLETED", Conclusion: "ACTION_REQUIRED"},
			},
			want:   StateFailure,
			failed: 2,
		},
		{
			// Legacy StatusContext entries carry neither conclusion nor status.
			name: "legacy status contexts are read from state",
			entries: []rollupEntry{
				{State: "FAILURE"},
				{State: "SUCCESS"},
			},
			want:   StateFailure,
			passed: 1,
			failed: 1,
		},
		{
			name: "lowercase outcomes are normalized",
			entries: []rollupEntry{
				{Conclusion: "success"},
			},
			want:   StateSuccess,
			passed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(tt.entries)
			if got.State != tt.want {
				t.Errorf("State = %v, want %v", got.State, tt.want)
			}
			if got.Passed != tt.passed {
				t.Errorf("Passed = %d, want %d", got.Passed, tt.passed)
			}
			if got.Failed != tt.failed {
				t.Errorf("Failed = %d, want %d", got.Failed, tt.failed)
			}
			if got.Pending != tt.pending {
				t.Errorf("Pending = %d, want %d", got.Pending, tt.pending)
			}
		})
	}
}

// A CheckRun reports `status` while running and `conclusion` once done; the
// conclusion must win, or a finished check reads as still pending.
func TestOutcomeOfPrefersConclusion(t *testing.T) {
	got := outcomeOf(rollupEntry{Status: "COMPLETED", Conclusion: "FAILURE", State: "SUCCESS"})
	if got != "FAILURE" {
		t.Errorf("outcomeOf = %q, want FAILURE", got)
	}

	got = outcomeOf(rollupEntry{Status: "IN_PROGRESS", State: "SUCCESS"})
	if got != "IN_PROGRESS" {
		t.Errorf("outcomeOf = %q, want IN_PROGRESS", got)
	}
}

// Get must answer instantly from cache; a network round trip on the 500ms
// metadata tick would stall the diff stats computed alongside it.
func TestGetIsNonBlockingAndSkipsBlankInput(t *testing.T) {
	if s := Get("", "/worktree", "branch"); s.State != StateUnknown {
		t.Errorf("blank repo path: State = %v, want StateUnknown", s.State)
	}
	if s := Get("/repo", "", "branch"); s.State != StateUnknown {
		t.Errorf("blank worktree path: State = %v, want StateUnknown", s.State)
	}
}

// A merged PR's checks are all green, so without this the row would look exactly
// like an open branch that is passing -- and an abandoned PR would too.
func TestTerminalState(t *testing.T) {
	tests := []struct {
		prState string
		want    State
	}{
		{"MERGED", StateMerged},
		{"merged", StateMerged},
		{"CLOSED", StateClosed},
		{"closed", StateClosed},
		{"OPEN", StateUnknown},
		{"", StateUnknown},
	}

	for _, tt := range tests {
		if got := terminalState(tt.prState); got != tt.want {
			t.Errorf("terminalState(%q) = %v, want %v", tt.prState, got, tt.want)
		}
	}
}

// Only an explicit CONFLICTING is a conflict. GitHub computes mergeability
// asynchronously, so UNKNOWN is the ordinary answer for the seconds after a push
// -- reading it as a conflict would flash a blocker on every branch just pushed.
func TestMergeability(t *testing.T) {
	tests := []struct {
		value string
		want  Mergeability
	}{
		{"CONFLICTING", MergeConflicting},
		{"conflicting", MergeConflicting},
		{"MERGEABLE", MergeClean},
		{"UNKNOWN", MergeUnknown},
		{"", MergeUnknown},
		{"SOMETHING_NEW", MergeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := mergeability(tt.value); got != tt.want {
				t.Errorf("mergeability(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// REVIEW_REQUIRED and "" both mean nobody has said anything yet, which is true of
// every pull request at some point and so earns no marker.
func TestReviewState(t *testing.T) {
	tests := []struct {
		value string
		want  ReviewState
	}{
		{"APPROVED", ReviewApproved},
		{"CHANGES_REQUESTED", ReviewChangesRequested},
		{"changes_requested", ReviewChangesRequested},
		{"REVIEW_REQUIRED", ReviewNone},
		{"", ReviewNone},
		{"SOMETHING_NEW", ReviewNone},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			if got := reviewState(tt.value); got != tt.want {
				t.Errorf("reviewState(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

// The two pull-request facts ride the same lookup as the checks, so a decoded
// view has to carry all three or the badge silently loses a marker.
func TestFetchDecodesPRLevelFacts(t *testing.T) {
	const payload = `{"number":4406,"state":"OPEN","mergeable":"CONFLICTING",` +
		`"reviewDecision":"CHANGES_REQUESTED","statusCheckRollup":` +
		`[{"status":"COMPLETED","conclusion":"SUCCESS"}]}`

	var view prView
	if err := json.Unmarshal([]byte(payload), &view); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	status := classify(view.StatusCheckRollup)
	status.PRNumber = view.Number
	status.Mergeable = mergeability(view.Mergeable)
	status.Review = reviewState(view.ReviewDecision)

	if status.State != StateSuccess {
		t.Errorf("State = %v, want StateSuccess", status.State)
	}
	if status.PRNumber != 4406 {
		t.Errorf("PRNumber = %d, want 4406", status.PRNumber)
	}
	if status.Mergeable != MergeConflicting {
		t.Errorf("Mergeable = %v, want MergeConflicting", status.Mergeable)
	}
	if status.Review != ReviewChangesRequested {
		t.Errorf("Review = %v, want ReviewChangesRequested", status.Review)
	}
}

func review(state string, at time.Time) reviewEntry {
	return reviewEntry{State: state, SubmittedAt: at}
}
func comment(at time.Time) commentEntry { return commentEntry{CreatedAt: at} }

// An approval is not feedback. It already has its own marker, and counting it
// would light the comment marker on every approved pull request -- which is most
// of them, making the marker mean nothing.
func TestAnApprovalIsNotCountedAsFeedback(t *testing.T) {
	now := time.Now()
	v := prView{LatestReviews: []reviewEntry{review("APPROVED", now)}}

	_, any := v.lastRemark()
	require.False(t, any, "an approval on its own is not a remark")
	require.Equal(t, FeedbackNone, feedbackState(v, now.Add(-time.Hour)))
}

// The case the whole thing exists for: GitHub's reviewDecision only moves on an
// approval or a request for changes, so a review submitted as COMMENTED -- what
// every review bot does -- leaves it empty and the pull request looks untouched.
func TestACommentedReviewCountsEvenThoughReviewDecisionIsEmpty(t *testing.T) {
	pushed := time.Now().Add(-time.Hour)
	v := prView{
		ReviewDecision: "",
		LatestReviews:  []reviewEntry{review("COMMENTED", pushed.Add(time.Minute))},
	}

	require.Equal(t, ReviewNone, reviewState(v.ReviewDecision), "precondition: GitHub says nothing")
	require.Equal(t, FeedbackOutstanding, feedbackState(v, pushed))
}

func TestFeedbackIsDatedAgainstTheBranchHead(t *testing.T) {
	pushed := time.Now().Add(-time.Hour)

	cases := []struct {
		name string
		view prView
		head time.Time
		want Feedback
	}{
		{"nothing said", prView{}, pushed, FeedbackNone},
		{"a comment after the push", prView{Comments: []commentEntry{comment(pushed.Add(time.Minute))}}, pushed, FeedbackOutstanding},
		{"a comment before the push", prView{Comments: []commentEntry{comment(pushed.Add(-time.Minute))}}, pushed, FeedbackAddressed},
		{"changes requested after the push",
			prView{LatestReviews: []reviewEntry{review("CHANGES_REQUESTED", pushed.Add(time.Minute))}}, pushed, FeedbackOutstanding},
		// Approved later, but the earlier remark still predates the push.
		{"an approval does not hide an older remark",
			prView{LatestReviews: []reviewEntry{
				review("COMMENTED", pushed.Add(-time.Hour)),
				review("APPROVED", pushed.Add(time.Minute)),
			}}, pushed, FeedbackAddressed},
		// Claiming feedback is unanswered is a stronger statement than an
		// unreadable worktree supports.
		{"an unknown head reports presence, not urgency",
			prView{Comments: []commentEntry{comment(pushed)}}, time.Time{}, FeedbackAddressed},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, feedbackState(c.view, c.head))
		})
	}
}

// The most recent remark decides, wherever it came from.
func TestLastRemarkTakesTheLatestAcrossReviewsAndComments(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	v := prView{
		LatestReviews: []reviewEntry{review("COMMENTED", base.Add(time.Minute))},
		Comments:      []commentEntry{comment(base), comment(base.Add(10 * time.Minute))},
	}
	last, any := v.lastRemark()
	require.True(t, any)
	require.Equal(t, base.Add(10*time.Minute), last)
}

// A settled pull request is looked up rarely; a live one on the normal clock.
func TestSettledPullRequestsAreRefreshedRarely(t *testing.T) {
	for _, st := range []State{StateMerged, StateClosed} {
		if ttlFor(Status{State: st}) != settledTTL {
			t.Fatalf("state %v should use the settled ttl", st)
		}
	}
	for _, st := range []State{StateUnknown, StateNoPR, StatePending, StateFailure, StateSuccess} {
		if ttlFor(Status{State: st}) != ttl {
			t.Fatalf("state %v should use the normal ttl", st)
		}
	}
}
