package plan

import "strings"

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
