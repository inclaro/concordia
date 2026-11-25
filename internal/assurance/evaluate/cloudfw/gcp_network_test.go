package cloudfw_test

import (
	"slices"

	"github.com/inclaro/concordia/internal/assurance/evaluate"
	"github.com/inclaro/concordia/internal/assurance/evaluate/cloudfw"
	"github.com/inclaro/concordia/internal/assurance/plan"
)

// gcpNetworkScenarios are instances whose networks or network interfaces the evaluator cannot compare.
func gcpNetworkScenarios() []gcpScenario {
	return []gcpScenario{
		{
			name: "networks of one name in two projects", from: gcpWeb, to: gcpDB, port: 5432,
			mutate: gcpAll(gcpNetworkLink(gcpWeb, "projects/a/global/networks/vpc"),
				gcpNetworkLink(gcpDB, "projects/b/global/networks/vpc")),
			want: cloudfw.VerdictUnknown, gap: evaluate.GapPeeringNotEvaluated, rules: "",
		},
		{
			name: "instance with two network interfaces", from: gcpWeb, to: gcpDB, port: 5432,
			mutate: gcpEdit(gcpWeb, func(res *plan.Resource) {
				nics, _ := res.Values[gcpInterfaces].([]any)
				res.Values[gcpInterfaces] = append(nics, map[string]any{"network_ip": "10.40.0.5"})
			}),
			want: cloudfw.VerdictUnknown, gap: evaluate.GapUnsupportedResource, rules: "",
		},
	}
}

// gcpNetworkLink makes the plan know the network of the first interface of the instance as a self link.
func gcpNetworkLink(address, link string) func(in *cloudfw.Input) {
	return gcpEdit(address, func(res *plan.Resource) {
		nic, _ := res.Values[gcpInterfaces].([]any)[0].(map[string]any)
		nic["network"] = link
		res.Unknown = slices.DeleteFunc(res.Unknown, func(path string) bool { return path == "network_interface[0].network" })
	})
}

// gcpTypedScenarios are references that name a resource of a type that the attribute cannot hold.
func gcpTypedScenarios() []gcpScenario {
	return []gcpScenario{
		{
			name: "network reference to a resource that is not a network", from: gcpWeb, to: gcpDB, port: 5432,
			mutate: gcpAll(gcpSetReference(gcpAttr{gcpWeb, gcpInstanceNet}, gcpWrongTypeNode),
				gcpSetReference(gcpAttr{gcpDB, gcpInstanceNet}, gcpWrongTypeNode)),
			want: cloudfw.VerdictUnknown, gap: evaluate.GapUnresolvedReference, rules: "",
		},
	}
}
