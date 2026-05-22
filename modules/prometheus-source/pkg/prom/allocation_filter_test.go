package prom

import "testing"

func TestAllocationNamespaceFilter(t *testing.T) {
	querier := &PrometheusMetricsQuerier{
		promConfig: &OpenCostPrometheusConfig{
			ClusterFilter: `cluster_id="cluster-a"`,
			ClusterLabel:  "cluster_id",
		},
		allocationFilter: allocationQueryFilter{
			namespace: "kubecost",
		},
	}

	got := querier.allocationNamespaceFilter()
	want := `cluster_id="cluster-a", namespace="kubecost"`
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestAllocationClusterFilter(t *testing.T) {
	querier := &PrometheusMetricsQuerier{
		promConfig: &OpenCostPrometheusConfig{
			ClusterLabel: "cluster_id",
		},
		allocationFilter: allocationQueryFilter{
			cluster: "cluster-b",
		},
	}

	got := querier.allocationClusterFilter()
	want := `cluster_id="cluster-b"`
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestAllocationFilterEscapesValues(t *testing.T) {
	querier := &PrometheusMetricsQuerier{
		promConfig: &OpenCostPrometheusConfig{
			ClusterLabel: "cluster_id",
		},
		allocationFilter: allocationQueryFilter{
			namespace: `team"one`,
		},
	}

	got := querier.allocationNamespaceFilter()
	want := `namespace="team\"one"`
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
