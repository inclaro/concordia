package intent

// collect builds nodes, relationships and control attachments.
func (l *loader) collect() {
	eachObject(l.doc["nodes"], "/nodes", l.collectNode)
	eachObject(l.doc["relationships"], "/relationships", l.collectRelationship)
	eachObject(l.doc["flows"], "/flows", l.collectFlowDeclaration)
	l.collectControls(l.doc["controls"], ControlScope{Level: ScopeArchitecture}, "/controls")
}

func (l *loader) collectFlowDeclaration(flow obj, pointer string) {
	l.collectControls(flow["controls"], ControlScope{Level: ScopeFlow, ID: asStr(flow["unique-id"])}, pointer+"/controls")
	l.collectFlow(flow, pointer)
}

func (l *loader) collectNode(decl obj, pointer string) {
	uniqueID, ok := decl["unique-id"].(string)
	if !ok {
		return
	}

	ext := l.nodeExtension(decl, pointer)

	l.nodes = append(l.nodes, NodeDeclaration{
		ID:          uniqueID,
		NodeType:    asStr(decl["node-type"]),
		Name:        asStr(decl["name"]),
		Description: asStr(decl["description"]),
		Tags:        strList(ext["tags"]),
		Bindings:    bindingsOf(ext["bindings"]),
		Interfaces:  interfaceIDs(decl),
		Pointer:     pointer,
		Owner:       asStr(ext["owner"]),
		C4Level:     C4Level(asStr(ext["c4-level"])),
		Trust:       l.trustOf(ext["trust"], pointer+"/concordia/trust"),
	})
	l.collectControls(decl["controls"], ControlScope{Level: ScopeNode, ID: uniqueID}, pointer+"/controls")
}

// nodeExtension gives the `concordia` extension of a node declaration. It is
// empty when the node has none or the extension is not valid.
func (l *loader) nodeExtension(decl obj, pointer string) obj {
	raw, present := decl["concordia"]
	if !present || !l.extErrors(l.set.node, raw, pointer+"/concordia") {
		return obj{}
	}

	if ext, isObj := asObj(raw); isObj {
		return ext
	}

	return obj{}
}

func bindingsOf(raw any) []ExtBinding {
	bindings := []ExtBinding{}

	for _, item := range asArr(raw) {
		if b, isObj := asObj(item); isObj {
			bindings = append(bindings, bindingOf(b))
		}
	}

	return bindings
}

func bindingOf(b obj) ExtBinding {
	return ExtBinding{
		Kind: asStr(b["kind"]), Value: asStr(b["value"]),
		Environment: asStr(b["environment"]), Cluster: asStr(b["cluster"]),
		APIGroup: asStr(b["api-group"]), ResourceKind: asStr(b["resource-kind"]),
		Namespace: asStr(b["namespace"]), Name: asStr(b["name"]),
		Repository: asStr(b["repository"]), Digest: asStr(b["digest"]),
		Address: asStr(b["address"]), File: asStr(b["file"]), Service: asStr(b["service"]),
		Cardinality: asStr(b["cardinality"]), Agreement: asStr(b["agreement"]),
		Status: asStr(b["status"]), Authority: asStr(b["authority"]),
	}
}

// interfaceIDs returns the distinct unique-ids of the interfaces of a node declaration, in declaration order.
func interfaceIDs(decl obj) []string {
	ids := []string{}
	seen := map[string]bool{}

	for _, raw := range asArr(decl["interfaces"]) {
		item, _ := asObj(raw)

		if id := asStr(item["unique-id"]); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	return ids
}
