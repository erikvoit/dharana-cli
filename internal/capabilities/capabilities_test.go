package capabilities

import "testing"

func TestRemoteCommandsDeclareRequiredScopes(t *testing.T) {
	result := All()
	for _, command := range result.Commands {
		if command.RequiresAuth && (command.ReadsRemote || command.MutatesRemote) && command.Name != "auth refresh" && len(command.RequiredScopes) == 0 {
			t.Fatalf("remote command %s has no declared scopes", command.Name)
		}
	}
	plan, ok := Find("plan apply")
	if !ok || !contains(plan.RequiredScopes, "tasks:read") || !contains(plan.RequiredScopes, "tasks:write") {
		t.Fatalf("plan apply scopes incomplete: %#v", plan.RequiredScopes)
	}
}
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
