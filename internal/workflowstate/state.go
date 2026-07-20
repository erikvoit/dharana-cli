package workflowstate

import (
	"sort"
	"strings"
)

const (
	Backlog      = "backlog"
	Selected     = "selected"
	InProgress   = "in_progress"
	Verification = "verification"
	Done         = "done"
	Deferred     = "deferred"
	Canceled     = "canceled"
)

type Definition struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Terminal    bool   `json:"terminal"`
	Active      bool   `json:"active"`
}

var definitions = []Definition{
	{Name: Backlog, DisplayName: "Backlog"},
	{Name: Selected, DisplayName: "Selected for Development", Active: true},
	{Name: InProgress, DisplayName: "In Progress", Active: true},
	{Name: Verification, DisplayName: "Verification", Active: true},
	{Name: Done, DisplayName: "Done", Terminal: true},
	{Name: Deferred, DisplayName: "Deferred"},
	{Name: Canceled, DisplayName: "Canceled", Terminal: true},
}

var transitions = map[string][]string{
	"":           {Backlog, Selected, InProgress, Deferred, Canceled},
	Backlog:      {Selected, Deferred, Canceled},
	Selected:     {Backlog, InProgress, Deferred, Canceled},
	InProgress:   {Selected, Verification, Deferred, Canceled},
	Verification: {InProgress, Done, Canceled},
	Done:         {Selected},
	Deferred:     {Backlog, Selected, Canceled},
	Canceled:     {Backlog},
}

func Definitions() []Definition { return append([]Definition(nil), definitions...) }

func Names() []string {
	values := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		values = append(values, definition.Name)
	}
	return values
}

func DisplayName(value string) string {
	value, _ = Normalize(value)
	for _, definition := range definitions {
		if definition.Name == value {
			return definition.DisplayName
		}
	}
	return ""
}

func Normalize(value string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(value))
	key = strings.NewReplacer("-", "_", " ", "_").Replace(key)
	switch key {
	case "selected_for_development", "ready":
		key = Selected
	case "inprogress", "doing":
		key = InProgress
	case "review", "in_review", "verify":
		key = Verification
	case "complete", "completed":
		key = Done
	case "cancelled", "wont_do", "won't_do":
		key = Canceled
	}
	for _, definition := range definitions {
		if definition.Name == key {
			return key, true
		}
	}
	return key, false
}

func IsTerminal(value string) bool { return value == Done || value == Canceled }
func IsReady(value string) bool    { return value == Selected }

func CanTransition(from, to string) bool {
	from, fromOK := Normalize(from)
	if strings.TrimSpace(from) == "" {
		fromOK = true
	}
	to, toOK := Normalize(to)
	if !fromOK || !toOK || from == to {
		return fromOK && toOK && from == to
	}
	for _, candidate := range transitions[from] {
		if candidate == to {
			return true
		}
	}
	return false
}

func AllowedTransitions(from string) []string {
	from, ok := Normalize(from)
	if strings.TrimSpace(from) == "" {
		ok = true
	}
	if !ok {
		return nil
	}
	values := append([]string(nil), transitions[from]...)
	sort.Strings(values)
	return values
}

func Path(from, to string) ([]string, bool) {
	from, fromOK := Normalize(from)
	if strings.TrimSpace(from) == "" {
		fromOK = true
	}
	to, toOK := Normalize(to)
	if !fromOK || !toOK {
		return nil, false
	}
	if from == to {
		return []string{}, true
	}
	type node struct {
		state string
		path  []string
	}
	queue := []node{{state: from}}
	seen := map[string]bool{from: true}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range transitions[current.state] {
			if seen[next] {
				continue
			}
			path := append(append([]string(nil), current.path...), next)
			if next == to {
				return path, true
			}
			seen[next] = true
			queue = append(queue, node{state: next, path: path})
		}
	}
	return nil, false
}
