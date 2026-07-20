package workflowstate

import "testing"

func TestCanonicalTransitions(t *testing.T) {
	if value, ok := Normalize("Selected for Development"); !ok || value != Selected {
		t.Fatalf("unexpected normalized state %q ok=%t", value, ok)
	}
	if !CanTransition(Selected, InProgress) || !CanTransition(Verification, Done) || !CanTransition(Done, Selected) {
		t.Fatal("expected canonical transitions")
	}
	if CanTransition(Backlog, Done) || CanTransition(Done, InProgress) {
		t.Fatal("unsafe transition was accepted")
	}
}
