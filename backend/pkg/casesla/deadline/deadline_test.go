package deadline

import (
	"testing"
	"time"
)

func fixture() StageInput {
	at := time.Date(2026, 10, 7, 8, 0, 0, 0, time.FixedZone("ICT", 7*3600))
	return StageInput{ResourceType: "support", ResourceID: "case", Stage: "acknowledgement", WaitingOn: "admin", Duration: SupportAcknowledgement, At: at, CreatedAt: at}
}
func TestReminderBoundaryUTCAndRestart(t *testing.T) {
	in := fixture()
	i := New(in)
	if i.DueAt.Location() != time.UTC {
		t.Fatal("deadline must be UTC")
	}
	if got := i.Notices(in.At.Add(18*time.Hour - time.Nanosecond)); len(got) != 0 {
		t.Fatal(got)
	}
	if got := i.Notices(in.At.Add(18 * time.Hour)); len(got) != 1 || got[0] != "reminder" {
		t.Fatal(got)
	}
	if got := i.Notices(i.DueAt); len(got) != 1 || got[0] != "overdue" {
		t.Fatal(got)
	}
	if got := i.Notices(i.DueAt.Add(10 * 24 * time.Hour)); len(got) != 4 {
		t.Fatal(got)
	}
}
func TestRepeatedMessagesPauseResumeAndOuterDeadline(t *testing.T) {
	in := fixture()
	i := New(in)
	original := i.DueAt
	in.At = in.At.Add(6 * time.Hour)
	if i.Apply(in) || !i.DueAt.Equal(original) {
		t.Fatal("same stage reset deadline")
	}
	in.Pause = true
	in.WaitingOn = "buyer"
	i.Apply(in)
	if got := i.Notices(original); len(got) != 0 {
		t.Fatal("operator reminder while waiting buyer", got)
	}
	in.At = in.At.Add(24 * time.Hour)
	if i.Apply(in) {
		t.Fatal("repeated wait moved pause")
	}
	in.Pause = false
	in.WaitingOn = "admin"
	i.Apply(in)
	if !i.DueAt.Equal(original.Add(24 * time.Hour)) {
		t.Fatal("remaining budget lost", i.DueAt)
	}
	in.Pause = true
	i.Apply(in)
	if got := i.Notices(i.OverallDueAt); len(got) == 0 || got[0] != "overdue" {
		t.Fatal("outer deadline was paused")
	}
	in.At = i.OverallDueAt.Add(time.Hour)
	in.Pause = false
	i.Apply(in)
	if !i.DueAt.Equal(i.OverallDueAt) {
		t.Fatal("resume moved outer deadline")
	}
}
func TestExtensionPreservesBreachAndReopenVersion(t *testing.T) {
	in := fixture()
	i := New(in)
	old := i.DueAt
	if e := i.Extend(old.Add(time.Hour), old.Add(24*time.Hour)); e != nil {
		t.Fatal(e)
	}
	if i.BreachedAt == nil || !i.BreachedAt.Equal(old) {
		t.Fatal("extension erased breach")
	}
	if e := i.Extend(old, i.OverallDueAt.Add(time.Hour)); e == nil {
		t.Fatal("extension beyond cap")
	}
	v := i.DeadlineVersion
	in.Stage = ""
	i.Apply(in)
	in.Stage = "operator_response"
	in.At = old.Add(25 * time.Hour)
	i.Apply(in)
	if i.DeadlineVersion <= v || !i.Active || i.BreachedAt == nil {
		t.Fatal("reopen lost history")
	}
}
func TestLegacyNeverSends(t *testing.T) {
	in := fixture()
	in.Legacy = true
	i := New(in)
	if len(i.Notices(i.OverallDueAt.Add(7*24*time.Hour))) != 0 {
		t.Fatal("legacy mass mailing")
	}
}

func TestLeavingPausedStageStartsItsOwnPolicyAndKeepsBreach(t *testing.T) {
	in := fixture()
	i := New(in)
	in.Pause = true
	in.At = in.At.Add(25 * time.Hour)
	i.Apply(in)
	if i.BreachedAt == nil {
		t.Fatal("late request for buyer information erased operator breach")
	}
	in.Pause = false
	in.Stage = "vendor_response"
	in.Duration = VendorResponse
	in.WaitingOn = "vendor"
	in.At = in.At.Add(time.Hour)
	i.Apply(in)
	if !i.DueAt.Equal(in.At.Add(VendorResponse)) || i.PausedAt != nil {
		t.Fatal("new stage reused paused stage budget")
	}
}
