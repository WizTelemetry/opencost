package costmodel

import (
	"testing"
	"time"

	"github.com/opencost/opencost/core/pkg/filter/allocation"
	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/patrickmn/go-cache"
)

func TestAllocationPushdownFromFilter(t *testing.T) {
	parser := allocation.NewAllocationFilterParser()

	tests := []struct {
		name          string
		filter        string
		wantCluster   string
		wantNamespace string
		wantPushdown  bool
	}{
		{
			name:          "cluster and namespace equality",
			filter:        `cluster:"prod"+namespace:"kubecost"`,
			wantCluster:   "prod",
			wantNamespace: "kubecost",
			wantPushdown:  true,
		},
		{
			name:          "namespace with unsupported filter still pushes safe operand",
			filter:        `namespace:"kubecost"+pod~:"cost"`,
			wantNamespace: "kubecost",
			wantPushdown:  true,
		},
		{
			name:         "or expression is not pushed",
			filter:       `namespace:"kubecost"|namespace:"monitoring"`,
			wantPushdown: false,
		},
		{
			name:         "not equals is not pushed",
			filter:       `namespace!:"kube-system"`,
			wantPushdown: false,
		},
		{
			name:         "conflicting equality is not pushed",
			filter:       `namespace:"kubecost"+namespace:"monitoring"`,
			wantPushdown: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			node, err := parser.Parse(tc.filter)
			if err != nil {
				t.Fatalf("unexpected parse error: %s", err)
			}

			got := allocationPushdownFromFilter(node)
			if !tc.wantPushdown {
				if got != nil {
					t.Fatalf("expected no pushdown, got %#v", got)
				}
				return
			}

			if got == nil {
				t.Fatalf("expected pushdown")
			}
			if got.cluster != tc.wantCluster {
				t.Fatalf("expected cluster %q, got %q", tc.wantCluster, got.cluster)
			}
			if got.namespace != tc.wantNamespace {
				t.Fatalf("expected namespace %q, got %q", tc.wantNamespace, got.namespace)
			}
		})
	}
}

func TestQueryAllocationFiltersDuringAggregation(t *testing.T) {
	start := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	allocSet := opencost.NewAllocationSet(start, end)
	mustInsertAllocation(t, allocSet, &opencost.Allocation{
		Name:       "cluster1/node1/ns-a/pod-a/container-a",
		Start:      start,
		End:        end,
		CPUCost:    10,
		Properties: &opencost.AllocationProperties{Cluster: "cluster1", Node: "node1", Namespace: "ns-a", Pod: "pod-a", Container: "container-a"},
	})
	mustInsertAllocation(t, allocSet, &opencost.Allocation{
		Name:       "cluster1/node2/ns-a/pod-b/container-b",
		Start:      start,
		End:        end,
		CPUCost:    30,
		Properties: &opencost.AllocationProperties{Cluster: "cluster1", Node: "node2", Namespace: "ns-a", Pod: "pod-b", Container: "container-b"},
	})
	mustInsertAllocation(t, allocSet, &opencost.Allocation{
		Name:       "cluster1/__idle__",
		Start:      start,
		End:        end,
		CPUCost:    10,
		Properties: &opencost.AllocationProperties{Cluster: "cluster1"},
	})

	stepCache := cache.New(5*time.Minute, 10*time.Minute)
	stepCache.Set(allocStepCacheKey(start, end, 0), &allocStepCacheValue{allocSet: allocSet}, cache.DefaultExpiration)

	cm := &CostModel{
		BatchDuration:  time.Hour,
		allocStepCache: stepCache,
	}

	asr, err := cm.QueryAllocation(
		opencost.NewClosedWindow(start, end),
		time.Hour,
		[]string{opencost.AllocationNamespaceProp},
		false,
		false,
		false,
		false,
		false,
		opencost.AccumulateOptionNone,
		true,
		`node:"node1"`,
	)
	if err != nil {
		t.Fatalf("unexpected query error: %s", err)
	}
	if len(asr.Allocations) != 1 {
		t.Fatalf("expected one allocation set, got %d", len(asr.Allocations))
	}

	got := asr.Allocations[0].Get("ns-a")
	if got == nil {
		t.Fatalf("expected filtered namespace allocation")
	}
	if got.TotalCost() != 12.5 {
		t.Fatalf("expected filtered allocation to keep full-set idle weighting cost 12.5, got %.2f", got.TotalCost())
	}
}

func mustInsertAllocation(t *testing.T, allocSet *opencost.AllocationSet, alloc *opencost.Allocation) {
	t.Helper()

	if err := allocSet.Insert(alloc); err != nil {
		t.Fatalf("failed to insert allocation %q: %s", alloc.Name, err)
	}
}

// TestQueryAllocationPushdownableFilter verifies that pushdownable filters
// (cluster="x", namespace="x") produce the same correct results as
// non-pushdownable filters — i.e. the filter is applied as a post-filter
// during AggregateBy, not as a PromQL-level pushdown. This guards against
// regressions where pushdown re-enablement breaks data consistency.
func TestQueryAllocationPushdownableFilter(t *testing.T) {
	start := time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	allocSet := opencost.NewAllocationSet(start, end)
	mustInsertAllocation(t, allocSet, &opencost.Allocation{
		Name:       "cluster1/node1/ns-a/pod-a/container-a",
		Start:      start,
		End:        end,
		CPUCost:    10,
		Properties: &opencost.AllocationProperties{Cluster: "cluster1", Node: "node1", Namespace: "ns-a", Pod: "pod-a", Container: "container-a"},
	})
	mustInsertAllocation(t, allocSet, &opencost.Allocation{
		Name:       "cluster1/node2/ns-b/pod-b/container-b",
		Start:      start,
		End:        end,
		CPUCost:    30,
		Properties: &opencost.AllocationProperties{Cluster: "cluster1", Node: "node2", Namespace: "ns-b", Pod: "pod-b", Container: "container-b"},
	})
	mustInsertAllocation(t, allocSet, &opencost.Allocation{
		Name:       "cluster1/__idle__",
		Start:      start,
		End:        end,
		CPUCost:    10,
		Properties: &opencost.AllocationProperties{Cluster: "cluster1"},
	})

	stepCache := cache.New(5*time.Minute, 10*time.Minute)
	stepCache.Set(allocStepCacheKey(start, end, 0), &allocStepCacheValue{allocSet: allocSet}, cache.DefaultExpiration)

	cm := &CostModel{
		BatchDuration:  time.Hour,
		allocStepCache: stepCache,
	}

	t.Run("cluster pushdownable filter", func(t *testing.T) {
		asr, err := cm.QueryAllocation(
			opencost.NewClosedWindow(start, end),
			time.Hour,
			[]string{opencost.AllocationNamespaceProp},
			false, false, false, false, false,
			opencost.AccumulateOptionNone,
			true,
			`cluster:"cluster1"`,
		)
		if err != nil {
			t.Fatalf("unexpected query error: %s", err)
		}
		if len(asr.Allocations) != 1 {
			t.Fatalf("expected one allocation set, got %d", len(asr.Allocations))
		}
		// Both ns-a and ns-b are in cluster1, idle should be shared
		nsA := asr.Allocations[0].Get("ns-a")
		if nsA == nil {
			t.Fatal("expected ns-a allocation")
		}
		nsB := asr.Allocations[0].Get("ns-b")
		if nsB == nil {
			t.Fatal("expected ns-b allocation")
		}
		// Total cost should be 10+30+10=50, and both namespaces should be present
		total := nsA.TotalCost() + nsB.TotalCost()
		if total < 40 || total > 50 {
			t.Fatalf("expected total cost between 40 and 50, got %.2f", total)
		}
	})

	t.Run("namespace pushdownable filter", func(t *testing.T) {
		asr, err := cm.QueryAllocation(
			opencost.NewClosedWindow(start, end),
			time.Hour,
			[]string{opencost.AllocationNamespaceProp},
			false, false, false, false, false,
			opencost.AccumulateOptionNone,
			true,
			`namespace:"ns-a"`,
		)
		if err != nil {
			t.Fatalf("unexpected query error: %s", err)
		}
		if len(asr.Allocations) != 1 {
			t.Fatalf("expected one allocation set, got %d", len(asr.Allocations))
		}
		got := asr.Allocations[0].Get("ns-a")
		if got == nil {
			t.Fatal("expected filtered ns-a allocation")
		}
		// ns-a should be present, ns-b should be absent
		if asr.Allocations[0].Get("ns-b") != nil {
			t.Fatal("expected ns-b to be filtered out")
		}
		// Cost should be ns-a's direct cost (10) + idle share of idle(10)
		// weighted by ns-a's share of non-idle cost (10/40 = 0.25)
		// idle share = 10 * 0.25 = 2.5, total = 10 + 2.5 = 12.5
		if got.TotalCost() != 12.5 {
			t.Fatalf("expected filtered ns-a cost 12.5, got %.2f", got.TotalCost())
		}
	})
}
