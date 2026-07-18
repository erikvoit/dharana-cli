package plan

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/work"
)

var nonIDCharacters = regexp.MustCompile(`[^a-z0-9]+`)

func (s *Service) Export(ctx context.Context, epicRef string) (*ExportResult, error) {
	tree, err := s.work().WorkTree(ctx, work.WorkTreeOptions{EpicRef: epicRef})
	if err != nil {
		return nil, err
	}
	if len(tree.Epics) == 0 {
		return nil, output.NewError("EPIC_NOT_FOUND", "No epic matched the export reference.")
	}
	if len(tree.Epics) > 1 {
		return nil, output.NewError("AMBIGUOUS_EPIC", "More than one epic was returned for export.")
	}
	cfg, err := s.config().Load()
	if err != nil {
		return nil, output.NewError("CONFIG_READ_FAILED", "Could not read context metadata for plan export.")
	}

	root := tree.Epics[0]
	manifestID := uniqueID(slug(root.Item.Name), map[string]bool{})
	manifest := &Manifest{APIVersion: APIVersion, Kind: Kind, Metadata: Metadata{ID: manifestID}, Spec: Spec{RemovalPolicy: "preserve"}}
	if cfg != nil && cfg.ActiveContext != "" {
		manifest.Metadata.Context = cfg.ActiveContext
	} else {
		manifest.Spec.Project = tree.Project.GID
	}
	usedIDs := map[string]bool{}
	manifest.Spec.Epic = Epic{ID: uniqueID("epic", usedIDs), Name: root.Item.Name}

	type exported struct {
		node Node
		gid  string
	}
	records := []exported{}
	gidToID := map[string]string{root.Item.GID: manifest.Spec.Epic.ID}

	rootProps, err := s.work().UpdateWork(ctx, work.UpdateWorkOptions{Ref: root.Item.GID, DryRun: true})
	if err != nil {
		return nil, err
	}
	if rootProps.Before.Notes != "" {
		manifest.Spec.Epic.Notes = stringPointer(rootProps.Before.Notes)
	}
	records = append(records, exported{node: Node{ID: manifest.Spec.Epic.ID, Type: "epic", Name: root.Item.Name, Notes: manifest.Spec.Epic.Notes}, gid: root.Item.GID})

	for _, child := range root.Children {
		id := uniqueID(slug(child.Item.Name), usedIDs)
		gidToID[child.Item.GID] = id
		properties, err := s.work().UpdateWork(ctx, work.UpdateWorkOptions{Ref: child.Item.GID, DryRun: true})
		if err != nil {
			return nil, err
		}
		completed := properties.Before.Completed
		item := Work{ID: id, Type: child.Item.Type, Name: child.Item.Name, Completed: &completed}
		assignExportedProperties(&item.Notes, &item.Assignee, &item.DueOn, &item.Priority, &item.Component, properties.Before)
		if item.Type == "spike" && item.Notes != nil {
			item.Timebox, item.Notes = parseSpikeManagedNotes(*item.Notes)
		}
		manifest.Spec.Work = append(manifest.Spec.Work, item)
		records = append(records, exported{node: nodeFromWork(manifest.Spec.Epic.ID, item), gid: child.Item.GID})
		workIndex := len(manifest.Spec.Work) - 1
		for _, leaf := range child.Children {
			taskID := uniqueID(slug(leaf.Item.Name), usedIDs)
			gidToID[leaf.Item.GID] = taskID
			taskProperties, err := s.work().UpdateWork(ctx, work.UpdateWorkOptions{Ref: leaf.Item.GID, DryRun: true})
			if err != nil {
				return nil, err
			}
			taskCompleted := taskProperties.Before.Completed
			task := Task{ID: taskID, Name: leaf.Item.Name, Completed: &taskCompleted}
			assignExportedProperties(&task.Notes, &task.Assignee, &task.DueOn, nil, nil, taskProperties.Before)
			if task.Notes != nil {
				task.Estimate, task.Notes = parseTaskManagedNotes(*task.Notes)
			}
			manifest.Spec.Work[workIndex].Tasks = append(manifest.Spec.Work[workIndex].Tasks, task)
			records = append(records, exported{node: nodeFromTask(id, task), gid: leaf.Item.GID})
		}
	}

	var findings []Finding
	for i := range records {
		detail, err := s.work().GetWork(ctx, records[i].gid)
		if err != nil {
			return nil, err
		}
		for _, blocker := range detail.Item.Dependencies.Blockers {
			logicalID, ok := gidToID[blocker.GID]
			if !ok {
				findings = append(findings, finding("EXTERNAL_DEPENDENCY_PRESERVED", "warning", nodePath(manifest, records[i].node.ID)+".blockedBy", "A blocker outside the exported epic was not added to the manifest: "+blocker.GID+".", "Manage the external dependency separately or add an explicit supported reference in a future schema."))
				continue
			}
			records[i].node.BlockedBy = append(records[i].node.BlockedBy, logicalID)
		}
		records[i].node.BlockedBy = normalizedIDs(records[i].node.BlockedBy)
		setManifestBlockers(manifest, records[i].node.ID, records[i].node.BlockedBy)
	}
	manifest.Normalize()

	bindings := NewBindingState(manifest.Metadata.ID, tree.Project.GID, tree.Project.WorkspaceGID)
	bindings.Context = manifest.Metadata.Context
	for _, record := range records {
		bindings.Bind(record.node, record.gid)
	}
	bindings.ManifestDigest = manifest.Digest()
	bindings.LastAppliedAt = time.Now().UTC().Format(time.RFC3339)
	store := s.bindingStore()
	release, err := store.Acquire(manifest.Metadata.ID, tree.Project.GID)
	if err != nil {
		return nil, output.NewErrorWithDetails("BINDING_LOCK_FAILED", "Could not acquire the project-scoped plan binding lock.", err.Error())
	}
	defer release()
	if err := store.Save(bindings); err != nil {
		return nil, output.NewError("BINDING_WRITE_FAILED", "Could not save exported plan bindings.")
	}
	sortFindings(findings)
	return &ExportResult{Manifest: manifest, Bindings: bindings, Findings: findings, BindingsPath: store.path(manifest.Metadata.ID, tree.Project.GID)}, nil
}

func assignExportedProperties(notes, assignee, dueOn, priority, component **string, properties work.WorkProperties) {
	if notes != nil && properties.Notes != "" {
		*notes = stringPointer(properties.Notes)
	}
	if assignee != nil && properties.Assignee != nil {
		identity := properties.Assignee.Email
		if identity == "" {
			identity = properties.Assignee.GID
		}
		*assignee = stringPointer(identity)
	}
	if dueOn != nil && properties.DueOn != "" {
		*dueOn = stringPointer(properties.DueOn)
	}
	if priority != nil && properties.Priority != "" {
		*priority = stringPointer(properties.Priority)
	}
	if component != nil && properties.Component != "" {
		*component = stringPointer(properties.Component)
	}
}

func nodeFromWork(epicID string, item Work) Node {
	return Node{ID: item.ID, Type: item.Type, Name: item.Name, ParentID: epicID, Notes: item.Notes, Assignee: item.Assignee, DueOn: item.DueOn, Priority: item.Priority, Component: item.Component, Completed: item.Completed, BlockedBy: item.BlockedBy}
}

func nodeFromTask(parentID string, item Task) Node {
	return Node{ID: item.ID, Type: "task", Name: item.Name, ParentID: parentID, Notes: item.Notes, Assignee: item.Assignee, DueOn: item.DueOn, Estimate: item.Estimate, Completed: item.Completed, BlockedBy: item.BlockedBy}
}

func setManifestBlockers(manifest *Manifest, id string, blockers []string) {
	for i := range manifest.Spec.Work {
		if manifest.Spec.Work[i].ID == id {
			manifest.Spec.Work[i].BlockedBy = append([]string(nil), blockers...)
			return
		}
		for j := range manifest.Spec.Work[i].Tasks {
			if manifest.Spec.Work[i].Tasks[j].ID == id {
				manifest.Spec.Work[i].Tasks[j].BlockedBy = append([]string(nil), blockers...)
				return
			}
		}
	}
}

func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = nonIDCharacters.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		value = "work-" + value
	}
	return strings.Trim(value, "-")
}

func uniqueID(base string, used map[string]bool) string {
	if base == "" {
		base = "work"
	}
	if !used[base] {
		used[base] = true
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !used[candidate] {
			used[candidate] = true
			return candidate
		}
	}
}

func parseSpikeManagedNotes(notes string) (*string, *string) {
	lines := strings.Split(notes, "\n")
	if len(lines) < 6 || !strings.HasPrefix(lines[0], "Timebox: ") || lines[1] != "" || lines[2] != "Expected outcomes:" || lines[3] != "- Root-cause analysis" || lines[4] != "- Technical recommendation" || lines[5] != "- Follow-up story or bug, if needed" {
		return nil, optionalStringPointer(notes)
	}
	timebox := strings.TrimSpace(strings.TrimPrefix(lines[0], "Timebox: "))
	remainder := ""
	if len(lines) > 6 {
		remainder = strings.TrimSpace(strings.Join(lines[6:], "\n"))
	}
	return optionalStringPointer(timebox), optionalStringPointer(remainder)
}

func parseTaskManagedNotes(notes string) (*string, *string) {
	lines := strings.Split(notes, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "Estimate: ") {
		return nil, optionalStringPointer(notes)
	}
	estimate := strings.TrimSpace(strings.TrimPrefix(lines[0], "Estimate: "))
	remainder := ""
	if len(lines) > 1 {
		remainder = strings.TrimSpace(strings.Join(lines[1:], "\n"))
	}
	return optionalStringPointer(estimate), optionalStringPointer(remainder)
}
