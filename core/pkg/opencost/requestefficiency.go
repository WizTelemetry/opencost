package opencost

import "math"

// requestEfficiencyStats keeps only resource use from allocations that declare a
// request. Its fields are cumulative so grouping and time accumulation commute.
// The versioned binary codec preserves this state across ETL cache reloads.
type requestEfficiencyStats struct {
	cpuRequest    float64
	cpuUsage      float64
	cpuCost       float64
	cpuAdjustment float64
	ramRequest    float64
	ramUsage      float64
	ramCost       float64
	ramAdjustment float64
}

func newRequestEfficiencyStats(minutes, cpuRequest, cpuUsage, cpuCost, ramRequest, ramUsage, ramCost float64) *requestEfficiencyStats {
	if minutes <= 0 {
		minutes = 1
	}
	s := &requestEfficiencyStats{}
	if cpuRequest > 0 {
		s.cpuRequest, s.cpuUsage, s.cpuCost = cpuRequest*minutes, cpuUsage*minutes, cpuCost
	}
	if ramRequest > 0 {
		s.ramRequest, s.ramUsage, s.ramCost = ramRequest*minutes, ramUsage*minutes, ramCost
	}
	return s
}

func (s *requestEfficiencyStats) add(other *requestEfficiencyStats) {
	*s = *s.finiteCopy()
	other = other.finiteCopy()
	s.cpuRequest += other.cpuRequest
	s.cpuUsage += other.cpuUsage
	s.cpuCost += other.cpuCost
	s.cpuAdjustment += other.cpuAdjustment
	s.ramRequest += other.ramRequest
	s.ramUsage += other.ramUsage
	s.ramCost += other.ramCost
	s.ramAdjustment += other.ramAdjustment
}

// Share idle cost in the same proportion as the eligible pre-share cost.
func (sa *SummaryAllocation) addSharedIdleToRequestStats(cpu, ram float64) {
	if sa.requestEfficiencyStats == nil {
		return
	}
	if sa.CPUCost > 0 {
		sa.requestEfficiencyStats.cpuCost += cpu * sa.requestEfficiencyStats.cpuCost / sa.CPUCost
	} else if sa.CPUCost == 0 && sa.requestEfficiencyStats.cpuRequest > 0 {
		sa.requestEfficiencyStats.cpuCost += cpu
	}
	if sa.RAMCost > 0 {
		sa.requestEfficiencyStats.ramCost += ram * sa.requestEfficiencyStats.ramCost / sa.RAMCost
	} else if sa.RAMCost == 0 && sa.requestEfficiencyStats.ramRequest > 0 {
		sa.requestEfficiencyStats.ramCost += ram
	}
}

func (a *Allocation) addSharedIdleToRequestStats(cpu, ram float64) {
	if a.requestEfficiencyStats == nil {
		return
	}
	if cost := a.CPUTotalCost(); cost > 0 {
		a.requestEfficiencyStats.cpuCost += cpu * a.requestEfficiencyStats.cpuCost / cost
	} else if cost == 0 && a.requestEfficiencyStats.cpuRequest > 0 {
		a.requestEfficiencyStats.cpuCost += cpu
	}
	if cost := a.RAMTotalCost(); cost > 0 {
		a.requestEfficiencyStats.ramCost += ram * a.requestEfficiencyStats.ramCost / cost
	} else if cost == 0 && a.requestEfficiencyStats.ramRequest > 0 {
		a.requestEfficiencyStats.ramCost += ram
	}
}

// A resource with incomplete numeric inputs cannot contribute to a response.
func (s *requestEfficiencyStats) finiteCopy() *requestEfficiencyStats {
	if s == nil {
		return nil
	}
	copy := *s
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	if !finite(copy.cpuRequest) || !finite(copy.cpuUsage) || !finite(copy.cpuCost) || !finite(copy.cpuAdjustment) {
		copy.cpuRequest, copy.cpuUsage, copy.cpuCost, copy.cpuAdjustment = 0, 0, 0, 0
	}
	if !finite(copy.ramRequest) || !finite(copy.ramUsage) || !finite(copy.ramCost) || !finite(copy.ramAdjustment) {
		copy.ramRequest, copy.ramUsage, copy.ramCost, copy.ramAdjustment = 0, 0, 0, 0
	}
	return &copy
}

func (s *requestEfficiencyStats) total() *float64 {
	return weightedRequestEfficiency(s.cpuRequest, s.cpuUsage, s.cpuCost, s.ramRequest, s.ramUsage, s.ramCost)
}

func (a *Allocation) requestStats() *requestEfficiencyStats {
	if a.requestEfficiencyStats != nil {
		return a.requestEfficiencyStats
	}
	s := newRequestEfficiencyStats(a.Minutes(), a.CPUCoreRequestAverage, a.CPUCoreUsageAverage, a.CPUTotalCost(), a.RAMBytesRequestAverage, a.RAMBytesUsageAverage, a.RAMTotalCost())
	if a.CPUCoreRequestAverage > 0 {
		s.cpuAdjustment = a.CPUCostAdjustment
	}
	if a.RAMBytesRequestAverage > 0 {
		s.ramAdjustment = a.RAMCostAdjustment
	}
	return s
}

// RequestEfficiency returns the request-valid cost-weighted efficiency.
func (sa *SummaryAllocation) RequestEfficiency() *float64 {
	return sa.requestStats().finiteCopy().total()
}

func (sa *SummaryAllocation) requestStats() *requestEfficiencyStats {
	if sa.requestEfficiencyStats != nil {
		return sa.requestEfficiencyStats
	}
	return newRequestEfficiencyStats(sa.Minutes(), sa.CPUCoreRequestAverage, sa.CPUCoreUsageAverage, sa.CPUCost, sa.RAMBytesRequestAverage, sa.RAMBytesUsageAverage, sa.RAMCost)
}
