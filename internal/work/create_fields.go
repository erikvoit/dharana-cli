package work

import (
	"github.com/erikvoit/dharana-cli/internal/config"
	"github.com/erikvoit/dharana-cli/internal/workflowstate"
)

func creationCustomFields(cfg *config.File, typeOption string, includeState bool) map[string]string {
	fields := make(map[string]string, 2)
	if cfg.TaskTypes.FieldGID != "" && typeOption != "" {
		fields[cfg.TaskTypes.FieldGID] = typeOption
	}
	if includeState && cfg.States.Complete() {
		fields[cfg.States.FieldGID] = cfg.States.Option(workflowstate.Backlog)
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}
