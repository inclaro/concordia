package deployment

import (
	"slices"
)

func props(values map[string]string) map[string]string {
	out := map[string]string{}

	for key, value := range values {
		if value != "" && isAllowedProperty(key) {
			out[key] = value
		}
	}

	return out
}

// allowedProperties are the entity properties: kind, namespace, name, container name, image parts, stage names, and
// the structure of an action or a binding. Environment variable values, `env`, `envFrom`, secret data, and
// command arguments are never read into an entity (default deny).
var allowedProperties = []string{
	propAPIGroup, propBindingKind, propBuilder, propContainer, propDigest, propKind, propName, propNamespace, propNodeType, propReference,
	propRegistry, propRepository, propSourceRepository, propStage, propTag, propTarget, propTargetKind, propTemplated, propTool,
	propTrustContract,
}

// AllowedProperties gives the property keys that an entity may have, sorted.
func AllowedProperties() []string { return slices.Clone(allowedProperties) }

func isAllowedProperty(key string) bool { return slices.Contains(allowedProperties, key) }
