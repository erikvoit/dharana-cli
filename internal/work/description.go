package work

import (
	"strings"

	"github.com/erikvoit/dharana-cli/internal/output"
	"github.com/erikvoit/dharana-cli/internal/richtext"
)

func descriptionPayload(managed, notes string, description *richtext.Description) (string, string, error) {
	if description == nil {
		return joinDescriptionParts(managed, notes), "", nil
	}
	if strings.TrimSpace(notes) != "" {
		return "", "", output.NewError("DESCRIPTION_NOTES_CONFLICT", "Use Markdown description or plain notes, not both.")
	}
	if err := description.Validate(); err != nil {
		return "", "", output.NewErrorWithDetails("INVALID_MARKDOWN_DESCRIPTION", "The Markdown description cannot be rendered safely.", err.Error())
	}
	markdown := joinDescriptionParts(managed, description.Content)
	htmlNotes, err := richtext.RenderMarkdown(markdown)
	if err != nil {
		return "", "", output.NewErrorWithDetails("INVALID_MARKDOWN_DESCRIPTION", "The Markdown description cannot be rendered safely.", err.Error())
	}
	return "", htmlNotes, nil
}

func joinDescriptionParts(values ...string) string {
	var parts []string
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return strings.Join(parts, "\n\n")
}
