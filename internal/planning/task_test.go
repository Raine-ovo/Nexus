package planning

import "testing"

func TestTaskManager_AssignConstrainsClaim(t *testing.T) {
	tm, err := NewTaskManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := tm.Create("Check observability", "validate traces and metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, assignErr := tm.Assign(task.ID, "planner-alpha", "devops-bravo", "devops", "dispatch infra validation"); assignErr != nil {
		t.Fatal(assignErr)
	}
	if _, claimErr := tm.Claim(task.ID, "planner-alpha", "planner", "manual"); claimErr == nil {
		t.Fatal("expected planner claim to be rejected by assignment")
	}
	claimed, err := tm.Claim(task.ID, "devops-bravo", "devops", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.AssignedTo != "devops-bravo" || claimed.AssignedRole != "devops" {
		t.Fatalf("expected assignment metadata to persist, got %+v", claimed)
	}
	if claimed.ClaimedRole != "devops" || claimed.LastClaimedRole != "devops" {
		t.Fatalf("expected claimed role audit fields to be populated, got %+v", claimed)
	}
}

func TestTaskManager_UpdateClearsActiveClaimButKeepsLastClaim(t *testing.T) {
	tm, err := NewTaskManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task, err := tm.Create("Write summary", "complete report", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimErr := tm.Claim(task.ID, "planner-alpha", "planner", "manual"); claimErr != nil {
		t.Fatal(claimErr)
	}
	if updateErr := tm.Update(task.ID, TaskCompleted); updateErr != nil {
		t.Fatal(updateErr)
	}
	got, err := tm.Get(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClaimedBy != "" || got.ClaimedRole != "" || got.ClaimSource != "" {
		t.Fatalf("expected active claim fields to be cleared, got %+v", got)
	}
	if got.LastClaimedBy != "planner-alpha" || got.LastClaimedRole != "planner" || got.LastClaimSource != "manual" {
		t.Fatalf("expected last claim audit fields to persist, got %+v", got)
	}
}
