/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"
)

const serviceAccountName = "inflightoperations-controller-manager"

type generatorFlags struct {
	crds            string
	rbacDir         string
	dumpCRDs        bool
	csvVersion      string
	namespace       string
	operatorImage   string
	operatorVersion string
	pullPolicy      string
}

var (
	flags   generatorFlags
	command = &cobra.Command{
		Use:   "csv-generator",
		Short: "csv-generator for inflightoperations",
		Long:  `csv-generator generates a ClusterServiceVersion manifest for inflightoperations`,
		Run: func(cmd *cobra.Command, args []string) {
			if err := generate(); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
		},
	}
)

func init() {
	command.Flags().StringVar(&flags.crds, "crds",
		"config/crd/bases", "Directory containing CRD files")
	command.Flags().StringVar(&flags.rbacDir, "rbac-dir",
		"config/rbac", "Directory containing RBAC files")
	command.Flags().StringVar(&flags.csvVersion, "csv-version",
		"", "Version of csv manifest (required)")
	command.Flags().StringVar(&flags.namespace, "namespace",
		"", "Namespace in which the operator will be deployed (required)")
	command.Flags().StringVar(&flags.operatorImage, "operator-image",
		"", "Operator container image (required)")
	command.Flags().StringVar(&flags.operatorVersion, "operator-version",
		"", "Operator version (required)")
	command.Flags().StringVar(&flags.pullPolicy, "pull-policy",
		"IfNotPresent", "Image pull policy")
	command.Flags().BoolVar(&flags.dumpCRDs, "dump-crds",
		false, "Dump CRDs to stdout after the CSV")

	for _, required := range []string{"csv-version", "namespace", "operator-image", "operator-version"} {
		if err := command.MarkFlagRequired(required); err != nil {
			panic(fmt.Sprintf("marking flag %s required: %v", required, err))
		}
	}
}

func main() {
	if err := command.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func generate() error {
	clusterPermRules, err := buildClusterPermissions()
	if err != nil {
		return fmt.Errorf("building cluster permissions: %w", err)
	}

	permRules, err := readRBACRules(filepath.Join(flags.rbacDir, "leader_election_role.yaml"))
	if err != nil {
		return fmt.Errorf("reading leader election role: %w", err)
	}

	ownedCRDs, err := readOwnedCRDs(flags.crds)
	if err != nil {
		return fmt.Errorf("reading owned CRDs: %w", err)
	}

	csv := buildCSV(clusterPermRules, permRules, ownedCRDs)

	if err := writeDocument(csv, os.Stdout); err != nil {
		return fmt.Errorf("writing CSV: %w", err)
	}

	if flags.dumpCRDs {
		if err := dumpCRDs(flags.crds); err != nil {
			return fmt.Errorf("dumping CRDs: %w", err)
		}
	}
	return nil
}

func buildClusterPermissions() ([]PolicyRule, error) {
	managerRules, err := readRBACRules(filepath.Join(flags.rbacDir, "role.yaml"))
	if err != nil {
		return nil, fmt.Errorf("reading manager role: %w", err)
	}

	watchRules, err := readRBACRules(filepath.Join(flags.rbacDir, "watch_role.yaml"))
	if err != nil {
		return nil, fmt.Errorf("reading watch role: %w", err)
	}

	var rules []PolicyRule
	rules = append(rules, managerRules...)
	rules = append(rules, metricsAuthRules()...)
	rules = append(rules, watchRules...)
	return rules, nil
}

func buildCSV(
	clusterPermRules []PolicyRule,
	permRules []PolicyRule,
	ownedCRDs []CRDDescription,
) ClusterServiceVersion {
	trueVal := true
	falseVal := false
	gracePeriod := int64(10)
	labels := map[string]string{
		"app.kubernetes.io/name": "inflightoperations",
		"control-plane":          "controller-manager",
	}
	// HCO-generated NetworkPolicies select pod labels. Keep these out of the
	// Deployment selector: they are not an identity of the workload and should
	// remain freely changeable as the HCO policy contract evolves.
	podLabels := make(map[string]string, len(labels)+2)
	maps.Copy(podLabels, labels)
	podLabels["np.kubevirt.io/allow-access-cluster-services"] = "true"
	podLabels["np.kubevirt.io/allow-prometheus-access"] = "true"

	return ClusterServiceVersion{
		APIVersion: "operators.coreos.com/v1alpha1",
		Kind:       "ClusterServiceVersion",
		Metadata: CSVMetadata{
			Name:      "inflightoperations.v" + flags.csvVersion,
			Namespace: flags.namespace,
			Annotations: map[string]string{
				"alm-examples":   "[]",
				"capabilities":   "Basic Install",
				"containerImage": flags.operatorImage,
				"repository":     "https://github.com/openshift-virtualization/inflightoperations",
			},
		},
		Spec: CSVSpec{
			DisplayName: "InFlightOperations",
			Description: "Detects in-flight operations on Kubernetes resources using " +
				"CEL expressions and creates InFlightOperation objects to track them.",
			Keywords: []string{
				"inflightoperations",
				"kubevirt",
				"virtualization",
				"observability",
			},
			Links: []Link{
				{
					Name: "Source Code",
					URL:  "https://github.com/openshift-virtualization/inflightoperations",
				},
			},
			Maintainers: []Maintainer{
				{
					Name:  "Samuel Lucidi",
					Email: "slucidi@redhat.com",
				},
			},
			Provider: Provider{
				Name: "Red Hat",
				URL:  "https://www.redhat.com",
			},
			Maturity: "alpha",
			Version:  flags.csvVersion,
			InstallModes: []InstallMode{
				{Type: "OwnNamespace", Supported: false},
				{Type: "SingleNamespace", Supported: false},
				{Type: "MultiNamespace", Supported: false},
				{Type: "AllNamespaces", Supported: true},
			},
			Install: NamedInstallStrategy{
				Strategy: "deployment",
				Spec: InstallStrategySpec{
					ClusterPermissions: []StrategyDeploymentPermissions{
						{
							ServiceAccountName: serviceAccountName,
							Rules:              clusterPermRules,
						},
					},
					Permissions: []StrategyDeploymentPermissions{
						{
							ServiceAccountName: serviceAccountName,
							Rules:              permRules,
						},
					},
					Deployments: []StrategyDeploymentSpec{
						{
							Name:  serviceAccountName,
							Label: labels,
							Spec: DeploymentSpec{
								Replicas: 1,
								Selector: &LabelSelector{
									MatchLabels: labels,
								},
								Template: PodTemplateSpec{
									Metadata: PodMetadata{
										Annotations: map[string]string{
											"kubectl.kubernetes.io/default-container": "manager",
										},
										Labels: podLabels,
									},
									Spec: PodSpec{
										ServiceAccountName:            serviceAccountName,
										TerminationGracePeriodSeconds: &gracePeriod,
										SecurityContext: &PodSecurityContext{
											RunAsNonRoot: &trueVal,
											SeccompProfile: &SeccompProfile{
												Type: "RuntimeDefault",
											},
										},
										Containers: []Container{
											{
												Name:            "manager",
												Image:           flags.operatorImage,
												ImagePullPolicy: flags.pullPolicy,
												Command:         []string{"/manager"},
												Args: []string{
													"--metrics-bind-address=:8443",
													"--leader-elect",
													"--health-probe-bind-address=:8081",
												},
												Env: []EnvVar{
													{
														Name:  "OPERATOR_VERSION",
														Value: flags.operatorVersion,
													},
												},
												Resources: ResourceRequirements{
													Limits: map[string]string{
														"cpu":    "500m",
														"memory": "512Mi",
													},
													Requests: map[string]string{
														"cpu":    "10m",
														"memory": "256Mi",
													},
												},
												SecurityContext: &SecurityContext{
													AllowPrivilegeEscalation: &falseVal,
													ReadOnlyRootFilesystem:   &trueVal,
													Capabilities: &Capabilities{
														Drop: []string{"ALL"},
													},
												},
												LivenessProbe: &Probe{
													HTTPGet: &HTTPGetAction{
														Path: "/healthz",
														Port: 8081,
													},
													InitialDelaySeconds: 15,
													PeriodSeconds:       20,
												},
												ReadinessProbe: &Probe{
													HTTPGet: &HTTPGetAction{
														Path: "/readyz",
														Port: 8081,
													},
													InitialDelaySeconds: 5,
													PeriodSeconds:       10,
												},
												TerminationMessagePolicy: "FallbackToLogsOnError",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			CustomResourceDefinitions: CustomResourceDefinitions{
				Owned: ownedCRDs,
			},
		},
	}
}

func dumpCRDs(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading CRD directory %s: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		var obj map[string]any
		if err := yaml.Unmarshal(data, &obj); err != nil {
			return err
		}
		if err := writeDocument(obj, os.Stdout); err != nil {
			return err
		}
	}
	return nil
}

func writeDocument(obj any, writer io.Writer) error {
	yamlBytes, err := yaml.Marshal(obj)
	if err != nil {
		return err
	}
	if _, err := writer.Write([]byte("---\n")); err != nil {
		return err
	}
	_, err = writer.Write(yamlBytes)
	return err
}
