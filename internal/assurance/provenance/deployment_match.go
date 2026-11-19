package provenance

import (
	"slices"
	"time"

	"github.com/inclaro/concordia/internal/assurance"
	"github.com/inclaro/concordia/internal/assurance/render"
	"github.com/inclaro/concordia/internal/assurance/statement"
	"github.com/inclaro/concordia/internal/canonjson"
)

// statementParts are the fields of a record or an observation that decide whether it counts for an occurrence.
type statementParts struct {
	ref         *Ref
	admission   *assurance.Admission
	environment string
	placement   placement
}

// placement is where a statement says its workload container runs.
type placement struct {
	workload    *statement.Workload
	container   string
	cluster     string
	kubeContext string
}

func recordParts(rec *Record) statementParts {
	return statementParts{
		ref: &rec.Ref, admission: &rec.Admission, environment: rec.Record.Environment,
		placement: placement{workload: &rec.Record.Workload, container: rec.Record.Container, cluster: rec.Record.Cluster, kubeContext: rec.Record.Context},
	}
}

func observationParts(obs *Observation) statementParts {
	return statementParts{
		ref: &obs.Ref, admission: &obs.Admission, environment: obs.Observation.Environment,
		placement: placement{workload: &obs.Observation.Workload, container: obs.Observation.Container, cluster: obs.Observation.Cluster, kubeContext: obs.Observation.Context},
	}
}

// partitionAbout splits the statements about the occurrence into the admissible ones of its environment and the
// rejected ones. Statements about another workload container are in neither.
func partitionAbout[T any](items []T, occ *render.ContainerOccurrence, partsOf func(*T) statementParts) ([]*T, []Rejected) {
	var (
		admitted []*T
		rejected []Rejected
	)

	for i := range items {
		item := &items[i]
		parts := partsOf(item)

		switch {
		case !parts.placement.matches(occ):
		case !parts.admission.Admissible:
			rejected = append(rejected, Rejected{Ref: *parts.ref, Details: slices.Clone(parts.admission.Details)})
		case parts.environment == occ.Workload.Environment:
			// An admissible statement was admitted for the obligation's environment; one for another
			// environment of the same run is not about this occurrence.
			admitted = append(admitted, item)
		}
	}

	return admitted, rejected
}

// matches compares the workload container. The environment is not part of it: an inadmissible statement for
// another environment is still listed as rejected, with `different-environment`.
func (p *placement) matches(occ *render.ContainerOccurrence) bool {
	if p.container != occ.Container || !sameWorkload(p.workload, &occ.Workload) {
		return false
	}

	return p.clusterMatches(occ.Workload.Cluster)
}

func sameWorkload(w *statement.Workload, id *render.ResourceID) bool {
	if w.Namespace != id.Namespace || w.Kind != id.Kind {
		return false
	}

	return w.Name == id.Name
}

// clusterMatches holds unless both the rendered workload and the statement state a cluster, and they differ.
func (p *placement) clusterMatches(rendered string) bool {
	if rendered == "" {
		return true
	}

	if p.cluster == "" && p.kubeContext == "" {
		return true
	}

	return rendered == p.cluster || rendered == p.kubeContext
}

// newest gives the items with the newest time, in path order. when gives the time and the path of an item.
func newest[T any](items []*T, when func(*T) (time.Time, string)) []*T {
	timeOf := func(item *T) time.Time {
		at, _ := when(item)

		return at
	}

	var latest time.Time

	for _, item := range items {
		latest = laterTime(latest, timeOf(item))
	}

	out := slices.DeleteFunc(slices.Clone(items), func(item *T) bool { return !timeOf(item).Equal(latest) })

	slices.SortFunc(out, func(a, b *T) int {
		_, pathA := when(a)
		_, pathB := when(b)

		return canonjson.CompareUTF16(pathA, pathB)
	})

	return out
}

func laterTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}

	return a
}
