package project

import (
	"context"
	"testing"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
)

func stateTestService(client *fakeAsana, store *fakeStore) *Service {
	client.project = &asana.Project{GID: "p1", Name: "Project", Workspace: asana.Workspace{GID: "w1", Name: "Workspace"}}
	return &Service{Auth: &auth.Service{Store: &fakeTokenStore{token: "token"}}, Config: store, Asana: client}
}

func stateProjectConfig() *config.File {
	return &config.File{ActiveProject: &config.ProjectConfig{GID: "p1", Name: "Project", WorkspaceGID: "w1", WorkspaceName: "Workspace"}}
}

func TestProvisionStatesDryRunDoesNotMutate(t *testing.T) {
	client := &fakeAsana{}
	service := stateTestService(client, &fakeStore{cfg: stateProjectConfig()})

	result, err := service.ProvisionStates(context.Background(), StateProvisionOptions{DryRun: true})
	if err != nil {
		t.Fatalf("ProvisionStates returned error: %v", err)
	}
	if !result.DryRun || result.Applied || client.createdFieldCount != 0 || client.attachedFieldGID != "" {
		t.Fatalf("dry-run mutated remote state: result=%#v client=%#v", result, client)
	}
}

func TestProvisionStatesCreatesAttachesAndPersistsMappings(t *testing.T) {
	client := &fakeAsana{}
	store := &fakeStore{cfg: stateProjectConfig()}
	service := stateTestService(client, store)

	result, err := service.ProvisionStates(context.Background(), StateProvisionOptions{Apply: true})
	if err != nil {
		t.Fatalf("ProvisionStates returned error: %v", err)
	}
	if !result.Applied || !result.CreatedField || !result.AttachedField || client.createdFieldCount != 1 || len(client.createdOptions) != 7 {
		t.Fatalf("unexpected provisioning result: %#v client=%#v", result, client)
	}
	if client.attachedFieldGID != "state-field" || store.cfg == nil || !store.cfg.States.Complete() {
		t.Fatalf("state mapping was not attached and persisted: client=%#v config=%#v", client, store.cfg)
	}
}

func TestBindStatesAdoptsCompatibleAttachedField(t *testing.T) {
	options := []asana.EnumOption{
		{GID: "backlog", Name: "Backlog", Enabled: true},
		{GID: "selected", Name: "Selected for Development", Enabled: true},
		{GID: "progress", Name: "In Progress", Enabled: true},
		{GID: "verification", Name: "Verification", Enabled: true},
		{GID: "done", Name: "Done", Enabled: true},
		{GID: "deferred", Name: "Deferred", Enabled: true},
		{GID: "canceled", Name: "Canceled", Enabled: true},
	}
	client := &fakeAsana{fields: []asana.CustomFieldSetting{{CustomField: asana.CustomField{GID: "existing", Name: "Dharana State", Type: "enum", EnumOptions: options}}}}
	store := &fakeStore{cfg: stateProjectConfig()}
	service := stateTestService(client, store)

	result, err := service.BindStates(context.Background(), StateBindOptions{FieldGID: "existing"})
	if err != nil {
		t.Fatalf("BindStates returned error: %v", err)
	}
	if !result.Configured || !result.Attached || store.cfg.States.InProgress != "progress" {
		t.Fatalf("unexpected bind result: %#v config=%#v", result, store.cfg.States)
	}
}
