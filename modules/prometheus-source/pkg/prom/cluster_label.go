package prom

import (
	"context"
	"fmt"
	"time"

	"github.com/opencost/opencost/core/pkg/log"
	prometheus "github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

const (
	defaultQueryClusterLabel     = "cluster_id"
	kubeSphereQueryClusterLabel  = "cluster"
	clusterLabelProbeTimeout     = 15 * time.Second
	kubeStateMetricsProbeJobName = "kube-state-metrics"
	kubeletProbeJobName          = "kubelet"
)

type clusterLabelCoverage struct {
	KubeStateMetrics int64
	Kubelet          int64
}

func (c clusterLabelCoverage) total() int64 {
	return c.KubeStateMetrics + c.Kubelet
}

func (c clusterLabelCoverage) complete() bool {
	return c.KubeStateMetrics > 0 && c.Kubelet > 0
}

type clusterLabelSelection struct {
	Label              string
	ConfiguredCoverage clusterLabelCoverage
	SelectedCoverage   clusterLabelCoverage
	Err                error
}

type clusterLabelProbe struct {
	name  string
	job   string
	query string
	set   func(*clusterLabelCoverage, int64)
}

var clusterLabelProbes = []clusterLabelProbe{
	{
		name:  "kube-state-metrics",
		job:   kubeStateMetricsProbeJobName,
		query: `count(kube_pod_container_resource_requests{job=%q, %s!=""})`,
		set: func(coverage *clusterLabelCoverage, value int64) {
			coverage.KubeStateMetrics = value
		},
	},
	{
		name:  "kubelet",
		job:   kubeletProbeJobName,
		query: `count(container_memory_working_set_bytes{job=%q, %s!=""})`,
		set: func(coverage *clusterLabelCoverage, value int64) {
			coverage.Kubelet = value
		},
	},
}

func selectQueryClusterLabel(client prometheus.Client, config *OpenCostPrometheusConfig) clusterLabelSelection {
	configuredLabel := config.ClusterLabel
	if configuredLabel == "" {
		configuredLabel = defaultQueryClusterLabel
	}

	selection := clusterLabelSelection{Label: configuredLabel}
	if configuredLabel == kubeSphereQueryClusterLabel {
		coverage, err := probeClusterLabel(client, configuredLabel)
		selection.ConfiguredCoverage = coverage
		selection.SelectedCoverage = coverage
		selection.Err = err
		return selection
	}

	configuredCoverage, configuredErr := probeClusterLabel(client, configuredLabel)
	selectedCoverage, selectedErr := probeClusterLabel(client, kubeSphereQueryClusterLabel)
	selection.ConfiguredCoverage = configuredCoverage

	if configuredErr != nil {
		selection.Err = fmt.Errorf("probing configured label %q: %w", configuredLabel, configuredErr)
	}
	if selectedErr != nil {
		if selection.Err == nil {
			selection.Err = fmt.Errorf("probing fallback label %q: %w", kubeSphereQueryClusterLabel, selectedErr)
		} else {
			selection.Err = fmt.Errorf("%v; probing fallback label %q: %w", selection.Err, kubeSphereQueryClusterLabel, selectedErr)
		}
	}

	switch {
	case configuredCoverage.complete():
		selection.Label = configuredLabel
		selection.SelectedCoverage = configuredCoverage
	case selectedCoverage.complete():
		selection.Label = kubeSphereQueryClusterLabel
		selection.SelectedCoverage = selectedCoverage
	case configuredCoverage.total() == 0 && selectedCoverage.total() > 0:
		selection.Label = kubeSphereQueryClusterLabel
		selection.SelectedCoverage = selectedCoverage
	default:
		selection.Label = configuredLabel
		selection.SelectedCoverage = configuredCoverage
	}

	log.Infof(
		"Prometheus cluster label probe: configured[%s]={ksm:%d,kubelet:%d} fallback[%s]={ksm:%d,kubelet:%d} selected=%s",
		configuredLabel,
		configuredCoverage.KubeStateMetrics,
		configuredCoverage.Kubelet,
		kubeSphereQueryClusterLabel,
		selectedCoverage.KubeStateMetrics,
		selectedCoverage.Kubelet,
		selection.Label,
	)

	return selection
}

func probeClusterLabel(client prometheus.Client, clusterLabel string) (clusterLabelCoverage, error) {
	var coverage clusterLabelCoverage
	api := v1.NewAPI(client)

	for _, probe := range clusterLabelProbes {
		ctx, cancel := context.WithTimeout(context.Background(), clusterLabelProbeTimeout)
		query := fmt.Sprintf(probe.query, probe.job, clusterLabel)
		value, warnings, err := api.Query(ctx, query, time.Now())
		cancel()
		if err != nil {
			return coverage, fmt.Errorf("%s query failed: %w", probe.name, err)
		}
		for _, warning := range warnings {
			log.Warnf("Prometheus cluster label probe warning for %s: %s", probe.name, warning)
		}

		count, err := clusterLabelSampleCount(value)
		if err != nil {
			return coverage, fmt.Errorf("%s query returned an unexpected value: %w", probe.name, err)
		}
		probe.set(&coverage, count)
	}

	return coverage, nil
}

func clusterLabelSampleCount(value model.Value) (int64, error) {
	switch typed := value.(type) {
	case model.Vector:
		var total int64
		for _, sample := range typed {
			total += int64(sample.Value)
		}
		return total, nil
	case *model.Scalar:
		return int64(typed.Value), nil
	default:
		return 0, fmt.Errorf("unsupported Prometheus result type %T", value)
	}
}
