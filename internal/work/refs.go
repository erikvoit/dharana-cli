package work

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/refcache"
)

type RefStore interface {
	Load() (*refcache.Cache, error)
	Replace(entries []refcache.Entry) (*refcache.Cache, error)
	Resolve(ref string) (*refcache.Entry, error)
}

type RefreshRefsOptions struct {
	Limit int
}

type RefreshRefsResult struct {
	UpdatedAt string           `json:"updated_at"`
	Count     int              `json:"count"`
	Items     []refcache.Entry `json:"items"`
}

type RefreshChangedRefsResult struct {
	UpdatedAt string   `json:"updated_at"`
	Refreshed []string `json:"refreshed,omitempty"`
	Removed   []string `json:"removed,omitempty"`
}

type ResolveRefResult struct {
	Entry refcache.Entry `json:"entry"`
}

func (s *Service) RefreshRefs(ctx context.Context, opts RefreshRefsOptions) (*RefreshRefsResult, error) {
	if opts.Limit <= 0 || opts.Limit > 100 {
		opts.Limit = 100
	}
	resolved, err := s.resolveToken()
	if err != nil {
		return nil, err
	}
	cfg, err := s.config().Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
	}
	if cfg.ActiveProject == nil || cfg.ActiveProject.GID == "" {
		return nil, output.NewError("PROJECT_NOT_CONFIGURED", "No active project is configured. Run project select first.")
	}

	var entries []refcache.Entry
	var offset string
	for {
		page, err := s.asana().ProjectTasks(ctx, resolved.Token, cfg.ActiveProject.GID, opts.Limit, offset)
		if err != nil {
			return nil, mapAsanaError(err, "Could not refresh friendly references.")
		}
		if page == nil {
			break
		}
		for _, task := range page.Tasks {
			item := toWorkItem(task, cfg.TaskTypes, cfg.States)
			entries = append(entries, refcache.Entry{
				Ref:       item.Ref,
				GID:       item.GID,
				Name:      item.Name,
				Type:      item.Type,
				Status:    item.Status,
				State:     item.State,
				Permalink: item.Permalink,
				ParentRef: parentRef(item.Parent),
				ParentGID: parentGID(item.Parent),
			})
		}
		if page.NextOffset == "" {
			break
		}
		offset = page.NextOffset
	}

	sortRefEntries(entries)
	cache, err := s.refsForProject(cfg.ActiveProject).Replace(entries)
	if err != nil {
		return nil, output.NewError("REF_CACHE_WRITE_FAILED", "Could not save local reference cache.")
	}
	return &RefreshRefsResult{
		UpdatedAt: cache.UpdatedAt,
		Count:     len(cache.Items),
		Items:     cache.Items,
	}, nil
}

// RefreshChangedRefs re-fetches authoritative task state for only the supplied
// task GIDs and atomically merges those changes into the local projection.
func (s *Service) RefreshChangedRefs(ctx context.Context, gids []string) (*RefreshChangedRefsResult, error) {
	resolved, err := s.resolveToken()
	if err != nil {
		return nil, err
	}
	cfg, err := s.config().Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read local configuration.")
	}
	if cfg.ActiveProject == nil || cfg.ActiveProject.GID == "" {
		return nil, output.NewError("PROJECT_NOT_CONFIGURED", "No active project is configured. Run project select first.")
	}
	store := s.refsForProject(cfg.ActiveProject)
	cache, err := store.Load()
	if err != nil {
		return nil, output.NewError("REF_CACHE_READ_FAILED", "Could not read the local reference projection.")
	}
	byGID := make(map[string]refcache.Entry, len(cache.Items))
	for _, entry := range cache.Items {
		byGID[entry.GID] = entry
	}
	seen := map[string]bool{}
	result := &RefreshChangedRefsResult{}
	for _, gid := range gids {
		gid = strings.TrimSpace(gid)
		if gid == "" || seen[gid] {
			continue
		}
		seen[gid] = true
		task, taskErr := s.asana().Task(ctx, resolved.Token, gid)
		if taskErr != nil {
			var apiErr *asana.APIError
			if errors.As(taskErr, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
				delete(byGID, gid)
				result.Removed = append(result.Removed, gid)
				continue
			}
			return nil, mapAsanaError(taskErr, "Could not refresh changed work.")
		}
		item := toWorkItem(*task, cfg.TaskTypes, cfg.States)
		byGID[gid] = refcache.Entry{Ref: item.Ref, GID: item.GID, Name: item.Name, Type: item.Type, Status: item.Status, State: item.State, Permalink: item.Permalink, ParentRef: parentRef(item.Parent), ParentGID: parentGID(item.Parent)}
		result.Refreshed = append(result.Refreshed, gid)
	}
	entries := make([]refcache.Entry, 0, len(byGID))
	for _, entry := range byGID {
		entries = append(entries, entry)
	}
	sortRefEntries(entries)
	sort.Strings(result.Refreshed)
	sort.Strings(result.Removed)
	updated, err := store.Replace(entries)
	if err != nil {
		return nil, output.NewError("REF_CACHE_WRITE_FAILED", "Could not save the local reference projection.")
	}
	result.UpdatedAt = updated.UpdatedAt
	return result, nil
}

func sortRefEntries(entries []refcache.Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Ref == entries[j].Ref {
			return entries[i].GID < entries[j].GID
		}
		return entries[i].Ref < entries[j].Ref
	})
}

func (s *Service) ResolveRef(ctx context.Context, ref string) (*ResolveRefResult, error) {
	ref = strings.TrimSpace(ref)
	entry, err := s.refs().Resolve(ref)
	if err != nil {
		if errors.Is(err, refcache.ErrReferenceRequired) {
			return nil, output.NewError("REFERENCE_REQUIRED", "Provide a friendly reference or Asana GID.")
		}
		if errors.Is(err, refcache.ErrReferenceNotFound) {
			return nil, output.NewError("REFERENCE_NOT_FOUND", "No cached reference matched the supplied value. Run refs refresh.")
		}
		if errors.Is(err, refcache.ErrProjectMismatch) {
			return nil, output.NewError("REF_CACHE_PROJECT_MISMATCH", "The friendly-reference cache belongs to a different Asana project. Run refs refresh for the selected project.")
		}
		return nil, output.NewError("REF_CACHE_READ_FAILED", "Could not read local reference cache.")
	}

	resolved, err := s.resolveToken()
	if err != nil {
		return nil, err
	}
	task, err := s.asana().Task(ctx, resolved.Token, entry.GID)
	if err != nil {
		return nil, output.NewErrorWithDetails("STALE_REFERENCE", "The cached reference no longer resolves in Asana. Run refs refresh.", entry)
	}
	if task == nil {
		return nil, output.NewErrorWithDetails("STALE_REFERENCE", "The cached reference resolved to empty work in Asana. Run refs refresh.", entry)
	}
	entry.Name = task.Name
	entry.Permalink = task.Permalink
	return &ResolveRefResult{Entry: *entry}, nil
}

func (s *Service) refs() RefStore {
	if s.Refs != nil {
		return s.Refs
	}
	return refcache.NewStore()
}

func (s *Service) refsForProject(project *config.ProjectConfig) RefStore {
	if s.Refs != nil {
		return s.Refs
	}
	return &refcache.Store{Path: refcache.NewStore().Path, Project: project}
}

func parentRef(parent *TaskParent) string {
	if parent == nil {
		return ""
	}
	return parent.Ref
}

func parentGID(parent *TaskParent) string {
	if parent == nil {
		return ""
	}
	return parent.GID
}
