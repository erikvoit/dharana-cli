package plan

func Schema() map[string]any {
	stringPointer := map[string]any{"type": []string{"string", "null"}}
	booleanPointer := map[string]any{"type": []string{"boolean", "null"}}
	description := map[string]any{
		"type": []string{"object", "null"}, "additionalProperties": false,
		"required":   []string{"format", "content"},
		"properties": map[string]any{"format": map[string]any{"const": "markdown"}, "content": map[string]any{"type": "string"}},
	}
	taskProperties := map[string]any{
		"id":          map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]*$"},
		"name":        map[string]any{"type": "string", "minLength": 1},
		"notes":       stringPointer,
		"description": description,
		"assignee":    stringPointer,
		"dueOn":       map[string]any{"type": []string{"string", "null"}, "format": "date"},
		"estimate":    map[string]any{"type": []string{"string", "null"}, "pattern": "^[1-9][0-9]*(m|h|d|w)$"},
		"completed":   booleanPointer,
		"blockedBy":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "uniqueItems": true},
	}
	workProperties := map[string]any{
		"id":          map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]*$"},
		"type":        map[string]any{"enum": []string{"story", "bug", "spike"}},
		"name":        map[string]any{"type": "string", "minLength": 1},
		"notes":       stringPointer,
		"description": description,
		"assignee":    stringPointer,
		"dueOn":       map[string]any{"type": []string{"string", "null"}, "format": "date"},
		"priority":    stringPointer,
		"component":   stringPointer,
		"timebox":     map[string]any{"type": []string{"string", "null"}, "pattern": "^[1-9][0-9]*(m|h|d|w)$"},
		"completed":   booleanPointer,
		"blockedBy":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "uniqueItems": true},
		"tasks": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "name"}, "properties": taskProperties},
		},
	}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://dharana.dev/schemas/epic-plan-v1alpha1.json",
		"title":                "Dharana EpicPlan",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"apiVersion", "kind", "metadata", "spec"},
		"properties": map[string]any{
			"apiVersion": map[string]any{"const": APIVersion},
			"kind":       map[string]any{"const": Kind},
			"metadata": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"id"},
				"properties": map[string]any{
					"id":      map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]*$"},
					"context": map[string]any{"type": "string"},
				},
			},
			"spec": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"epic"},
				"properties": map[string]any{
					"project":       map[string]any{"type": "string"},
					"removalPolicy": map[string]any{"enum": []string{"preserve", "complete"}, "default": "preserve"},
					"epic": map[string]any{
						"type": "object", "additionalProperties": false, "required": []string{"id", "name"},
						"properties": map[string]any{"id": map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]*$"}, "name": map[string]any{"type": "string", "minLength": 1}, "notes": stringPointer, "description": description},
					},
					"work": map[string]any{"type": "array", "items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"id", "type", "name"}, "properties": workProperties}},
				},
			},
		},
	}
}
