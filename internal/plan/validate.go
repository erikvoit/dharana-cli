package plan

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

var logicalIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var effortPattern = regexp.MustCompile(`^[1-9][0-9]*(m|h|d|w)$`)

type Finding struct {
	Code        string `json:"code"`
	Severity    string `json:"severity"`
	Path        string `json:"path"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

type ValidationResult struct {
	Valid          bool      `json:"valid"`
	APIVersion     string    `json:"api_version,omitempty"`
	ManifestID     string    `json:"manifest_id,omitempty"`
	ManifestDigest string    `json:"manifest_digest,omitempty"`
	LocalFindings  []Finding `json:"local_findings"`
	RemoteFindings []Finding `json:"remote_findings,omitempty"`
}

func ValidateLocal(manifest *Manifest) ValidationResult {
	result := ValidationResult{LocalFindings: []Finding{}}
	if manifest == nil {
		result.LocalFindings = append(result.LocalFindings, finding("PLAN_REQUIRED", "error", "$", "Plan manifest is required.", "Provide a valid EpicPlan manifest."))
		return result
	}
	manifest.Normalize()
	result.APIVersion = manifest.APIVersion
	result.ManifestID = manifest.Metadata.ID
	result.ManifestDigest = manifest.Digest()
	if manifest.APIVersion != APIVersion {
		result.LocalFindings = append(result.LocalFindings, finding("UNSUPPORTED_PLAN_VERSION", "error", "$.apiVersion", "Unsupported plan API version.", "Use "+APIVersion+"."))
	}
	if manifest.Kind != Kind {
		result.LocalFindings = append(result.LocalFindings, finding("INVALID_PLAN_KIND", "error", "$.kind", "Plan kind must be EpicPlan.", "Set kind to "+Kind+"."))
	}
	validateID(&result, manifest.Metadata.ID, "$.metadata.id", "manifest")
	if manifest.Metadata.Context == "" && manifest.Spec.Project == "" {
		result.LocalFindings = append(result.LocalFindings, finding("PLAN_TARGET_REQUIRED", "error", "$.metadata.context", "Plan must name a context or explicit project.", "Set metadata.context or spec.project."))
	}
	if manifest.Metadata.Context != "" && manifest.Spec.Project != "" {
		result.LocalFindings = append(result.LocalFindings, finding("PLAN_TARGET_AMBIGUOUS", "error", "$.spec.project", "Plan must not declare both a named context and an explicit project.", "Keep metadata.context or spec.project, but not both."))
	}
	if manifest.Spec.RemovalPolicy != "preserve" && manifest.Spec.RemovalPolicy != "complete" {
		result.LocalFindings = append(result.LocalFindings, finding("INVALID_REMOVAL_POLICY", "error", "$.spec.removalPolicy", "Removal policy must be preserve or complete.", "Use preserve unless completion of removed managed work is intentional."))
	}
	validateID(&result, manifest.Spec.Epic.ID, "$.spec.epic.id", "epic")
	if manifest.Spec.Epic.Name == "" {
		result.LocalFindings = append(result.LocalFindings, finding("EPIC_NAME_REQUIRED", "error", "$.spec.epic.name", "Epic name is required.", "Provide a non-empty epic name."))
	}

	nodes := manifest.Nodes()
	seen := map[string]string{}
	for _, node := range nodes {
		path := nodePath(manifest, node.ID)
		validateID(&result, node.ID, path+".id", node.Type)
		if prior, ok := seen[node.ID]; ok {
			result.LocalFindings = append(result.LocalFindings, finding("DUPLICATE_LOGICAL_ID", "error", path+".id", "Logical ID is already used at "+prior+".", "Use globally unique logical IDs within the EpicPlan."))
		} else {
			seen[node.ID] = path
		}
		if node.Name == "" {
			result.LocalFindings = append(result.LocalFindings, finding("WORK_NAME_REQUIRED", "error", path+".name", "Work name is required.", "Provide a non-empty name."))
		}
		if node.Notes != nil && node.Description != nil {
			result.LocalFindings = append(result.LocalFindings, finding("DESCRIPTION_NOTES_CONFLICT", "error", path+".description", "A node cannot manage both Markdown description and plain notes.", "Keep description or notes, but not both."))
		}
		if node.Description != nil {
			if err := node.Description.Validate(); err != nil {
				result.LocalFindings = append(result.LocalFindings, finding("INVALID_MARKDOWN_DESCRIPTION", "error", path+".description", err.Error(), "Use format markdown and remove raw HTML, images, or unsafe links."))
			}
		}
		if node.Type != "epic" && node.Type != "story" && node.Type != "bug" && node.Type != "spike" && node.Type != "task" {
			result.LocalFindings = append(result.LocalFindings, finding("INVALID_WORK_TYPE", "error", path+".type", "Work type must be story, bug, or spike; nested tasks are implicit task type.", "Use a supported Dharana work type."))
		}
		if node.DueOn != nil && strings.TrimSpace(*node.DueOn) != "" {
			if _, err := time.Parse("2006-01-02", strings.TrimSpace(*node.DueOn)); err != nil {
				result.LocalFindings = append(result.LocalFindings, finding("INVALID_DUE_ON", "error", path+".dueOn", "Due date must use YYYY-MM-DD.", "Provide an ISO calendar date."))
			}
		}
		if node.Timebox != nil && strings.TrimSpace(*node.Timebox) != "" && !effortPattern.MatchString(strings.TrimSpace(*node.Timebox)) {
			result.LocalFindings = append(result.LocalFindings, finding("INVALID_TIMEBOX", "error", path+".timebox", "Timebox must be a positive integer followed by m, h, d, or w.", "Use a value such as 30m, 4h, or 2d."))
		}
		if node.Estimate != nil && strings.TrimSpace(*node.Estimate) != "" && !effortPattern.MatchString(strings.TrimSpace(*node.Estimate)) {
			result.LocalFindings = append(result.LocalFindings, finding("INVALID_ESTIMATE", "error", path+".estimate", "Estimate must be a positive integer followed by m, h, d, or w.", "Use a value such as 30m, 4h, or 2d."))
		}
	}

	for _, node := range nodes {
		for _, blocker := range node.BlockedBy {
			path := nodePath(manifest, node.ID) + ".blockedBy"
			if blocker == node.ID {
				result.LocalFindings = append(result.LocalFindings, finding("SELF_DEPENDENCY", "error", path, "Work cannot depend on itself.", "Remove the self-reference."))
				continue
			}
			if _, ok := seen[blocker]; !ok {
				result.LocalFindings = append(result.LocalFindings, finding("DEPENDENCY_TARGET_NOT_FOUND", "error", path, "Dependency target "+blocker+" does not exist in the plan.", "Add the target or remove the dependency."))
			}
		}
	}
	for _, cycle := range dependencyCycles(nodes) {
		result.LocalFindings = append(result.LocalFindings, finding("DEPENDENCY_CYCLE", "error", "$.spec.work", "Dependency cycle detected: "+strings.Join(cycle, " -> ")+".", "Remove at least one edge from the cycle."))
	}
	sortFindings(result.LocalFindings)
	result.Valid = !hasErrors(result.LocalFindings)
	return result
}

func validateID(result *ValidationResult, value, path, kind string) {
	if value == "" {
		result.LocalFindings = append(result.LocalFindings, finding("LOGICAL_ID_REQUIRED", "error", path, "A logical ID is required for the "+kind+".", "Use a lowercase identifier such as payment-recovery."))
		return
	}
	if !logicalIDPattern.MatchString(value) {
		result.LocalFindings = append(result.LocalFindings, finding("INVALID_LOGICAL_ID", "error", path, "Logical IDs must start with a lowercase letter and contain only lowercase letters, numbers, and hyphens.", "Use a value such as payment-recovery."))
	}
}

func finding(code, severity, path, message, remediation string) Finding {
	return Finding{Code: code, Severity: severity, Path: path, Message: message, Remediation: remediation}
}

func hasErrors(findings []Finding) bool {
	for _, value := range findings {
		if value.Severity == "error" {
			return true
		}
	}
	return false
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Path == findings[j].Path {
			return findings[i].Code < findings[j].Code
		}
		return findings[i].Path < findings[j].Path
	})
}

func nodePath(manifest *Manifest, id string) string {
	if manifest != nil && manifest.Spec.Epic.ID == id {
		return "$.spec.epic"
	}
	if manifest != nil {
		for i, item := range manifest.Spec.Work {
			if item.ID == id {
				return "$.spec.work[" + itoa(i) + "]"
			}
			for j, task := range item.Tasks {
				if task.ID == id {
					return "$.spec.work[" + itoa(i) + "].tasks[" + itoa(j) + "]"
				}
			}
		}
	}
	return "$.spec.work"
}

func dependencyCycles(nodes []Node) [][]string {
	edges := map[string][]string{}
	for _, node := range nodes {
		edges[node.ID] = append(edges[node.ID], node.BlockedBy...)
		edges[node.ID] = normalizedIDs(edges[node.ID])
		sort.Strings(edges[node.ID])
	}
	state := map[string]int{}
	var stack []string
	seen := map[string]bool{}
	var cycles [][]string
	var visit func(string)
	visit = func(id string) {
		state[id] = 1
		stack = append(stack, id)
		for _, next := range edges[id] {
			if state[next] == 0 {
				visit(next)
				continue
			}
			if state[next] != 1 {
				continue
			}
			start := 0
			for stack[start] != next {
				start++
			}
			cycle := append([]string(nil), stack[start:]...)
			cycle = append(cycle, next)
			key := canonicalCycle(cycle)
			if !seen[key] {
				seen[key] = true
				cycles = append(cycles, cycle)
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
	}
	ids := make([]string, 0, len(edges))
	for id := range edges {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if state[id] == 0 {
			visit(id)
		}
	}
	return cycles
}

func canonicalCycle(cycle []string) string {
	if len(cycle) <= 1 {
		return strings.Join(cycle, "|")
	}
	base := cycle[:len(cycle)-1]
	best := ""
	for i := range base {
		rotated := append(append([]string(nil), base[i:]...), base[:i]...)
		candidate := strings.Join(rotated, "|")
		if best == "" || candidate < best {
			best = candidate
		}
	}
	return best
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}
