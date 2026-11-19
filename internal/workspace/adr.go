package workspace

// ADR is the ADR governance configuration of a workspace.
type ADR struct {
	// Files are the decision register file globs.
	Files     []string     `json:"files"`
	Lifecycle ADRLifecycle `json:"lifecycle"`
	Triggers  []ADRTrigger `json:"triggers"`
}

// ADRLifecycle maps decision statuses to the active lifecycle class.
type ADRLifecycle struct {
	// StatusMap maps a raw status text (case-insensitive) to a status.
	StatusMap map[string]string `json:"statusMap"`
	// Active are the statuses that count as active. Default: accepted.
	Active []string `json:"active"`
}

// ADRTrigger is a scope trigger: a change that should be justified by a decision.
type ADRTrigger struct {
	ID           string   `json:"id"`
	Category     string   `json:"category,omitempty"`
	Paths        []string `json:"paths"`
	Architecture []string `json:"architecture"`
	NodeTypes    []string `json:"nodeTypes"`
	Tags         []string `json:"tags"`
}

// DefaultADRActive gives the default active statuses.
func DefaultADRActive() []string { return []string{"accepted"} }

// adrOf reads the merged `adr` object with defaults. It returns nil when there is no `adr` key.
func adrOf(merged object) *ADR {
	raw, ok := merged["adr"].(map[string]any)
	if !ok {
		return nil
	}

	life := obj(raw, "lifecycle")
	statusMap := map[string]string{}

	if m, isMap := life["statusMap"].(map[string]any); isMap {
		for k, v := range m {
			if s, isStr := v.(string); isStr {
				statusMap[k] = s
			}
		}
	}

	out := &ADR{
		Files:     strList(raw, "files", []string{}),
		Lifecycle: ADRLifecycle{Active: strList(life, "active", DefaultADRActive()), StatusMap: statusMap},
		Triggers:  []ADRTrigger{},
	}

	list, _ := raw["triggers"].([]any)
	for _, item := range list {
		trig, isObj := item.(map[string]any)
		if !isObj {
			continue
		}

		out.Triggers = append(out.Triggers, ADRTrigger{
			ID:           str(trig, "id", ""),
			Category:     str(trig, "category", ""),
			Paths:        strList(trig, "paths", []string{}),
			Architecture: strList(trig, "architecture", []string{}),
			NodeTypes:    strList(trig, "nodeTypes", []string{}),
			Tags:         strList(trig, "tags", []string{}),
		})
	}

	return out
}
