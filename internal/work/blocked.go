package work

import (
	"context"
	"sort"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/refcache"
)

type BlockedWorkOptions struct {
	Types   []string
	EpicRef string
}

type BlockedWorkResult struct {
	Items   []BlockedWorkItem `json:"items"`
	Filters BlockedWorkFilter `json:"filters"`
}

type BlockedWorkItem struct {
	Item     WorkItem        `json:"item"`
	Blockers []DependencyRef `json:"blockers"`
}

type BlockedWorkFilter struct {
	Types   []string `json:"types,omitempty"`
	EpicRef string   `json:"epic_ref,omitempty"`
}

func (s *Service) BlockedWork(ctx context.Context, opts BlockedWorkOptions) (*BlockedWorkResult, error) {
	types := normalizeTypes(opts.Types)
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

	var epicGID string
	if strings.TrimSpace(opts.EpicRef) != "" {
		epic, err := s.resolveEpic(ctx, resolved.Token, cfg, opts.EpicRef)
		if err != nil {
			return nil, err
		}
		epicGID = epic.GID
	}

	tasks, err := s.allProjectTasks(ctx, resolved.Token, cfg.ActiveProject.GID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not list blocked work.")
	}
	refs := refIndex(s.refs())

	items := make([]BlockedWorkItem, 0)
	for _, task := range tasks {
		if len(task.Dependencies) == 0 {
			continue
		}
		item := toWorkItem(task, cfg.TaskTypes)
		if !matchesType(item.Type, types) {
			continue
		}
		if epicGID != "" && (item.Parent == nil || item.Parent.GID != epicGID) && item.GID != epicGID {
			continue
		}
		blockers := make([]DependencyRef, 0, len(task.Dependencies))
		for _, dependency := range task.Dependencies {
			blockers = append(blockers, dependencyRefFromSummary(dependency.GID, dependency.Name, refs))
		}
		sort.SliceStable(blockers, func(i, j int) bool {
			if blockers[i].Type == blockers[j].Type {
				return blockers[i].Name < blockers[j].Name
			}
			return typeOrder(blockers[i].Type) < typeOrder(blockers[j].Type)
		})
		items = append(items, BlockedWorkItem{Item: item, Blockers: blockers})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Item.Type == items[j].Item.Type {
			return items[i].Item.Name < items[j].Item.Name
		}
		return typeOrder(items[i].Item.Type) < typeOrder(items[j].Item.Type)
	})

	return &BlockedWorkResult{
		Items: items,
		Filters: BlockedWorkFilter{
			Types:   types,
			EpicRef: strings.TrimSpace(opts.EpicRef),
		},
	}, nil
}

func refIndex(store RefStore) map[string]refcache.Entry {
	out := map[string]refcache.Entry{}
	if store == nil {
		return out
	}
	cache, err := store.Load()
	if err != nil || cache == nil {
		return out
	}
	for _, entry := range cache.Items {
		if entry.GID != "" {
			out[entry.GID] = entry
		}
	}
	return out
}

func dependencyRefFromSummary(gid string, name string, refs map[string]refcache.Entry) DependencyRef {
	entry, ok := refs[gid]
	if ok {
		if name == "" {
			name = entry.Name
		}
		return DependencyRef{
			GID:       gid,
			Ref:       entry.Ref,
			Name:      name,
			Type:      entry.Type,
			Status:    entry.Status,
			Permalink: entry.Permalink,
		}
	}
	return DependencyRef{
		GID:  gid,
		Ref:  gid,
		Name: name,
		Type: "unknown",
	}
}
