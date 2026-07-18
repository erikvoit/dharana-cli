package work

import (
	"context"
	"errors"
	"strings"

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
		for _, task := range page.Tasks {
			item := toWorkItem(task, cfg.TaskTypes)
			entries = append(entries, refcache.Entry{
				Ref:       item.Ref,
				GID:       item.GID,
				Name:      item.Name,
				Type:      item.Type,
				Status:    item.Status,
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

	cache, err := s.refs().Replace(entries)
	if err != nil {
		return nil, output.NewError("REF_CACHE_WRITE_FAILED", "Could not save local reference cache.")
	}
	return &RefreshRefsResult{
		UpdatedAt: cache.UpdatedAt,
		Count:     len(cache.Items),
		Items:     cache.Items,
	}, nil
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
