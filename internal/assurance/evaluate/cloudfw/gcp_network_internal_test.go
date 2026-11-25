package cloudfw

import (
	"testing"

	"github.com/inclaro/concordia/internal/assurance/plan"
)

// Addresses of the Shared VPC scenarios.
const (
	sharedVPCInstance = "google_compute_instance.web"
	sharedVPCSubnet   = "google_compute_subnetwork.shared"
)

// sharedVPCInterface is the first network interface of the instance of the scenarios: the bare subnetwork `shared`.
func sharedVPCInterface(subnetProject string) map[string]any {
	nic := map[string]any{"subnetwork": "shared"}
	if subnetProject != "" {
		nic["subnetwork_project"] = subnetProject
	}

	return nic
}

// TestBareSubnetworkInTheSubnetworkProject: a bare subnetwork name is in the
// `subnetwork_project` of the interface (a Shared VPC host project), not in the project of the instance.
func TestBareSubnetworkInTheSubnetworkProject(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		subnetProject, hostProject string
		configured                 []string
		found                      bool
	}{
		"subnetwork in the host project that the interface names": {
			subnetProject: "host-b", hostProject: "host-b", found: true,
			configured: []string{"network_interface[0].subnetwork", "network_interface[0].subnetwork_project", "project"},
		},
		"subnetwork in another project than the one the interface names": {
			subnetProject: "host-b", hostProject: "host-c",
			configured: []string{"network_interface[0].subnetwork", "network_interface[0].subnetwork_project", "project"},
		},
		"subnetwork project unset: the provider project, not the explicit instance project": {
			hostProject: "service-a", configured: []string{"network_interface[0].subnetwork", "project"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			instance := resourceOf(sharedVPCInstance, "google_compute_instance", map[string]any{
				"zone": "europe-west1-b", "project": "service-a", "network_interface": []any{sharedVPCInterface(tc.subnetProject)},
			})
			instance.Configured = tc.configured

			sub := resourceOf(sharedVPCSubnet, gcpSubnetworkType, map[string]any{
				"name": "shared", "region": "europe-west1", "project": tc.hostProject,
			})
			e := coreEvaluator(instance, sub)

			if got := e.gcpSubnet(e.resource(sharedVPCInstance)); (got != nil) != tc.found {
				t.Errorf("subnetwork found = %v, want %v", got != nil, tc.found)
			}
		})
	}
}

// TestUnknownExplicitProjectsAreNotOne: two projects that the configuration sets to
// values unknown until apply can be two projects; only projects that the configuration leaves to the provider
// default are one project of one provider configuration.
func TestUnknownExplicitProjectsAreNotOne(t *testing.T) {
	t.Parallel()

	network := func(address string, configured []string) plan.Resource {
		res := resourceOf(address, gcpNetworkType, map[string]any{"name": "vpc"})
		res.Unknown = []string{gcpProjectAttr}
		res.Configured = configured
		res.ProviderConfig = "google"

		return res
	}

	cases := map[string]struct {
		configured []string
		want       tri
	}{
		"projects set to unknown values": {configured: []string{"name", gcpProjectAttr}, want: triMaybe},
		"projects left to the provider":  {configured: []string{"name"}, want: triYes},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := coreEvaluator(network("google_compute_network.a", tc.configured),
				network("google_compute_network.b", tc.configured))
			a := e.gcpNetOfResource(e.resource("google_compute_network.a"), "vpc")
			b := e.gcpNetOfResource(e.resource("google_compute_network.b"), "vpc")

			if got := a.same(b); got != tc.want {
				t.Errorf("same = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestQualifiedSubnetworkMatchesItsIdentity: a self link of a subnetwork matches only a
// subnetwork with that link, and a bare name only one in the project and region of the instance.
func TestQualifiedSubnetworkMatchesItsIdentity(t *testing.T) {
	t.Parallel()

	sub := resourceOf("google_compute_subnetwork.app", gcpSubnetworkType, map[string]any{
		"name": "app", "region": "europe-west1", "project": "p",
	})

	cases := map[string]struct {
		subnetwork, zone string
		found            bool
	}{
		"self link of another project": {subnetwork: "projects/q/regions/europe-west1/subnetworks/app", zone: "europe-west1-b"},
		"bare name in another region":  {subnetwork: "app", zone: "us-central1-a"},
		"bare name in its region":      {subnetwork: "app", zone: "europe-west1-b", found: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			instance := resourceOf("google_compute_instance.web", "google_compute_instance", map[string]any{
				"zone": tc.zone, "project": "p", "network_interface": []any{map[string]any{"subnetwork": tc.subnetwork}},
			})
			instance.Configured = []string{"network_interface", "network_interface[0].subnetwork", "zone"}
			e := coreEvaluator(instance, sub)

			if got := e.gcpSubnet(e.resource("google_compute_instance.web")); (got != nil) != tc.found {
				t.Errorf("subnetwork found = %v, want %v", got != nil, tc.found)
			}
		})
	}
}
