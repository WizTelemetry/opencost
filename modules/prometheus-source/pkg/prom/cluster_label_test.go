package prom

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
)

type clusterLabelPromClient struct {
	responses [][]byte
	current   int
}

func (c *clusterLabelPromClient) URL(string, map[string]string) *url.URL {
	return &url.URL{}
}

func (c *clusterLabelPromClient) Do(context.Context, *http.Request) (*http.Response, []byte, error) {
	body := c.responses[c.current]
	c.current++
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}, body, nil
}

func promVectorResponse(value string) []byte {
	return []byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1,"` + value + `"]}]}}`)
}

func promEmptyVectorResponse() []byte {
	return []byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`)
}

func TestSelectQueryClusterLabelPrefersCompleteConfiguredCoverage(t *testing.T) {
	client := &clusterLabelPromClient{
		responses: [][]byte{
			promVectorResponse("10"),
			promVectorResponse("20"),
			promVectorResponse("10"),
			promVectorResponse("20"),
		},
	}

	selection := selectQueryClusterLabel(client, &OpenCostPrometheusConfig{ClusterLabel: "cluster_id"})

	if selection.Label != "cluster_id" {
		t.Fatalf("expected configured label to win, got %q", selection.Label)
	}
	if !selection.SelectedCoverage.complete() {
		t.Fatalf("expected complete coverage, got %+v", selection.SelectedCoverage)
	}
	if selection.Err != nil {
		t.Fatalf("unexpected probe error: %v", selection.Err)
	}
}

func TestSelectQueryClusterLabelFallsBackToCluster(t *testing.T) {
	client := &clusterLabelPromClient{
		responses: [][]byte{
			promEmptyVectorResponse(),
			promEmptyVectorResponse(),
			promVectorResponse("374"),
			promVectorResponse("630"),
		},
	}

	selection := selectQueryClusterLabel(client, &OpenCostPrometheusConfig{ClusterLabel: "cluster_id"})

	if selection.Label != "cluster" {
		t.Fatalf("expected cluster fallback, got %q", selection.Label)
	}
	if selection.ConfiguredCoverage.total() != 0 {
		t.Fatalf("expected no configured coverage, got %+v", selection.ConfiguredCoverage)
	}
	if selection.SelectedCoverage.KubeStateMetrics != 374 || selection.SelectedCoverage.Kubelet != 630 {
		t.Fatalf("unexpected fallback coverage: %+v", selection.SelectedCoverage)
	}
}

func TestSelectQueryClusterLabelKeepsConfiguredLabelWhenCoverageIsInconclusive(t *testing.T) {
	client := &clusterLabelPromClient{
		responses: [][]byte{
			promVectorResponse("5"),
			promEmptyVectorResponse(),
			promVectorResponse("5"),
			promEmptyVectorResponse(),
		},
	}

	selection := selectQueryClusterLabel(client, &OpenCostPrometheusConfig{ClusterLabel: "cluster_id"})

	if selection.Label != "cluster_id" {
		t.Fatalf("expected configured label to remain, got %q", selection.Label)
	}
}

func TestSetEffectiveClusterLabelRebuildsClusterFilter(t *testing.T) {
	t.Setenv("CLUSTER_ID", "cluster-a")
	t.Setenv("CURRENT_CLUSTER_ID_FILTER_ENABLED", "true")

	config := &OpenCostPrometheusConfig{
		ClusterLabel:  "cluster_id",
		ClusterFilter: `cluster_id="cluster-a"`,
	}
	config.SetEffectiveClusterLabel("cluster")

	if config.ClusterLabel != "cluster" {
		t.Fatalf("expected cluster label to change, got %q", config.ClusterLabel)
	}
	if config.ClusterFilter != `cluster="cluster-a"` {
		t.Fatalf("expected cluster filter to be rebuilt, got %q", config.ClusterFilter)
	}
}

func TestClusterInfoQueryUsesEffectiveClusterFilter(t *testing.T) {
	config := &OpenCostPrometheusConfig{ClusterFilter: `cluster="cluster-a"`}

	got := clusterInfoQuery(config, " offset 5m")
	want := `kubecost_cluster_info{cluster="cluster-a"} offset 5m`
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
