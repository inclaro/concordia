package intent

type pendingControl struct {
	req     obj
	pointer string
	att     int
}

func (l *loader) collectControls(value any, scope ControlScope, pointer string) {
	controls, ok := asObj(value)
	if !ok {
		return
	}

	for _, key := range sortedKeys(controls) {
		if control, isObj := asObj(controls[key]); isObj {
			l.collectRequirements(key, control, scope, pointer+"/"+esc(key))
		}
	}
}

// collectRequirements attaches each requirement of a control. pointer is the pointer of the control.
func (l *loader) collectRequirements(key string, control obj, scope ControlScope, pointer string) {
	eachObject(control["requirements"], pointer+"/requirements", func(req obj, requirementPointer string) {
		l.controls = append(l.controls, ControlAttachment{ControlID: key, Scope: scope, Pointer: requirementPointer})
		l.pending = append(l.pending, pendingControl{att: len(l.controls) - 1, req: req, pointer: requirementPointer})
	})
}

// checkControlConfigs reads and validates the control configurations.
func (l *loader) checkControlConfigs() {
	for _, pending := range l.pending {
		l.checkControlConfig(pending)
	}
}

func (l *loader) checkControlConfig(pending pendingControl) {
	config, base := l.pendingConfig(pending)

	cfg, ok := asObj(config)
	if !ok {
		return
	}

	att := &l.controls[pending.att]
	att.Engine = asStr(cfg["engine"])

	if cfg["engine"] != "concordia" || !l.extErrors(l.set.control, cfg, base) {
		return
	}

	att.Config = controlConfigOf(cfg)
	if att.Config.ControlID != "" {
		att.ControlID = att.Config.ControlID
	}
}

// pendingConfig gives the configuration of a control requirement (the inline
// `config`, else the document of `config-url`) and the pointer that its
// diagnostics are reported at. The configuration is nil when there is none.
func (l *loader) pendingConfig(pending pendingControl) (config any, base string) {
	if inline, hasConfig := pending.req["config"]; hasConfig {
		return inline, pending.pointer + "/config"
	}

	url := asStr(pending.req["config-url"])
	if url == "" {
		return nil, ""
	}

	return l.readConfigURL(url, pending), pending.pointer + "/config-url"
}

// readConfigURL reads the config-url document of a control requirement. A read
// error gives no config here; controlRules reports it (control-requirement-validation)
// as the CALM CLI does.
func (l *loader) readConfigURL(url string, pending pendingControl) any {
	value, err := l.loadReference(url, pending.pointer+"/config-url")
	if err != nil {
		return nil
	}

	return value
}

func controlConfigOf(cfg obj) *ControlConfig {
	out := &ControlConfig{
		ControlID:          asStr(cfg["control-id"]),
		Engine:             asStr(cfg["engine"]),
		PolicyRef:          asStr(cfg["policy-ref"]),
		RuleIDs:            strList(cfg["rule-ids"]),
		CheckIDs:           strList(cfg["check-ids"]),
		UnknownDisposition: asStr(cfg["unknown-disposition"]),
	}

	if v, ok := cfg["minimum-policy-coverage"].(float64); ok {
		out.MinimumPolicyCoverage = &v
	}

	return out
}
