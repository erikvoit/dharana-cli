package project

import (
	"context"
	"errors"
	"testing"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/auth"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
)

type fakeStore struct {
	cfg *config.File
}

func (s *fakeStore) Load() (*config.File, error) {
	if s.cfg == nil {
		return &config.File{}, nil
	}
	return s.cfg, nil
}

func (s *fakeStore) Save(cfg *config.File) error {
	s.cfg = cfg
	return nil
}

type fakeTokenStore struct {
	token string
}

func (s *fakeTokenStore) Save(token string) error {
	s.token = token
	return nil
}

func (s *fakeTokenStore) Load() (string, error) {
	if s.token == "" {
		return "", auth.ErrTokenNotFound
	}
	return s.token, nil
}

func (s *fakeTokenStore) Delete() error {
	s.token = ""
	return nil
}

type fakeAsana struct {
	projects []asana.Project
	project  *asana.Project
}

func (f *fakeAsana) CurrentUser(_ context.Context, _ string) (*asana.User, error) {
	return &asana.User{GID: "u1", Name: "User"}, nil
}

func (f *fakeAsana) Projects(_ context.Context, _ string, _ string) ([]asana.Project, error) {
	return f.projects, nil
}

func (f *fakeAsana) Project(_ context.Context, _ string, _ string) (*asana.Project, error) {
	if f.project == nil {
		return nil, errors.New("not found")
	}
	return f.project, nil
}

func TestSelectByNameRejectsAmbiguousProjects(t *testing.T) {
	service := &Service{
		Auth:   &auth.Service{Store: &fakeTokenStore{token: "token"}},
		Config: &fakeStore{},
		Asana: &fakeAsana{projects: []asana.Project{
			{GID: "1", Name: "Delivery", Workspace: asana.Workspace{GID: "w1", Name: "One"}},
			{GID: "2", Name: "Delivery", Workspace: asana.Workspace{GID: "w2", Name: "Two"}},
		}},
	}

	_, err := service.Select(context.Background(), SelectOptions{Name: "Delivery"})
	if err == nil {
		t.Fatal("expected error")
	}
	appErr, ok := err.(*output.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != "AMBIGUOUS_PROJECT" {
		t.Fatalf("expected AMBIGUOUS_PROJECT, got %q", appErr.Code)
	}
	candidates, ok := appErr.Candidates.([]ProjectValue)
	if !ok || len(candidates) != 2 {
		t.Fatalf("expected two candidates, got %#v", appErr.Candidates)
	}
}

func TestSelectByGIDSavesActiveProject(t *testing.T) {
	store := &fakeStore{}
	service := &Service{
		Auth:   &auth.Service{Store: &fakeTokenStore{token: "token"}},
		Config: store,
		Asana:  &fakeAsana{project: &asana.Project{GID: "123", Name: "Dharana", Workspace: asana.Workspace{GID: "w1", Name: "Workspace"}}},
	}

	result, err := service.Select(context.Background(), SelectOptions{GID: "123"})
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if result.ActiveProject.GID != "123" {
		t.Fatalf("unexpected selected project: %#v", result.ActiveProject)
	}
	if store.cfg == nil || store.cfg.ActiveProject == nil {
		t.Fatal("active project was not saved")
	}
	if store.cfg.ActiveProject.Name != "Dharana" {
		t.Fatalf("unexpected saved project: %#v", store.cfg.ActiveProject)
	}
}
