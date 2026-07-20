package capabilities

import (
	"slices"
	"testing"
)

func TestRemoteCommandsDeclareRequiredScopes(t *testing.T) {
	result := All()
	for _, command := range result.Commands {
		if command.RequiresAuth && (command.ReadsRemote || command.MutatesRemote) && command.Name != "auth refresh" && len(command.RequiredScopes) == 0 {
			t.Fatalf("remote command %s has no declared scopes", command.Name)
		}
	}
	plan, ok := Find("plan apply")
	if !ok || !slices.Contains(plan.RequiredScopes, "tasks:read") || !slices.Contains(plan.RequiredScopes, "tasks:write") {
		t.Fatalf("plan apply scopes incomplete: %#v", plan.RequiredScopes)
	}
}
