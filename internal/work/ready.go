package work

import (
	"context"
	"sort"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/output"
)

type ReadyWorkOptions struct {
	Types      []string
	EpicRef    string
	Priorities []string
	Components []string
}

type ReadyWorkResult struct {
	Items   []WorkItem      `json:"items"`
	Filters ReadyWorkFilter `json:"filters"`
}

type ReadyWorkFilter struct {
	Types      []string `json:"types,omitempty"`
	EpicRef    string   `json:"epic_ref,omitempty"`
	Priorities []string `json:"priorities,omitempty"`
	Components []string `json:"components,omitempty"`
}

func (s *Service) ReadyWork(ctx context.Context, opts ReadyWorkOptions) (*ReadyWorkResult, error) {
	types := normalizeTypes(opts.Types)
	priorities := normalizeValues(opts.Priorities)
	components := normalizeValues(opts.Components)

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
	if len(priorities) > 0 && cfg.Fields.PriorityGID == "" {
		return nil, output.NewError("PRIORITY_FIELD_NOT_CONFIGURED", "Configure a priority field GID before filtering by priority.")
	}
	if len(components) > 0 && cfg.Fields.ComponentGID == "" {
		return nil, output.NewError("COMPONENT_FIELD_NOT_CONFIGURED", "Configure a component field GID before filtering by component.")
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
		return nil, mapAsanaError(err, "Could not list ready work.")
	}
	items := make([]WorkItem, 0)
	for _, task := range tasks {
		if task.Completed || len(task.Dependencies) > 0 {
			continue
		}
		item := toWorkItem(task, cfg.TaskTypes)
		if !matchesType(item.Type, types) {
			continue
		}
		if epicGID != "" && (item.Parent == nil || item.Parent.GID != epicGID) && item.GID != epicGID {
			continue
		}
		if !taskFieldMatches(task, cfg.Fields.PriorityGID, priorities) {
			continue
		}
		if !taskFieldMatches(task, cfg.Fields.ComponentGID, components) {
			continue
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Type == items[j].Type {
			return items[i].Name < items[j].Name
		}
		return typeOrder(items[i].Type) < typeOrder(items[j].Type)
	})

	return &ReadyWorkResult{
		Items: items,
		Filters: ReadyWorkFilter{
			Types:      types,
			EpicRef:    strings.TrimSpace(opts.EpicRef),
			Priorities: priorities,
			Components: components,
		},
	}, nil
}

func normalizeValues(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			key := strings.ToLower(part)
			if part == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, part)
		}
	}
	return out
}

func taskFieldMatches(task asana.Task, fieldGID string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	if fieldGID == "" {
		return false
	}
	for _, field := range task.CustomFields {
		if field.GID != fieldGID {
			continue
		}
		for _, filter := range filters {
			if customFieldValueMatches(field, filter) {
				return true
			}
		}
	}
	return false
}

func customFieldValueMatches(field asana.CustomField, expected string) bool {
	if expected == "" {
		return false
	}
	if strings.EqualFold(field.DisplayValue, expected) {
		return true
	}
	if field.EnumValue == nil {
		return false
	}
	return strings.EqualFold(field.EnumValue.GID, expected) || strings.EqualFold(field.EnumValue.Name, expected)
}
