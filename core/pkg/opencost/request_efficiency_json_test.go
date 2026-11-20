package opencost

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"
	"time"
)

func TestRequestEfficiencyJSON(t *testing.T) {
	tests := []struct {
		name                          string
		cpuRequest, cpuUsage, cpuCost float64
		ramRequest, ramUsage, ramCost float64
		cpu, ram, total               *float64
	}{
		{name: "no requests or usage"},
		{name: "no requests with usage and cost", cpuUsage: 1, cpuCost: 10, ramUsage: 2, ramCost: 5},
		{name: "idle", cpuRequest: 1, cpuCost: 2, cpu: floatPtr(0), total: floatPtr(0)},
		{name: "overuse", cpuRequest: 1, cpuUsage: 1.37, cpuCost: 2, cpu: floatPtr(1.37), total: floatPtr(1.37)},
		{name: "exclude undeclared resource cost", cpuUsage: 1, cpuCost: 100, ramRequest: 2, ramUsage: 1, ramCost: 2, ram: floatPtr(0.5), total: floatPtr(0.5)},
		{name: "both resources weighted", cpuRequest: 1, cpuUsage: 1.5, cpuCost: 1, ramRequest: 2, ramUsage: 1, ramCost: 3, cpu: floatPtr(1.5), ram: floatPtr(0.5), total: floatPtr(0.75)},
		{name: "no cost", cpuRequest: 1, cpuUsage: 1, cpu: floatPtr(1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Allocation{CPUCoreRequestAverage: tt.cpuRequest, CPUCoreUsageAverage: tt.cpuUsage, CPUCost: tt.cpuCost, RAMBytesRequestAverage: tt.ramRequest, RAMBytesUsageAverage: tt.ramUsage, RAMCost: tt.ramCost}
			data, err := json.Marshal(a)
			if err != nil {
				t.Fatal(err)
			}
			var allocation struct {
				CPU   *float64 `json:"cpuEfficiency"`
				RAM   *float64 `json:"ramEfficiency"`
				Total *float64 `json:"totalEfficiency"`
			}
			if err := json.Unmarshal(data, &allocation); err != nil {
				t.Fatal(err)
			}
			checkEfficiency(t, "cpu", allocation.CPU, tt.cpu)
			checkEfficiency(t, "ram", allocation.RAM, tt.ram)
			checkEfficiency(t, "total", allocation.Total, tt.total)

			sa := &SummaryAllocation{CPUCoreRequestAverage: tt.cpuRequest, CPUCoreUsageAverage: tt.cpuUsage, CPUCost: tt.cpuCost, RAMBytesRequestAverage: tt.ramRequest, RAMBytesUsageAverage: tt.ramUsage, RAMCost: tt.ramCost}
			data, err = json.Marshal(sa)
			if err != nil {
				t.Fatal(err)
			}
			var summary struct {
				Efficiency *float64 `json:"efficiency"`
			}
			if err := json.Unmarshal(data, &summary); err != nil {
				t.Fatal(err)
			}
			checkEfficiency(t, "summary", summary.Efficiency, tt.total)
		})
	}
}

func checkEfficiency(t *testing.T, name string, got, want *float64) {
	t.Helper()
	if (got == nil) != (want == nil) || (got != nil && *got != *want) {
		t.Errorf("%s efficiency: got %v, want %v", name, got, want)
	}
}

func TestRequestEfficiencyAggregation(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	valid := &Allocation{Name: "valid", Start: start, End: end, CPUCoreRequestAverage: 2, CPUCoreUsageAverage: 1, CPUCost: 2}
	invalid := &Allocation{Name: "invalid", Start: start, End: end, CPUCoreUsageAverage: 10, CPUCost: 100}
	group, err := valid.Add(invalid)
	if err != nil {
		t.Fatal(err)
	}
	group = group.Clone()
	checkEfficiency(t, "group CPU", requestEfficiency(group.requestStats().cpuRequest, group.requestStats().cpuUsage), floatPtr(0.5))
	checkEfficiency(t, "group total", group.requestStats().total(), floatPtr(0.5))
	data, err := json.Marshal(group)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		CPU   *float64 `json:"cpuEfficiency"`
		Total *float64 `json:"totalEfficiency"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	checkEfficiency(t, "group JSON CPU", result.CPU, floatPtr(0.5))
	checkEfficiency(t, "group JSON total", result.Total, floatPtr(0.5))

	// A later interval contributes only its declared resource and is not an
	// average of the displayed row percentages.
	later := &Allocation{Name: "later", Start: end, End: end.Add(time.Hour), CPUCoreRequestAverage: 1, CPUCoreUsageAverage: 1.5, CPUCost: 1}
	combined, err := group.Add(later)
	if err != nil {
		t.Fatal(err)
	}
	checkEfficiency(t, "combined", combined.requestStats().total(), floatPtr(0.83333))

	summary := NewSummaryAllocation(group, false, false).Clone()
	if err := summary.Add(NewSummaryAllocation(later, false, false)); err != nil {
		t.Fatal(err)
	}
	checkEfficiency(t, "summary", summary.RequestEfficiency(), floatPtr(0.83333))
	checkEfficiency(t, "summary response", summary.ToResponse().TotalEfficiency, floatPtr(0.83333))
}

func TestGroupedAllocationJSONRoundTrip(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	valid := &Allocation{Name: "valid", Start: start, End: end, CPUCoreRequestAverage: 2, CPUCoreUsageAverage: 1, CPUCost: 2}
	invalid := &Allocation{Name: "invalid", Start: start, End: end, CPUCoreUsageAverage: 2, CPUCost: 2}
	combined, err := valid.Add(invalid)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(combined)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Allocation
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	checkEfficiency(t, "allocation JSON", decoded.requestStats().total(), floatPtr(0.5))
	again, err := decoded.Add(valid)
	if err != nil {
		t.Fatal(err)
	}
	checkEfficiency(t, "allocation re-aggregate", again.requestStats().total(), floatPtr(0.5))
	sa := NewSummaryAllocation(combined, false, false)
	data, err = json.Marshal(sa)
	if err != nil {
		t.Fatal(err)
	}
	var summary SummaryAllocation
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	checkEfficiency(t, "summary JSON", summary.RequestEfficiency(), floatPtr(0.5))
	if err := summary.Add(NewSummaryAllocation(valid, false, false)); err != nil {
		t.Fatal(err)
	}
	checkEfficiency(t, "summary re-aggregate", summary.RequestEfficiency(), floatPtr(0.5))
	if err := json.Unmarshal([]byte(`{"name":"legacy"}`), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.requestEfficiencyStats != nil {
		t.Fatal("legacy JSON retained old summary state")
	}
	if err := json.Unmarshal([]byte(`{"name":"legacy"}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.requestEfficiencyStats != nil {
		t.Fatal("legacy JSON retained old allocation state")
	}
}

func TestRequestEfficiencyCostChanges(t *testing.T) {
	s := &SummaryAllocation{CPUCoreRequestAverage: 2, CPUCoreUsageAverage: 1, CPUCost: 1, RAMBytesRequestAverage: 1, RAMBytesUsageAverage: 1, RAMCost: 1}
	s.requestEfficiencyStats = s.requestStats()
	s.addSharedIdleToRequestStats(2, 0)
	s.CPUCost += 2
	checkEfficiency(t, "idle sharing", s.RequestEfficiency(), floatPtr(0.625))
	a := &Allocation{CPUCoreRequestAverage: 2, CPUCoreUsageAverage: 1, CPUCost: 1, CPUCostAdjustment: 2, RAMBytesRequestAverage: 1, RAMBytesUsageAverage: 1, RAMCost: 1}
	a.requestEfficiencyStats = a.requestStats()
	checkEfficiency(t, "before reset", a.requestStats().total(), floatPtr(0.625))
	a.ResetAdjustments()
	checkEfficiency(t, "after reset", a.requestStats().total(), floatPtr(0.75))
	a.requestEfficiencyStats.cpuUsage = math.NaN()
	a.SanitizeNaN()
	checkEfficiency(t, "after sanitize", a.requestStats().total(), floatPtr(1))
}

func TestShareEvenWithZeroCostRequestEfficiency(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	makeSet := func() *AllocationSet {
		set := NewAllocationSet(start, end)
		workload := &Allocation{Name: "cluster/workload/a", Properties: &AllocationProperties{Cluster: "cluster", Namespace: "workload"}, Start: start, End: end, CPUCoreRequestAverage: 1, CPUCoreUsageAverage: 0.5, RAMBytesRequestAverage: 1, RAMBytesUsageAverage: 1, RAMCost: 1}
		workload.requestEfficiencyStats = workload.requestStats()
		idle := &Allocation{Name: "cluster/" + IdleSuffix, Properties: &AllocationProperties{Cluster: "cluster"}, Start: start, End: end, CPUCost: 2}
		if err := set.Insert(workload); err != nil {
			t.Fatal(err)
		}
		if err := set.Insert(idle); err != nil {
			t.Fatal(err)
		}
		return set
	}
	summary := NewSummaryAllocationSet(makeSet(), nil, nil, false, false)
	store := NewMemoryTotalsStore()
	store.SetAllocationTotalsByCluster(start, end, map[string]*AllocationTotals{"cluster": {Count: 1, RAMCost: 1}})
	if err := summary.AggregateBy([]string{"namespace"}, &AllocationAggregationOptions{ShareIdle: ShareEven, AllocationTotalsStore: store}); err != nil {
		t.Fatal(err)
	}
	row := summary.SummaryAllocations["workload"]
	if row == nil {
		t.Fatal("missing summary")
	}
	checkEfficiency(t, "summary", row.RequestEfficiency(), floatPtr(0.66667))
}

func TestRequestEfficiencyNonFiniteJSON(t *testing.T) {
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, field := range []string{"request", "usage", "cost", "adjustment"} {
			t.Run(fmt.Sprintf("%v/%s", bad, field), func(t *testing.T) {
				start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
				end := start.Add(time.Hour)
				a := &Allocation{Name: "a", Start: start, End: end, CPUCoreRequestAverage: 2, CPUCoreUsageAverage: 1, CPUCost: 2, RAMBytesRequestAverage: 2, RAMBytesUsageAverage: 1, RAMCost: 2}
				group, err := a.Add(a)
				if err != nil {
					t.Fatal(err)
				}
				s := group.requestEfficiencyStats
				switch field {
				case "request":
					s.cpuRequest = bad
				case "usage":
					s.cpuUsage = bad
				case "cost":
					s.cpuCost = bad
				case "adjustment":
					s.cpuAdjustment = bad
				}
				data, err := json.Marshal(group)
				if err != nil {
					t.Fatal(err)
				}
				var response struct {
					CPU      *float64                   `json:"cpuEfficiency"`
					RAM      *float64                   `json:"ramEfficiency"`
					Total    *float64                   `json:"totalEfficiency"`
					Metadata *requestEfficiencyMetadata `json:"requestEfficiencyMetadata"`
				}
				if err := json.Unmarshal(data, &response); err != nil {
					t.Fatal(err)
				}
				checkEfficiency(t, "invalid CPU", response.CPU, nil)
				checkEfficiency(t, "valid RAM", response.RAM, floatPtr(0.5))
				checkEfficiency(t, "eligible total", response.Total, floatPtr(0.5))
				if response.Metadata == nil || response.Metadata.CPURequestMinutes != 0 || response.Metadata.CPUUsageMinutes != 0 || response.Metadata.CPUCost != 0 || response.Metadata.CPUCostAdjustment != 0 {
					t.Fatalf("invalid CPU metadata: %+v", response.Metadata)
				}
				summary := NewSummaryAllocation(group, false, false)
				data, err = json.Marshal(summary)
				if err != nil {
					t.Fatal(err)
				}
				var decoded SummaryAllocation
				if err := json.Unmarshal(data, &decoded); err != nil {
					t.Fatal(err)
				}
				checkEfficiency(t, "summary", decoded.RequestEfficiency(), floatPtr(0.5))
				if decoded.requestEfficiencyStats == nil || decoded.requestEfficiencyStats.cpuRequest != 0 {
					t.Fatal("invalid CPU state was not excluded")
				}
				if field == "request" && !math.IsNaN(bad) && !math.IsInf(s.cpuRequest, 0) {
					t.Fatal("serialization mutated source")
				}
			})
		}
	}
}

func TestRequestEfficiencyNonFiniteAggregation(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, resource := range []string{"cpu", "ram"} {
			for _, reverse := range []bool{false, true} {
				t.Run(fmt.Sprintf("%v/%s/reverse=%v", bad, resource, reverse), func(t *testing.T) {
					valid := &Allocation{Name: "valid", Properties: &AllocationProperties{Cluster: "cluster", Namespace: "workload"}, Window: NewWindow(&start, &end), Start: start, End: end, CPUCoreRequestAverage: 2, CPUCoreUsageAverage: 1, CPUCost: 1, RAMBytesRequestAverage: 4, RAMBytesUsageAverage: 1, RAMCost: 1}
					invalid := &Allocation{Name: "invalid", Properties: &AllocationProperties{Cluster: "cluster", Namespace: "workload"}, Window: NewWindow(&start, &end), Start: start, End: end, CPUCoreRequestAverage: 2, CPUCoreUsageAverage: 2, CPUCost: 1, RAMBytesRequestAverage: 4, RAMBytesUsageAverage: 4, RAMCost: 1}
					invalid.requestEfficiencyStats = invalid.requestStats()
					wantCPU, wantRAM := 0.5, 0.625
					if resource == "cpu" {
						invalid.requestEfficiencyStats.cpuUsage = bad
					} else {
						invalid.requestEfficiencyStats.ramCost = bad
						wantCPU, wantRAM = 0.75, 0.25
					}
					left, right := valid, invalid
					if reverse {
						left, right = invalid, valid
					}
					group, err := left.Add(right)
					if err != nil {
						t.Fatal(err)
					}
					check := func(name string, stats *requestEfficiencyStats) {
						t.Helper()
						checkEfficiency(t, name+" CPU", requestEfficiency(stats.cpuRequest, stats.cpuUsage), floatPtr(wantCPU))
						checkEfficiency(t, name+" RAM", requestEfficiency(stats.ramRequest, stats.ramUsage), floatPtr(wantRAM))
					}
					check("allocation", group.requestStats())
					set := NewAllocationSet(start, end)
					if err := set.Insert(left.Clone()); err != nil {
						t.Fatal(err)
					}
					if err := set.Insert(right.Clone()); err != nil {
						t.Fatal(err)
					}
					summarySet := NewSummaryAllocationSet(set.Clone(), nil, nil, false, false)
					if err := set.AggregateBy([]string{"namespace"}, nil); err != nil {
						t.Fatal(err)
					}
					if row := set.Get("workload"); row == nil {
						t.Fatal("missing allocation group")
					} else {
						check("allocation group", row.requestStats())
					}
					if err := summarySet.AggregateBy([]string{"namespace"}, nil); err != nil {
						t.Fatal(err)
					}
					if row := summarySet.SummaryAllocations["workload"]; row == nil {
						t.Fatal("missing summary group")
					} else {
						check("summary group", row.requestStats())
					}
					binary, err := group.MarshalBinary()
					if err != nil {
						t.Fatal(err)
					}
					var fromBinary Allocation
					if err := fromBinary.UnmarshalBinary(binary); err != nil {
						t.Fatal(err)
					}
					check("binary", fromBinary.requestStats())
					data, err := json.Marshal(group)
					if err != nil {
						t.Fatal(err)
					}
					var fromJSON Allocation
					if err := json.Unmarshal(data, &fromJSON); err != nil {
						t.Fatal(err)
					}
					check("allocation JSON", fromJSON.requestStats())
					sa := NewSummaryAllocation(left, false, false)
					if err := sa.Add(NewSummaryAllocation(right, false, false)); err != nil {
						t.Fatal(err)
					}
					check("summary", sa.requestStats())
					data, err = json.Marshal(sa)
					if err != nil {
						t.Fatal(err)
					}
					var summaryJSON SummaryAllocation
					if err := json.Unmarshal(data, &summaryJSON); err != nil {
						t.Fatal(err)
					}
					check("summary JSON", summaryJSON.requestStats())
				})
			}
		}
	}
}

func TestRequestEfficiencyBinaryRoundTrip(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	valid := &Allocation{Name: "valid", Window: NewWindow(&start, &end), Start: start, End: end, CPUCoreRequestAverage: 2, CPUCoreUsageAverage: 1, CPUCost: 2}
	invalid := &Allocation{Name: "invalid", Window: NewWindow(&start, &end), Start: start, End: end, CPUCoreUsageAverage: 2, CPUCost: 2}
	combined, err := valid.Add(invalid)
	if err != nil {
		t.Fatal(err)
	}
	data, err := combined.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var decoded Allocation
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	checkEfficiency(t, "binary round trip", decoded.requestStats().total(), floatPtr(0.5))

	// Version 25 has no appended request-efficiency pointer.
	legacy, err := valid.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if legacy[len(legacy)-1] != 0 {
		t.Fatal("expected nil version 26 state")
	}
	legacy[0] = 25
	if err := decoded.UnmarshalBinary(legacy[:len(legacy)-1]); err != nil {
		t.Fatal(err)
	}
	if decoded.requestEfficiencyStats != nil {
		t.Fatal("legacy decoding retained stale aggregation state")
	}
	checkEfficiency(t, "legacy binary", decoded.requestStats().total(), floatPtr(0.5))
}

func floatPtr(v float64) *float64 { return &v }
