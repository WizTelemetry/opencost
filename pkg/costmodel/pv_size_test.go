package costmodel

import (
	"testing"
	"time"

	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/opencost/opencost/core/pkg/source"
	"github.com/opencost/opencost/core/pkg/util"
	"github.com/opencost/opencost/pkg/cloud/provider"
	"github.com/opencost/opencost/pkg/config"
)

func TestResolvePVSizeBytes(t *testing.T) {
	tests := []struct {
		name      string
		preferred float64
		fallback  float64
		want      float64
	}{
		{name: "preferred valid", preferred: 10 * GiB, fallback: 20 * GiB, want: 10 * GiB},
		{name: "fallback valid", preferred: 100 * PiB, fallback: 20 * GiB, want: 20 * GiB},
		{name: "both anomalous", preferred: 100 * PiB, fallback: 200 * PiB, want: 0},
		{name: "both invalid", preferred: 0, fallback: -1, want: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolvePVSizeBytes(test.preferred, test.fallback); got != test.want {
				t.Fatalf("resolvePVSizeBytes() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestApplyPVCBytesRequestedFallsBackFromAnomalousRequest(t *testing.T) {
	cluster := "cluster-a"
	namespace := "namespace-a"
	claim := "claim-a"
	key := newPVCKey(cluster, namespace, claim)
	pvcMap := map[pvcKey]*pvc{
		key: {
			Volume: &pv{Bytes: 20 * GiB},
		},
	}
	results := []*source.PVCBytesRequestedResult{
		{
			Cluster:               cluster,
			Namespace:             namespace,
			PersistentVolumeClaim: claim,
			Data:                  []*util.Vector{{Value: 100 * PiB}},
		},
	}

	applyPVCBytesRequested(pvcMap, results)

	if got, want := pvcMap[key].Bytes, 20*GiB; got != want {
		t.Fatalf("PVC bytes = %v, want valid PV capacity %v", got, want)
	}
}

func TestApplyPVBytesRejectsAnomalousSize(t *testing.T) {
	cluster := "cluster-a"
	volume := "pv-a"
	key := newPVKey(cluster, volume)
	pvMap := map[pvKey]*pv{
		key: {
			Cluster: cluster,
			Name:    volume,
		},
	}
	results := []*source.PVBytesResult{
		{
			Cluster:          cluster,
			PersistentVolume: volume,
			Data:             []*util.Vector{{Value: 100 * PiB}},
		},
	}

	applyPVBytes(pvMap, results)

	if got := pvMap[key].Bytes; got != 0 {
		t.Fatalf("PV bytes = %v, want anomalous size rejected", got)
	}
}

func TestPVCostsDropsAnomalousPVSizes(t *testing.T) {
	cluster := "cluster-a"
	validKey := DiskIdentifier{Cluster: cluster, Name: "valid-pv"}
	invalidKey := DiskIdentifier{Cluster: cluster, Name: "invalid-pv"}
	diskMap := map[DiskIdentifier]*Disk{
		validKey: {
			Cluster:   cluster,
			Name:      validKey.Name,
			Breakdown: &ClusterCostsBreakdown{},
		},
		invalidKey: {
			Cluster:   cluster,
			Name:      invalidKey.Name,
			Breakdown: &ClusterCostsBreakdown{},
		},
	}
	pvSizes := []*source.PVBytesResult{
		{
			Cluster:          cluster,
			PersistentVolume: validKey.Name,
			Data:             []*util.Vector{{Value: 20 * GiB}},
		},
		{
			Cluster:          cluster,
			PersistentVolume: invalidKey.Name,
			Data:             []*util.Vector{{Value: 100 * PiB}},
		},
	}
	start := time.Date(2026, time.November, 1, 15, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	window := opencost.NewClosedWindow(start, end)
	cp := &provider.CustomProvider{
		Config: provider.NewProviderConfig(config.NewConfigFileManager(nil), ""),
	}

	pvCosts(diskMap, time.Hour, nil, pvSizes, nil, nil, nil, nil, cp, window)

	if _, ok := diskMap[invalidKey]; ok {
		t.Fatal("anomalous PV must not be exposed as a disk asset")
	}
	if got, want := diskMap[validKey].Bytes, 20*GiB; got != want {
		t.Fatalf("valid PV bytes = %v, want %v", got, want)
	}
}
