package work

import (
	"context"
	"sort"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/output"
)

type ListWorkOptions struct {
	Types   []string
	Status  string
	EpicRef string
	Limit   int
	Offset  string
}

type WorkItem struct {
	GID       string      `json:"gid"`
	Ref       string      `json:"ref"`
	Name      string      `json:"name"`
	Type      string      `json:"type"`
	Status    string      `json:"status"`
	Parent    *TaskParent `json:"parent,omitempty"`
	Permalink string      `json:"permalink_url,omitempty"`
}

type ListWorkResult struct {
	Items      []WorkItem `json:"items"`
	Limit      int        `json:"limit"`
	NextOffset string     `json:"next_offset,omitempty"`
	Filters    ListFilter `json:"filters"`
}

type ListFilter struct {
	Types   []string `json:"types,omitempty"`
	Status  string   `json:"status,omitempty"`
	EpicRef string   `json:"epic_ref,omitempty"`
}

func (s *Service) ListWork(ctx context.Context, opts ListWorkOptions) (*ListWorkResult, error) {
	if opts.Limit <= 0 || opts.Limit > 100 {
		opts.Limit = 50
	}
	opts.Status = normalizeStatus(opts.Status)
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

	page, err := s.asana().ProjectTasks(ctx, resolved.Token, cfg.ActiveProject.GID, opts.Limit, opts.Offset)
	if err != nil {
		return nil, mapAsanaError(err, "Could not list active-project work.")
	}

	items := make([]WorkItem, 0, len(page.Tasks))
	for _, task := range page.Tasks {
		item := toWorkItem(task, cfg.TaskTypes)
		if !matchesType(item.Type, types) {
			continue
		}
		if !matchesStatus(item.Status, opts.Status) {
			continue
		}
		if epicGID != "" && (item.Parent == nil || item.Parent.GID != epicGID) && item.GID != epicGID {
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

	return &ListWorkResult{
		Items:      items,
		Limit:      opts.Limit,
		NextOffset: page.NextOffset,
		Filters: ListFilter{
			Types:   types,
			Status:  opts.Status,
			EpicRef: strings.TrimSpace(opts.EpicRef),
		},
	}, nil
}

func toWorkItem(task asana.Task, types config.TaskTypes) WorkItem {
	workType := taskType(task, types)
	item := WorkItem{
		GID:       task.GID,
		Ref:       strings.ToUpper(workType) + ":" + task.Name,
		Name:      task.Name,
		Type:      workType,
		Status:    statusForTask(task),
		Permalink: task.Permalink,
	}
	if task.Parent != nil {
		item.Parent = &TaskParent{
			GID:  task.Parent.GID,
			Ref:  "TASK:" + task.Parent.Name,
			Name: task.Parent.Name,
		}
	}
	return item
}

func taskType(task asana.Task, types config.TaskTypes) string {
	if types.FieldGID != "" {
		for _, field := range task.CustomFields {
			if field.GID != types.FieldGID {
				continue
			}
			switch {
			case customFieldMatches(field, types.Epic):
				return "epic"
			case customFieldMatches(field, types.Story):
				return "story"
			case customFieldMatches(field, types.Bug):
				return "bug"
			case customFieldMatches(field, types.Spike):
				return "spike"
			}
		}
	}
	if task.Parent != nil {
		return "task"
	}
	return "unknown"
}

func customFieldMatches(field asana.CustomField, expected string) bool {
	if expected == "" {
		return false
	}
	if field.DisplayValue == expected {
		return true
	}
	if field.EnumValue == nil {
		return false
	}
	return field.EnumValue.GID == expected || field.EnumValue.Name == expected
}

func statusForTask(task asana.Task) string {
	if task.Completed {
		return "completed"
	}
	return "incomplete"
}

func normalizeTypes(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.ToLower(strings.TrimSpace(part))
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}

func normalizeStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "", "all":
		return "all"
	case "complete":
		return "completed"
	default:
		return value
	}
}

func matchesType(value string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		if value == filter {
			return true
		}
	}
	return false
}

func matchesStatus(value string, filter string) bool {
	if filter == "" || filter == "all" {
		return true
	}
	return value == filter
}

func typeOrder(value string) int {
	switch value {
	case "epic":
		return 0
	case "story":
		return 1
	case "bug":
		return 2
	case "spike":
		return 3
	case "task":
		return 4
	default:
		return 9
	}
}
