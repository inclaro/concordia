package projection

import (
	"slices"
	"strings"

	"github.com/inclaro/concordia/internal/intent"
)

// styles are the relationship styles. The first line is written after the opening
// line of the block; the next lines have their own indent.
var styles = []struct {
	status relStatus
	body   string
}{
	{statusConforming, "color #2e7d32\n      thickness 2\n      style solid"},
	{statusDeclaredOnly, "color #1565c0\n      thickness 2\n      style dashed"},
	{statusObservedOnly, "color #ef6c00\n      thickness 3\n      style dotted"},
	{statusUnknown, "color #6a1b9a\n      thickness 2\n      style dotted"},
	{statusViolating, "color #c62828\n      thickness 4\n      style solid"},
}

// elementStyles are the styles of the status tags of deployment instances and nodes, in the colors of the
// relationship styles. They are written only when an instance carries a status tag.
var elementStyles = []struct {
	status relStatus
	body   string
}{
	{statusConforming, "stroke #2e7d32\n      strokeWidth 3\n      border solid"},
	{statusDeclaredOnly, "stroke #1565c0\n      strokeWidth 3\n      border dashed"},
	{statusObservedOnly, "stroke #ef6c00\n      strokeWidth 4\n      border dotted"},
	{statusUnknown, "stroke #6a1b9a\n      strokeWidth 3\n      border dotted"},
	{statusViolating, "stroke #c62828\n      strokeWidth 5\n      border solid"},
}

const excludeObservedOnly = `"relationship.tag==concordia:observed-only"`

// systemOf gives the ID of the software system that contains e (or is e).
func (m *dslModel) systemOf(e *C4Element) string {
	for c := e; c != nil; c = m.byID[c.Parent] {
		if c.Level == intent.C4SoftwareSystem {
			return c.ID
		}
	}

	return ""
}

// viewKey gives the key of a Structurizr view of a kind. In committed mode the key is the view ID of the kind (the
// first view of the kind; a later one has the ID and its own key, so that keys stay unique); otherwise it is auto.
func (m *dslModel) viewKey(kind ViewKind, auto string) string {
	id := m.viewIDs[kind]
	if !m.committed || id == "" {
		return auto
	}

	m.keyUsed[kind]++
	if m.keyUsed[kind] == 1 {
		return id
	}

	return id + "-" + auto
}

func (m *dslModel) writeView(out *dslWriter, header string, exclude bool) {
	var excluded []string

	if exclude {
		excluded = append(excluded, excludeObservedOnly)
	}

	if m.evidence {
		excluded = append(excluded, excludeEvidence)
	}

	out.line(2, header+" {")
	out.line(bodyDepth, "include *")

	if len(excluded) > 0 {
		out.line(bodyDepth, "exclude "+strings.Join(excluded, " "))
	}

	out.line(bodyDepth, "autoLayout")
	out.line(2, "}")
}

// dslInScope is true for an element of a level that the focus does not exclude: any one when focusID is empty,
// otherwise only the focus element.
func dslInScope(elem *C4Element, level intent.C4Level, focusID string) bool {
	if elem.Level != level {
		return false
	}

	return focusID == "" || elem.ID == focusID
}

// writeStyleBlock writes the style of one status tag of an element kind (`relationship` or `element`).
func (out *dslWriter) writeStyleBlock(kind string, status relStatus, body string) {
	out.line(bodyDepth, kind+" "+DSLString(statusTag(status))+" {")
	out.line(bodyDepth, "  "+body)
	out.line(bodyDepth, "}")
}

// writeStyles writes the `styles` block of the views.
func (m *dslModel) writeStyles(out *dslWriter) {
	out.line(2, "styles {")

	for _, st := range styles {
		out.writeStyleBlock("relationship", st.status, st.body)
	}

	if m.statusTagged {
		for _, st := range elementStyles {
			out.writeStyleBlock("element", st.status, st.body)
		}
	}

	out.line(2, "}")
}

// writeSystems writes the views of each software system that the focus does not exclude.
func (m *dslModel) writeSystems(out *dslWriter, focusSystem, focusContainer string, want func(ViewKind) bool) {
	for i := range m.els {
		if system := &m.els[i]; dslInScope(system, intent.C4SoftwareSystem, focusSystem) {
			m.writeSystemViews(out, system, focusContainer, want)
		}
	}
}

func (m *dslModel) writeViews(out *dslWriter, opts *DSLOptions, focus *C4Element) {
	want := func(k ViewKind) bool { return opts.Views == nil || opts.Views[k] }

	focusSystem, focusContainer := m.focusScope(focus)

	out.line(1, "views {")

	if want(ViewLandscape) && opts.Focus == "" {
		m.writeView(out, "systemLandscape "+DSLString(m.viewKey(ViewLandscape, "landscape")), true)
	}

	m.writeSystems(out, focusSystem, focusContainer, want)

	if want(ViewDeployment) && len(m.deployments) > 0 {
		m.writeDeploymentViews(out, focusSystem)
	}

	if opts.Dynamic != nil && want(ViewDynamic) {
		m.writeDynamic(out, opts.Dynamic)
	}

	m.writeStyles(out)
	out.line(1, "}")
}

// focusScope gives the software system and the container that the focus
// element belongs to ("" when there is none).
func (m *dslModel) focusScope(focus *C4Element) (system, container string) {
	if focus == nil {
		return "", ""
	}

	system = m.systemOf(focus)

	switch focus.Level {
	case intent.C4Component:
		container = focus.Parent
	case intent.C4Container:
		container = focus.ID
	case intent.C4Person, intent.C4SoftwareSystem:
	}

	return system, container
}

// writeSystemViews writes the wanted views of one software system.
func (m *dslModel) writeSystemViews(out *dslWriter, system *C4Element, focusContainer string, want func(ViewKind) bool) {
	key := m.ident[system.ID]

	if want(ViewContext) {
		m.writeView(out, "systemContext "+key+" "+DSLString(m.viewKey(ViewContext, "context-"+key)), true)
	}

	if want(ViewContainer) {
		m.writeView(out, "container "+key+" "+DSLString(m.viewKey(ViewContainer, "container-"+key)), true)
	}

	if want(ViewDrift) {
		m.writeView(out, "container "+key+" "+DSLString(m.viewKey(ViewDrift, "drift-"+key)), false)
	}

	if want(ViewComponent) {
		m.writeComponentViews(out, system, focusContainer)
	}
}

// writeDeploymentViews writes the deployment view of each environment that the DSL has, of the focus system when
// there is one. The first view of a workspace without declared environments keeps the as-built key.
func (m *dslModel) writeDeploymentViews(out *dslWriter, focusSystem string) {
	for _, env := range m.deployments {
		scope, auto := "*", "deployment"
		if focusSystem != "" {
			scope = m.ident[focusSystem]
			auto += "-" + scope
		}

		if env != deploymentEnvironment {
			auto += "-" + env
		}

		m.writeView(out, "deployment "+scope+" "+DSLString(env)+" "+DSLString(m.viewKey(ViewDeployment, auto)), false)
	}
}

// hasComponents is true when the container has a component.
func (m *dslModel) hasComponents(container *C4Element) bool {
	return slices.ContainsFunc(m.children(container.ID), func(y *C4Element) bool { return y.Level == intent.C4Component })
}

func (m *dslModel) writeComponentViews(out *dslWriter, system *C4Element, focusContainer string) {
	for _, c := range m.children(system.ID) {
		if !dslInScope(c, intent.C4Container, focusContainer) || !m.hasComponents(c) {
			continue
		}

		key := m.ident[c.ID]
		m.writeView(out, "component "+key+" "+DSLString("component-"+key), true)
	}
}
