package main

import "testing"

func TestBuildCSVAddsHCOPolicyLabelsOnlyToPodTemplate(t *testing.T) {
	csv := buildCSV(nil, nil, nil)
	deployment := csv.Spec.Install.Spec.Deployments[0]

	for _, key := range []string{
		"np.kubevirt.io/allow-access-cluster-services",
		"np.kubevirt.io/allow-prometheus-access",
	} {
		if deployment.Spec.Template.Metadata.Labels[key] != "true" {
			t.Errorf("pod template label %q = %q, want true", key, deployment.Spec.Template.Metadata.Labels[key])
		}
		if _, found := deployment.Spec.Selector.MatchLabels[key]; found {
			t.Errorf("deployment selector unexpectedly contains network-policy label %q", key)
		}
	}
}
