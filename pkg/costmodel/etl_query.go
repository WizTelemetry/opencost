package costmodel

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/opencost/opencost/core/pkg/log"
	"github.com/opencost/opencost/core/pkg/opencost"
)

type etlAllocationMetadata struct {
	labels               opencost.AllocationLabels
	annotations          opencost.AllocationAnnotations
	namespaceLabels      opencost.AllocationLabels
	namespaceAnnotations opencost.AllocationAnnotations
	services             map[string]struct{}
}

func mergeAllocationSets(current, next *opencost.AllocationSet) (*opencost.AllocationSet, error) {
	if current == nil {
		if next == nil {
			return nil, nil
		}
		return next.Clone(), nil
	}
	if next == nil {
		return current.Clone(), nil
	}

	metadata := make(map[string]*etlAllocationMetadata, current.Length()+next.Length())
	rawAllocationOnly := make(map[string]*opencost.RawAllocationOnlyData, current.Length()+next.Length())
	collectETLAllocationMergeData(current, metadata, rawAllocationOnly)
	collectETLAllocationMergeData(next, metadata, rawAllocationOnly)

	merged, err := current.Clone().Accumulate(next.Clone())
	if err != nil {
		return nil, fmt.Errorf("merging allocation ETL buckets: %w", err)
	}
	restoreETLAllocationMergeData(merged, metadata, rawAllocationOnly)
	return merged, nil
}

func collectETLAllocationMergeData(
	set *opencost.AllocationSet,
	metadata map[string]*etlAllocationMetadata,
	rawAllocationOnly map[string]*opencost.RawAllocationOnlyData,
) {
	if set == nil {
		return
	}

	for key, allocation := range set.Allocations {
		if allocation == nil {
			continue
		}

		allocationMetadata := metadata[key]
		if allocationMetadata == nil {
			allocationMetadata = &etlAllocationMetadata{
				labels:               opencost.AllocationLabels{},
				annotations:          opencost.AllocationAnnotations{},
				namespaceLabels:      opencost.AllocationLabels{},
				namespaceAnnotations: opencost.AllocationAnnotations{},
				services:             map[string]struct{}{},
			}
			metadata[key] = allocationMetadata
		}

		if properties := allocation.Properties; properties != nil {
			for name, value := range properties.Labels {
				allocationMetadata.labels[name] = value
			}
			for name, value := range properties.Annotations {
				allocationMetadata.annotations[name] = value
			}
			for name, value := range properties.NamespaceLabels {
				allocationMetadata.namespaceLabels[name] = value
			}
			for name, value := range properties.NamespaceAnnotations {
				allocationMetadata.namespaceAnnotations[name] = value
			}
			for _, service := range properties.Services {
				allocationMetadata.services[service] = struct{}{}
			}
		}

		if allocation.RawAllocationOnly == nil {
			continue
		}

		raw := rawAllocationOnly[key]
		if raw == nil {
			raw = &opencost.RawAllocationOnlyData{}
			rawAllocationOnly[key] = raw
		}
		raw.CPUCoreUsageMax = math.Max(raw.CPUCoreUsageMax, allocation.RawAllocationOnly.CPUCoreUsageMax)
		raw.RAMBytesUsageMax = math.Max(raw.RAMBytesUsageMax, allocation.RawAllocationOnly.RAMBytesUsageMax)
		if allocation.RawAllocationOnly.GPUUsageMax != nil &&
			(raw.GPUUsageMax == nil || *allocation.RawAllocationOnly.GPUUsageMax > *raw.GPUUsageMax) {
			gpuUsageMax := *allocation.RawAllocationOnly.GPUUsageMax
			raw.GPUUsageMax = &gpuUsageMax
		}
	}
}

func restoreETLAllocationMergeData(
	set *opencost.AllocationSet,
	metadata map[string]*etlAllocationMetadata,
	rawAllocationOnly map[string]*opencost.RawAllocationOnlyData,
) {
	if set == nil {
		return
	}

	for key, allocation := range set.Allocations {
		if allocation == nil {
			continue
		}
		if allocation.Properties == nil {
			allocation.Properties = &opencost.AllocationProperties{}
		}

		if restored := metadata[key]; restored != nil {
			allocation.Properties.Labels = copyAllocationLabels(restored.labels)
			allocation.Properties.Annotations = copyAllocationAnnotations(restored.annotations)
			allocation.Properties.NamespaceLabels = copyAllocationLabels(restored.namespaceLabels)
			allocation.Properties.NamespaceAnnotations = copyAllocationAnnotations(restored.namespaceAnnotations)

			services := make([]string, 0, len(restored.services))
			for service := range restored.services {
				services = append(services, service)
			}
			sort.Strings(services)
			allocation.Properties.Services = services
		}

		if raw := rawAllocationOnly[key]; raw != nil {
			allocation.RawAllocationOnly = raw.Clone()
		}
	}
}

func copyAllocationLabels(labels opencost.AllocationLabels) opencost.AllocationLabels {
	if len(labels) == 0 {
		return nil
	}
	copy := make(opencost.AllocationLabels, len(labels))
	for name, value := range labels {
		copy[name] = value
	}
	return copy
}

func copyAllocationAnnotations(annotations opencost.AllocationAnnotations) opencost.AllocationAnnotations {
	if len(annotations) == 0 {
		return nil
	}
	copy := make(opencost.AllocationAnnotations, len(annotations))
	for name, value := range annotations {
		copy[name] = value
	}
	return copy
}

func (cm *CostModel) allocationSetWithETL(start, end time.Time, resolution time.Duration, pushdown *allocationQueryPushdown) (*opencost.AllocationSet, bool, error) {
	if cm == nil || cm.ETL == nil || pushdown != nil {
		return nil, false, nil
	}

	revision, err := cm.ETL.revision()
	if err != nil {
		recordQueryComputeError("etl_allocation_compose")
		return nil, false, err
	}

	parts, coverage := cm.ETL.partition(etlAllocationKind, start, end, revision)
	if coverage < cm.ETL.minCoverage {
		recordQueryCacheResult("etl_allocation_compose", false)
		return nil, false, nil
	}

	var merged *opencost.AllocationSet
	usedETL := false
	for _, part := range parts {
		var set *opencost.AllocationSet
		if part.FromETL {
			cached, found, err := cm.ETL.readAllocation(part.Start, part.End, part.Resolution, revision)
			if err != nil {
				log.Warnf("ETL allocation bucket read failed; computing %s live: %v", opencost.NewClosedWindow(part.Start, part.End), err)
				found = false
			}
			if found {
				set = cached
				usedETL = true
			}
		}

		if set == nil {
			computed, _, err := cm.computeAllocationStepUncached(part.Start, part.End, resolution, pushdown)
			if err != nil {
				return nil, false, err
			}
			set = computed
		}

		var err error
		merged, err = mergeAllocationSets(merged, set)
		if err != nil {
			return nil, false, err
		}
	}

	if !usedETL || merged == nil {
		recordQueryCacheResult("etl_allocation_compose", false)
		return nil, false, nil
	}

	currentRevision, err := cm.ETL.revision()
	if err != nil {
		recordQueryComputeError("etl_allocation_compose")
		return nil, false, err
	}
	if currentRevision != revision {
		recordQueryCacheResult("etl_allocation_compose", false)
		return nil, false, fmt.Errorf("pricing revision changed while composing allocation ETL data")
	}

	merged.Window = opencost.NewClosedWindow(start, end)
	recordQueryCacheResult("etl_allocation_compose", true)
	return merged, true, nil
}
