package prom

import (
	"strconv"
	"strings"

	"github.com/opencost/opencost/core/pkg/source"
)

type allocationQueryFilter struct {
	cluster   string
	namespace string
}

func (pds *PrometheusMetricsQuerier) WithAllocationFilter(cluster, namespace string) source.MetricsQuerier {
	if cluster == "" && namespace == "" {
		return pds
	}

	return &PrometheusMetricsQuerier{
		promConfig:   pds.promConfig,
		promClient:   pds.promClient,
		promContexts: pds.promContexts,
		allocationFilter: allocationQueryFilter{
			cluster:   cluster,
			namespace: namespace,
		},
	}
}

func (pds *PrometheusMetricsQuerier) allocationClusterFilter() string {
	if pds.allocationFilter.cluster == "" {
		return pds.promConfig.ClusterFilter
	}
	return appendPromLabelMatcher(pds.promConfig.ClusterFilter, pds.promConfig.ClusterLabel, pds.allocationFilter.cluster)
}

func (pds *PrometheusMetricsQuerier) allocationNamespaceFilter() string {
	return appendPromLabelMatcher(pds.allocationClusterFilter(), "namespace", pds.allocationFilter.namespace)
}

func appendPromLabelMatcher(base, label, value string) string {
	if value == "" {
		return base
	}

	matcher := label + "=" + strconv.Quote(value)
	if strings.TrimSpace(base) == "" {
		return matcher
	}
	return base + ", " + matcher
}
