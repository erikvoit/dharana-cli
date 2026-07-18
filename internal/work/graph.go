package work

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/erikvoit/dharana-cli/internal/output"
)

type WorkGraphOptions struct {
	EpicRef string
}

type WorkGraphResult struct {
	Nodes   []GraphNode  `json:"nodes"`
	Edges   []GraphEdge  `json:"edges"`
	Cycles  []GraphCycle `json:"cycles,omitempty"`
	Mermaid string       `json:"mermaid"`
	Filters GraphFilter  `json:"filters"`
}

type GraphNode struct {
	GID    string `json:"gid"`
	Ref    string `json:"ref"`
	Type   string `json:"type"`
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
}

type GraphEdge struct {
	FromGID string `json:"from_gid"`
	FromRef string `json:"from_ref"`
	ToGID   string `json:"to_gid"`
	ToRef   string `json:"to_ref"`
}

type GraphCycle struct {
	GIDs []string `json:"gids"`
	Refs []string `json:"refs"`
}

type GraphFilter struct {
	EpicRef string `json:"epic_ref,omitempty"`
}

func (s *Service) WorkGraph(ctx context.Context, opts WorkGraphOptions) (*WorkGraphResult, error) {
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
		return nil, mapAsanaError(err, "Could not build dependency graph.")
	}
	refs := refIndex(s.refs())
	nodesByGID := map[string]GraphNode{}
	edgesByKey := map[string]GraphEdge{}

	for _, task := range tasks {
		item := toWorkItem(task, cfg.TaskTypes)
		if epicGID != "" && (item.Parent == nil || item.Parent.GID != epicGID) && item.GID != epicGID {
			continue
		}
		blockedNode := graphNodeFromWorkItem(item)
		nodesByGID[blockedNode.GID] = blockedNode
		for _, dependency := range task.Dependencies {
			blocker := dependencyRefFromSummary(dependency.GID, dependency.Name, refs)
			blockerNode := graphNodeFromDependency(blocker)
			nodesByGID[blockerNode.GID] = blockerNode
			key := blockerNode.GID + "->" + blockedNode.GID
			edgesByKey[key] = GraphEdge{
				FromGID: blockerNode.GID,
				FromRef: blockerNode.Ref,
				ToGID:   blockedNode.GID,
				ToRef:   blockedNode.Ref,
			}
		}
	}

	nodes := sortedGraphNodes(nodesByGID)
	edges := sortedGraphEdges(edgesByKey)
	result := &WorkGraphResult{
		Nodes:  nodes,
		Edges:  edges,
		Cycles: detectGraphCycles(nodes, edges),
		Filters: GraphFilter{
			EpicRef: strings.TrimSpace(opts.EpicRef),
		},
	}
	result.Mermaid = FormatMermaidGraph(result)
	return result, nil
}

func graphNodeFromWorkItem(item WorkItem) GraphNode {
	return GraphNode{
		GID:    item.GID,
		Ref:    item.Ref,
		Type:   item.Type,
		Name:   item.Name,
		Status: item.Status,
	}
}

func graphNodeFromDependency(dep DependencyRef) GraphNode {
	return GraphNode{
		GID:    dep.GID,
		Ref:    dep.Ref,
		Type:   dep.Type,
		Name:   dep.Name,
		Status: dep.Status,
	}
}

func sortedGraphNodes(nodes map[string]GraphNode) []GraphNode {
	out := make([]GraphNode, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type == out[j].Type {
			if out[i].Name == out[j].Name {
				return out[i].GID < out[j].GID
			}
			return out[i].Name < out[j].Name
		}
		return typeOrder(out[i].Type) < typeOrder(out[j].Type)
	})
	return out
}

func sortedGraphEdges(edges map[string]GraphEdge) []GraphEdge {
	out := make([]GraphEdge, 0, len(edges))
	for _, edge := range edges {
		out = append(out, edge)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].FromRef == out[j].FromRef {
			return out[i].ToRef < out[j].ToRef
		}
		return out[i].FromRef < out[j].FromRef
	})
	return out
}

func detectGraphCycles(nodes []GraphNode, edges []GraphEdge) []GraphCycle {
	refs := map[string]string{}
	for _, node := range nodes {
		refs[node.GID] = node.Ref
	}
	adjacency := map[string][]string{}
	for _, edge := range edges {
		adjacency[edge.FromGID] = append(adjacency[edge.FromGID], edge.ToGID)
	}
	for gid := range adjacency {
		sort.Strings(adjacency[gid])
	}

	state := map[string]int{}
	var stack []string
	seen := map[string]bool{}
	var cycles []GraphCycle
	var visit func(string)
	visit = func(gid string) {
		state[gid] = 1
		stack = append(stack, gid)
		for _, next := range adjacency[gid] {
			if state[next] == 0 {
				visit(next)
				continue
			}
			if state[next] != 1 {
				continue
			}
			start := indexOf(stack, next)
			if start < 0 {
				continue
			}
			cycleGIDs := append([]string{}, stack[start:]...)
			cycleGIDs = append(cycleGIDs, next)
			signature := cycleSignature(cycleGIDs)
			if seen[signature] {
				continue
			}
			seen[signature] = true
			cycleRefs := make([]string, 0, len(cycleGIDs))
			for _, cycleGID := range cycleGIDs {
				ref := refs[cycleGID]
				if ref == "" {
					ref = cycleGID
				}
				cycleRefs = append(cycleRefs, ref)
			}
			cycles = append(cycles, GraphCycle{GIDs: cycleGIDs, Refs: cycleRefs})
		}
		stack = stack[:len(stack)-1]
		state[gid] = 2
	}
	for _, node := range nodes {
		if state[node.GID] == 0 {
			visit(node.GID)
		}
	}
	return cycles
}

func indexOf(values []string, target string) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}

func cycleSignature(gids []string) string {
	if len(gids) <= 1 {
		return strings.Join(gids, "->")
	}
	path := gids[:len(gids)-1]
	minIdx := 0
	for i := 1; i < len(path); i++ {
		if path[i] < path[minIdx] {
			minIdx = i
		}
	}
	rotated := make([]string, 0, len(path)+1)
	rotated = append(rotated, path[minIdx:]...)
	rotated = append(rotated, path[:minIdx]...)
	rotated = append(rotated, rotated[0])
	return strings.Join(rotated, "->")
}

func FormatMermaidGraph(result *WorkGraphResult) string {
	if result == nil {
		return "flowchart LR\n"
	}
	nodeIDs := map[string]string{}
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	for i, node := range result.Nodes {
		id := fmt.Sprintf("N%d", i+1)
		nodeIDs[node.GID] = id
		_, _ = fmt.Fprintf(&b, "    %s[%q]\n", id, graphNodeLabel(node))
	}
	for _, edge := range result.Edges {
		_, _ = fmt.Fprintf(&b, "    %s --> %s\n", nodeIDs[edge.FromGID], nodeIDs[edge.ToGID])
	}
	for _, cycle := range result.Cycles {
		_, _ = fmt.Fprintf(&b, "    %%%% Cycle detected: %s\n", strings.Join(cycle.Refs, " -> "))
	}
	return b.String()
}

func graphNodeLabel(node GraphNode) string {
	ref := node.Ref
	if ref == "" {
		ref = node.GID
	}
	name := node.Name
	if name == "" {
		name = node.GID
	}
	typeName := node.Type
	if typeName == "" {
		typeName = "unknown"
	}
	return strings.ToUpper(typeName) + ": " + name + "\n" + ref
}
