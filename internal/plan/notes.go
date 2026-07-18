package plan

import (
	"strings"

	"github.com/erikvoit/dharana-cli/internal/richtext"
)

func effectiveNotes(node Node) *string {
	if node.Type == "spike" && node.Timebox != nil && strings.TrimSpace(*node.Timebox) != "" {
		lines := []string{
			"Timebox: " + strings.TrimSpace(*node.Timebox),
			"",
			"Expected outcomes:",
			"- Root-cause analysis",
			"- Technical recommendation",
			"- Follow-up story or bug, if needed",
		}
		if node.Notes != nil && strings.TrimSpace(*node.Notes) != "" {
			lines = append(lines, "", strings.TrimSpace(*node.Notes))
		}
		value := strings.Join(lines, "\n")
		return &value
	}
	if node.Type == "task" && node.Estimate != nil && strings.TrimSpace(*node.Estimate) != "" {
		lines := []string{"Estimate: " + strings.TrimSpace(*node.Estimate)}
		if node.Notes != nil && strings.TrimSpace(*node.Notes) != "" {
			lines = append(lines, "", strings.TrimSpace(*node.Notes))
		}
		value := strings.Join(lines, "\n")
		return &value
	}
	return node.Notes
}

func effectiveDescription(node Node) *richtext.Description {
	if node.Description == nil {
		return nil
	}
	content := strings.TrimSpace(node.Description.Content)
	var managed string
	if node.Type == "spike" && node.Timebox != nil && strings.TrimSpace(*node.Timebox) != "" {
		managed = "**Timebox:** " + strings.TrimSpace(*node.Timebox) + "\n\n## Expected outcomes\n\n- Root-cause analysis\n- Technical recommendation\n- Follow-up story or bug, if needed"
	}
	if node.Type == "task" && node.Estimate != nil && strings.TrimSpace(*node.Estimate) != "" {
		managed = "**Estimate:** " + strings.TrimSpace(*node.Estimate)
	}
	if managed != "" && content != "" {
		content = managed + "\n\n" + content
	} else if managed != "" {
		content = managed
	}
	return &richtext.Description{Format: "markdown", Content: content}
}

func effectiveHTMLNotes(node Node) *string {
	description := effectiveDescription(node)
	if description == nil {
		return nil
	}
	value, err := richtext.RenderMarkdown(description.Content)
	if err != nil {
		return nil
	}
	return &value
}

func parseSpikeManagedMarkdown(description *richtext.Description) (*string, *richtext.Description) {
	if description == nil {
		return nil, nil
	}
	lines := compactMarkdownLines(description.Content)
	if len(lines) < 5 || !strings.HasPrefix(lines[0], "**Timebox:** ") || lines[1] != "## Expected outcomes" || lines[2] != "- Root-cause analysis" || lines[3] != "- Technical recommendation" || lines[4] != "- Follow-up story or bug, if needed" {
		return nil, description
	}
	timebox := strings.TrimSpace(strings.TrimPrefix(lines[0], "**Timebox:** "))
	remainder := strings.TrimSpace(strings.Join(lines[5:], "\n\n"))
	if remainder == "" {
		return optionalStringPointer(timebox), nil
	}
	return optionalStringPointer(timebox), &richtext.Description{Format: "markdown", Content: remainder}
}

func parseTaskManagedMarkdown(description *richtext.Description) (*string, *richtext.Description) {
	if description == nil {
		return nil, nil
	}
	lines := compactMarkdownLines(description.Content)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "**Estimate:** ") {
		return nil, description
	}
	estimate := strings.TrimSpace(strings.TrimPrefix(lines[0], "**Estimate:** "))
	remainder := strings.TrimSpace(strings.Join(lines[1:], "\n\n"))
	if remainder == "" {
		return optionalStringPointer(estimate), nil
	}
	return optionalStringPointer(estimate), &richtext.Description{Format: "markdown", Content: remainder}
}

func compactMarkdownLines(value string) []string {
	var values []string
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}
