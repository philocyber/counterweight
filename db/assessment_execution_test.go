package db

import "testing"

func TestAssessmentCodeReviewBuiltinUpgrade(t *testing.T) {
	d := assessmentDB(t)
	// Keep the existing catalog and its operator customization, removing only the
	// new agents to simulate an installation created before source-review execution.
	general, err := d.GetAgentByKey("planner")
	if err != nil || general == nil {
		t.Fatal("existing planner missing")
	}
	for _, key := range []string{CodeReviewPlannerAgentKey, CodeReviewWorkerAgentKey} {
		if _, err := d.Exec(`DELETE FROM agents WHERE key=$1`, key); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		if err := d.seedBuiltins(); err != nil {
			t.Fatal(err)
		}
	}
	after, err := d.GetAgentByKey("planner")
	if err != nil || after.ID != general.ID || after.Name != general.Name {
		t.Fatal("existing planner changed")
	}
	for _, key := range []string{CodeReviewPlannerAgentKey, CodeReviewWorkerAgentKey} {
		a, err := d.GetAgentByKey(key)
		if err != nil || a == nil || !a.Builtin || a.InteractiveShell {
			t.Fatalf("invalid new agent %s: %+v %v", key, a, err)
		}
		skills, err := d.AgentSkillNames(a.ID)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, name := range skills {
			if name == "whitebox-code-review-methodology" {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s missing review methodology", key)
		}
		if _, err := d.Exec(`UPDATE agents SET name='Customized source reviewer' WHERE id=$1`, a.ID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = d.Exec(`UPDATE agents SET name=$1 WHERE id=$2`, a.Name, a.ID) })
		if err := d.seedBuiltins(); err != nil {
			t.Fatal(err)
		}
		customized, _ := d.GetAgentByKey(key)
		if customized.Name != "Customized source reviewer" {
			t.Fatal("startup overwrote edited agent")
		}
	}
}

func TestAssessmentExecutionIsolatedAndRegisteredStores(t *testing.T) {
	d := assessmentDB(t)
	expID, err := d.CreateExploration("isolated execution fixture", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID) })
	isolated, err := d.Exploration(expID).TaskExecution(expID)
	if err != nil || isolated.AssessmentType != AssessmentGeneral {
		t.Fatalf("isolated: %+v %v", isolated, err)
	}
	task := assessmentTask(t, d, TaskCreateOptions{AssessmentType: AssessmentCodeReview, CodeTarget: &TaskCodeTarget{Repository: "example/fixture", URL: "https://github.com/example/fixture", Mode: "light", LocalPath: t.TempDir()}})
	store := d.Exploration(task.ExplorationID)
	typed, err := store.TaskExecution(task.ID)
	if err != nil || typed.AssessmentType != AssessmentCodeReview {
		t.Fatalf("registered: %+v %v", typed, err)
	}
	if _, err := store.TaskExecution(task.ID + 1); err == nil {
		t.Fatal("mismatched task accepted")
	}
	if _, err := d.Exec(`UPDATE tasks SET deleted_at=now() WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TaskExecution(task.ID); err == nil {
		t.Fatal("deleted task fell back to general")
	}
	// Restore for the ordinary fixture cleanup.
	if _, err := d.Exec(`UPDATE tasks SET deleted_at=NULL WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
}
