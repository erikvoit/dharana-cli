package work

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/asana"
	"github.com/erikvoit/dharana-cli/internal/output"
)

type WorkTreeOptions struct {
	EpicRef string
}

type WorkTreeResult struct {
	Project ProjectSummary `json:"project"`
	Epics   []TreeNode     `json:"epics"`
	Issues  []TreeIssue    `json:"issues,omitempty"`
}

type ProjectSummary struct {
	GID           string `json:"gid"`
	Name          string `json:"name"`
	WorkspaceGID  string `json:"workspace_gid,omitempty"`
	WorkspaceName string `json:"workspace_name,omitempty"`
}

type TreeNode struct {
	Item     WorkItem   `json:"item"`
	Children []TreeNode `json:"children,omitempty"`
}

type TreeIssue struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	GID       string `json:"gid,omitempty"`
	Ref       string `json:"ref,omitempty"`
	Name      string `json:"name,omitempty"`
	Type      string `json:"type,omitempty"`
	ParentGID string `json:"parent_gid,omitempty"`
}

func (s *Service) WorkTree(ctx context.Context, opts WorkTreeOptions) (*WorkTreeResult, error) {
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

	var scopedEpic *asana.Task
	if strings.TrimSpace(opts.EpicRef) != "" {
		scopedEpic, err = s.resolveEpic(ctx, resolved.Token, cfg, opts.EpicRef)
		if err != nil {
			return nil, err
		}
	}

	tasks, err := s.allProjectTasks(ctx, resolved.Token, cfg.ActiveProject.GID)
	if err != nil {
		return nil, mapAsanaError(err, "Could not read active-project work tree.")
	}

	result := &WorkTreeResult{
		Project: ProjectSummary{
			GID:           cfg.ActiveProject.GID,
			Name:          cfg.ActiveProject.Name,
			WorkspaceGID:  cfg.ActiveProject.WorkspaceGID,
			WorkspaceName: cfg.ActiveProject.WorkspaceName,
		},
	}

	epics := map[string]*TreeNode{}
	for _, task := range tasks {
		item := toWorkItem(task, cfg.TaskTypes)
		if item.Type != "epic" {
			continue
		}
		if scopedEpic != nil && item.GID != scopedEpic.GID {
			continue
		}
		epics[item.GID] = &TreeNode{Item: item}
	}
	if scopedEpic != nil {
		if _, ok := epics[scopedEpic.GID]; !ok {
			epics[scopedEpic.GID] = &TreeNode{Item: epicTreeItem(scopedEpic)}
		}
	}

	for _, task := range tasks {
		item := toWorkItem(task, cfg.TaskTypes)
		if item.Type != "story" && item.Type != "bug" && item.Type != "spike" {
			continue
		}
		if item.Parent == nil || item.Parent.GID == "" {
			result.Issues = append(result.Issues, treeIssue("MALFORMED_PARENT", "First-level work item is missing an epic parent.", item, ""))
			continue
		}
		epic := epics[item.Parent.GID]
		if epic == nil {
			if scopedEpic == nil {
				result.Issues = append(result.Issues, treeIssue("MISSING_PARENT", "First-level work item references an epic not present in the tree.", item, item.Parent.GID))
			}
			continue
		}
		appendChildUnique(epic, TreeNode{Item: item})
	}

	for _, epic := range epics {
		children, err := s.allSubtasks(ctx, resolved.Token, epic.Item.GID)
		if err != nil {
			return nil, mapAsanaError(err, "Could not read epic subtasks.")
		}
		for _, childTask := range children {
			child := toWorkItem(childTask, cfg.TaskTypes)
			if child.Type != "story" && child.Type != "bug" && child.Type != "spike" {
				result.Issues = append(result.Issues, treeIssue("MALFORMED_PARENT", "Epic child is not configured as a story, bug, or spike.", child, epic.Item.GID))
				continue
			}
			if child.Parent == nil || child.Parent.GID != epic.Item.GID {
				result.Issues = append(result.Issues, treeIssue("MALFORMED_PARENT", "Epic child does not reference the expected epic parent.", child, epic.Item.GID))
			}
			appendChildUnique(epic, TreeNode{Item: child})
		}

		for i := range epic.Children {
			if epic.Children[i].Item.Type != "story" && epic.Children[i].Item.Type != "bug" && epic.Children[i].Item.Type != "spike" {
				continue
			}
			leafTasks, err := s.allSubtasks(ctx, resolved.Token, epic.Children[i].Item.GID)
			if err != nil {
				return nil, mapAsanaError(err, "Could not read implementation tasks.")
			}
			for _, leafTask := range leafTasks {
				leaf := toWorkItem(leafTask, cfg.TaskTypes)
				if leaf.Parent == nil || leaf.Parent.GID != epic.Children[i].Item.GID {
					result.Issues = append(result.Issues, treeIssue("MALFORMED_PARENT", "Implementation task does not reference the expected parent.", leaf, epic.Children[i].Item.GID))
				}
				if leaf.Type != "task" {
					result.Issues = append(result.Issues, treeIssue("MALFORMED_PARENT", "Nested implementation work is not configured as a task.", leaf, epic.Children[i].Item.GID))
				}
				appendChildUnique(&epic.Children[i], TreeNode{Item: leaf})
			}
		}
	}

	for _, task := range tasks {
		item := toWorkItem(task, cfg.TaskTypes)
		if item.Type != "task" {
			continue
		}
		if item.Parent == nil || !treeContains(epics, item.Parent.GID) {
			result.Issues = append(result.Issues, treeIssue("MISSING_PARENT", "Implementation task references a parent not present in the tree.", item, parentGID(item.Parent)))
		}
	}

	result.Epics = sortedTreeNodes(epics)
	return result, nil
}

func (s *Service) allProjectTasks(ctx context.Context, token string, projectGID string) ([]asana.Task, error) {
	var tasks []asana.Task
	var offset string
	for {
		page, err := s.asana().ProjectTasks(ctx, token, projectGID, 100, offset)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, page.Tasks...)
		if page.NextOffset == "" {
			return tasks, nil
		}
		offset = page.NextOffset
	}
}

func (s *Service) allSubtasks(ctx context.Context, token string, taskGID string) ([]asana.Task, error) {
	var tasks []asana.Task
	var offset string
	for {
		page, err := s.asana().Subtasks(ctx, token, taskGID, 100, offset)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, page.Tasks...)
		if page.NextOffset == "" {
			return tasks, nil
		}
		offset = page.NextOffset
	}
}

func epicTreeItem(task *asana.Task) WorkItem {
	return WorkItem{
		GID:       task.GID,
		Ref:       "EPIC:" + task.Name,
		Name:      task.Name,
		Type:      "epic",
		Status:    statusForTask(*task),
		Permalink: task.Permalink,
	}
}

func appendChildUnique(parent *TreeNode, child TreeNode) {
	for _, existing := range parent.Children {
		if existing.Item.GID == child.Item.GID {
			return
		}
	}
	parent.Children = append(parent.Children, child)
	sortTreeNodes(parent.Children)
}

func sortedTreeNodes(nodes map[string]*TreeNode) []TreeNode {
	out := make([]TreeNode, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, *node)
	}
	sortTreeNodes(out)
	return out
}

func sortTreeNodes(nodes []TreeNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if typeOrder(nodes[i].Item.Type) == typeOrder(nodes[j].Item.Type) {
			return nodes[i].Item.Name < nodes[j].Item.Name
		}
		return typeOrder(nodes[i].Item.Type) < typeOrder(nodes[j].Item.Type)
	})
}

func treeContains(epics map[string]*TreeNode, gid string) bool {
	if gid == "" {
		return false
	}
	for _, epic := range epics {
		if epic.Item.GID == gid || nodeContains(epic, gid) {
			return true
		}
	}
	return false
}

func nodeContains(node *TreeNode, gid string) bool {
	for i := range node.Children {
		if node.Children[i].Item.GID == gid {
			return true
		}
		if nodeContains(&node.Children[i], gid) {
			return true
		}
	}
	return false
}

func treeIssue(code, message string, item WorkItem, expectedParentGID string) TreeIssue {
	parent := expectedParentGID
	if parent == "" {
		parent = parentGID(item.Parent)
	}
	return TreeIssue{
		Code:      code,
		Message:   message,
		GID:       item.GID,
		Ref:       item.Ref,
		Name:      item.Name,
		Type:      item.Type,
		ParentGID: parent,
	}
}

func FormatWorkTree(result *WorkTreeResult) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "Project: %s (%s)\n", result.Project.Name, result.Project.GID)
	for _, epic := range result.Epics {
		writeTreeNode(&b, epic, "")
	}
	if len(result.Issues) > 0 {
		_, _ = fmt.Fprintf(&b, "\nIssues:\n")
		for _, issue := range result.Issues {
			_, _ = fmt.Fprintf(&b, "- %s: %s (%s)\n", issue.Code, issue.Message, issue.Ref)
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func writeTreeNode(b *strings.Builder, node TreeNode, indent string) {
	_, _ = fmt.Fprintf(b, "%s- %s [%s] %s\n", indent, node.Item.Ref, node.Item.Status, node.Item.GID)
	for _, child := range node.Children {
		writeTreeNode(b, child, indent+"  ")
	}
}
