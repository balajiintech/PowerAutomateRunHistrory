package flow

// ColumnOrder is the shared CSV action order: the run trigger, then definition
// order, then any action names that appeared on a run and were not in the definition.
func ColumnOrder(definition []string, runActions [][]string) []string {
	seen := map[string]struct{}{"Trigger": {}}
	order := []string{"Trigger"}
	add := func(name string) {
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		order = append(order, name)
	}
	for _, name := range definition {
		add(name)
	}
	for _, actions := range runActions {
		for _, name := range actions {
			add(name)
		}
	}
	return order
}

// RunFilter builds the Flow runs $filter value. Status is sent in Pascal case.
func RunFilter(status string, since, until string) string {
	filter := "status eq '" + status + "'"
	if since != "" {
		filter += " and startTime ge " + since
	}
	if until != "" {
		filter += " and startTime lt " + until
	}
	return filter
}
