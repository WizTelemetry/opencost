package costmodel

import (
	"testing"
	"time"

	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/opencost/opencost/core/pkg/storage"
	"github.com/opencost/opencost/pkg/cloud/models"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestETLStoreAllocationRoundTripReturnsClone(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	original := testETLAllocationSet(start, end, 1)
	store := newETLStore(storage.NewMemoryStorage(), t.TempDir(), "cluster-a", time.Hour, 0.5)

	require.NoError(t, store.WriteAllocation(original))
	require.True(t, store.has(etlAllocationKind, start, end, time.Hour))

	first, found, err := store.ReadAllocation(start, end, time.Hour)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 1.0, first.TotalCost())

	first.Get("allocation-a").CPUCost = 99
	second, found, err := store.ReadAllocation(start, end, time.Hour)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 1.0, second.TotalCost())
}

func TestETLStoreRequiresReadyMarker(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	store := newETLStore(storage.NewMemoryStorage(), t.TempDir(), "cluster-a", time.Hour, 0.5)
	revision := mustETLRevision(t, store)
	name := store.recordPath(etlAllocationKind, time.Hour, start, end, revision)
	data, err := testETLAllocationSet(start, end, 1).MarshalBinary()
	require.NoError(t, err)
	require.NoError(t, store.storage.Write(name, data))

	assert.False(t, store.has(etlAllocationKind, start, end, time.Hour))
	_, found, err := store.ReadAllocation(start, end, time.Hour)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestETLStorePricingRevisionIsolatesBuckets(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	fingerprint := "pricing-v1-first"
	store := newETLStore(storage.NewMemoryStorage(), t.TempDir(), "cluster-a", time.Hour, 0.5)
	store.pricing = func() (string, error) { return fingerprint, nil }

	require.NoError(t, store.WriteAllocation(testETLAllocationSet(start, end, 1)))
	assert.True(t, store.has(etlAllocationKind, start, end, time.Hour))

	fingerprint = "pricing-v1-second"
	assert.False(t, store.has(etlAllocationKind, start, end, time.Hour))
	_, found, err := store.ReadAllocation(start, end, time.Hour)
	require.NoError(t, err)
	assert.False(t, found)
}

func TestETLPricingFingerprintTracksCostInputsNotCredentials(t *testing.T) {
	first := &models.CustomPricing{
		Provider:              "custom",
		CPU:                   "0.10",
		RAM:                   "0.02",
		GPU:                   "1.00",
		Discount:              "10%",
		NegotiatedDiscount:    "5%",
		AwsServiceKeyName:     "first-access-key",
		AwsServiceKeySecret:   "first-secret",
		AzureClientSecret:     "first-azure-secret",
		CustomPricesEnabled:   "true",
		ZoneNetworkEgress:     "0.01",
		RegionNetworkEgress:   "0.02",
		InternetNetworkEgress: "0.03",
	}
	firstFingerprint := etlPricingFingerprint(first)

	credentialsRotated := *first
	credentialsRotated.AwsServiceKeyName = "second-access-key"
	credentialsRotated.AwsServiceKeySecret = "second-secret"
	credentialsRotated.AzureClientSecret = "second-azure-secret"
	assert.Equal(t, firstFingerprint, etlPricingFingerprint(&credentialsRotated))

	priceChanged := *first
	priceChanged.CPU = "0.20"
	assert.NotEqual(t, firstFingerprint, etlPricingFingerprint(&priceChanged))
}

func TestETLStoreCleanupRemovesExpiredBucketsAcrossPricingRevisions(t *testing.T) {
	now := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	store := newETLStore(storage.NewMemoryStorage(), t.TempDir(), "cluster-a", time.Hour, 0.5)

	oldStart := now.Add(-48 * time.Hour)
	oldEnd := oldStart.Add(time.Hour)
	recentStart := now.Add(-2 * time.Hour)
	recentEnd := recentStart.Add(time.Hour)

	fingerprint := "pricing-v1-old"
	store.pricing = func() (string, error) { return fingerprint, nil }
	oldRevision := mustETLRevision(t, store)
	require.NoError(t, store.writeAllocation(testETLAllocationSet(oldStart, oldEnd, 1), oldRevision, now.Add(-47*time.Hour), etlSourceCoverage{}))
	require.NoError(t, store.writeAllocation(testETLAllocationSet(recentStart, recentEnd, 1), oldRevision, now.Add(-time.Hour), etlSourceCoverage{}))

	fingerprint = "pricing-v1-new"
	newRevision := mustETLRevision(t, store)
	require.NoError(t, store.writeAllocation(testETLAllocationSet(recentStart, recentEnd, 2), newRevision, now.Add(-time.Hour), etlSourceCoverage{}))

	removed, err := store.cleanup(now, 24*time.Hour, 7*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.False(t, store.hasForRevision(etlAllocationKind, oldStart, oldEnd, time.Hour, oldRevision))
	assert.True(t, store.hasForRevision(etlAllocationKind, recentStart, recentEnd, time.Hour, oldRevision))
	assert.True(t, store.hasForRevision(etlAllocationKind, recentStart, recentEnd, time.Hour, newRevision))
}

func TestETLStorePartitionUsesDailyThenHourlyBuckets(t *testing.T) {
	dayTwo := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	dayThree := dayTwo.Add(24 * time.Hour)
	start := dayTwo.Add(-12*time.Hour + 30*time.Minute)
	end := dayThree.Add(6 * time.Hour)
	store := newETLStore(storage.NewMemoryStorage(), t.TempDir(), "cluster-a", time.Hour, 0.5)

	require.NoError(t, store.WriteAllocation(testETLAllocationSet(dayTwo, dayThree, 1)))

	parts, coverage := store.partition(etlAllocationKind, start, end, mustETLRevision(t, store))
	require.Len(t, parts, 3)
	assert.False(t, parts[0].FromETL)
	assert.True(t, parts[1].FromETL)
	assert.Equal(t, 24*time.Hour, parts[1].Resolution)
	assert.True(t, parts[1].Start.Equal(dayTwo))
	assert.True(t, parts[1].End.Equal(dayThree))
	assert.False(t, parts[2].FromETL)
	assert.Greater(t, coverage, store.minCoverage)
}

func TestETLStorePartitionFallsBelowMinimumCoverage(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(4 * time.Hour)
	store := newETLStore(storage.NewMemoryStorage(), t.TempDir(), "cluster-a", time.Hour, 0.5)
	require.NoError(t, store.WriteAllocation(testETLAllocationSet(start, start.Add(time.Hour), 1)))

	_, coverage := store.partition(etlAllocationKind, start, end, mustETLRevision(t, store))
	assert.Equal(t, 0.25, coverage)
	assert.Less(t, coverage, store.minCoverage)
}

func TestETLStorePartitionClampsFutureTailToNow(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := newETLStore(storage.NewMemoryStorage(), t.TempDir(), "cluster-a", time.Hour, 0.4)
	for hour := 0; hour < 10; hour++ {
		bucketStart := start.Add(time.Duration(hour) * time.Hour)
		require.NoError(t, store.WriteAllocation(testETLAllocationSet(bucketStart, bucketStart.Add(time.Hour), 1)))
	}
	now := start.Add(10*time.Hour + 20*time.Minute)
	store.now = func() time.Time { return now }

	parts, coverage := store.partition(etlAllocationKind, start, start.Add(24*time.Hour), mustETLRevision(t, store))
	require.NotEmpty(t, parts)
	assert.True(t, parts[len(parts)-1].End.Equal(now), "last part must stop at now, got %s", parts[len(parts)-1].End)
	assert.InDelta(t, float64(10)/float64(10+20.0/60.0), coverage, 0.0001)
}

func TestAllocationSetWithETLComposesBuckets(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	middle := start.Add(time.Hour)
	end := start.Add(2 * time.Hour)
	store := newETLStore(storage.NewMemoryStorage(), t.TempDir(), "cluster-a", time.Hour, 0.5)
	require.NoError(t, store.WriteAllocation(testETLAllocationSet(start, middle, 1)))
	require.NoError(t, store.WriteAllocation(testETLAllocationSet(middle, end, 2)))
	cm := &CostModel{ETL: store}

	set, used, err := cm.allocationSetWithETL(start, end, 0, nil)
	require.NoError(t, err)
	require.True(t, used)
	assert.Equal(t, 3.0, set.TotalCost())
	assert.Equal(t, 3.0, set.Get("allocation-a").CPUCoreHours)
	assert.True(t, set.Start().Equal(start))
	assert.True(t, set.End().Equal(end))
}

func TestMergeAllocationSetsPreservesAttributionMetadata(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	middle := start.Add(time.Hour)
	end := start.Add(2 * time.Hour)

	first := testETLAllocationSetWithMetadata(start, middle, 1)
	firstAllocation := first.Get("allocation-a")
	firstAllocation.Properties.Labels = opencost.AllocationLabels{"kubesphere_io_workspace": "workspace-a"}
	firstAllocation.Properties.Annotations = opencost.AllocationAnnotations{"owner": "team-a"}
	firstAllocation.Properties.Services = []string{"service-a"}
	firstAllocation.RawAllocationOnly.CPUCoreUsageMax = 2
	firstAllocation.RawAllocationOnly.RAMBytesUsageMax = 4
	gpuMax := 3.0
	firstAllocation.RawAllocationOnly.GPUUsageMax = &gpuMax

	second := testETLAllocationSetWithMetadata(middle, end, 2)
	secondAllocation := second.Get("allocation-a")
	secondAllocation.Properties.Labels = opencost.AllocationLabels{"kubesphere_io_workspace": "workspace-a"}
	secondAllocation.Properties.Annotations = opencost.AllocationAnnotations{"owner": "team-a"}
	secondAllocation.Properties.Services = []string{"service-b"}
	secondAllocation.RawAllocationOnly.CPUCoreUsageMax = 1
	secondAllocation.RawAllocationOnly.RAMBytesUsageMax = 8
	gpuMax = 5
	secondAllocation.RawAllocationOnly.GPUUsageMax = &gpuMax

	merged, err := mergeAllocationSets(first, second)
	require.NoError(t, err)
	require.Equal(t, 3.0, merged.TotalCost())

	allocation := merged.Get("allocation-a")
	require.NotNil(t, allocation)
	assert.Equal(t, "workspace-a", allocation.Properties.Labels["kubesphere_io_workspace"])
	assert.Equal(t, "team-a", allocation.Properties.Annotations["owner"])
	assert.Equal(t, []string{"service-a", "service-b"}, allocation.Properties.Services)

	require.NotNil(t, allocation.RawAllocationOnly)
	assert.Equal(t, 2.0, allocation.RawAllocationOnly.CPUCoreUsageMax)
	assert.Equal(t, 8.0, allocation.RawAllocationOnly.RAMBytesUsageMax)
	require.NotNil(t, allocation.RawAllocationOnly.GPUUsageMax)
	assert.Equal(t, 5.0, *allocation.RawAllocationOnly.GPUUsageMax)

	assert.Equal(t, 1.0, first.Get("allocation-a").CPUCoreHours, "merge must not mutate the first bucket")
	assert.Equal(t, 2.0, second.Get("allocation-a").CPUCoreHours, "merge must not mutate the second bucket")

	aggregated := merged.Clone()
	require.NoError(t, aggregated.AggregateBy(
		[]string{"label:kubesphere_io_workspace"},
		&opencost.AllocationAggregationOptions{},
	))
	assert.Equal(t, 3.0, aggregated.Get("workspace-a").TotalCost())
	assert.Nil(t, aggregated.Get(opencost.UnallocatedSuffix))

	aggregated = merged.Clone()
	require.NoError(t, aggregated.AggregateBy(
		[]string{"service"},
		&opencost.AllocationAggregationOptions{},
	))
	assert.Nil(t, aggregated.Get(opencost.UnallocatedSuffix))
}

func TestComputeAllocationStepUsesFullETLCoverage(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	store := newETLStore(storage.NewMemoryStorage(), t.TempDir(), "cluster-a", time.Hour, 0.5)
	require.NoError(t, store.WriteAllocation(testETLAllocationSet(start, start.Add(time.Hour), 1)))
	require.NoError(t, store.WriteAllocation(testETLAllocationSet(start.Add(time.Hour), end, 2)))
	cm := &CostModel{ETL: store}

	set, nodeMap, err := cm.computeAllocationStep(start, end, nil)
	require.NoError(t, err)
	assert.Nil(t, nodeMap)
	assert.Equal(t, 3.0, set.TotalCost())
}

func TestETLWorkerOnlySelectsMissingAllocationBuckets(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	middle := start.Add(time.Hour)
	end := start.Add(2 * time.Hour)
	store := newETLStore(storage.NewMemoryStorage(), t.TempDir(), "cluster-a", time.Hour, 0.5)
	require.NoError(t, store.WriteAllocation(testETLAllocationSet(start, middle, 1)))
	worker := &ETLWorker{store: store}

	tasks := worker.selectTasks(start, end, time.Hour, 0, 10)
	require.Len(t, tasks, 1)
	assert.True(t, tasks[0].start.Equal(middle))
	assert.True(t, tasks[0].end.Equal(end))
}

func TestETLWorkerPrioritizesMissingBucketsBeforeLateDataRefresh(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	end := floorToETLBoundary(now, time.Hour)
	start := end.Add(-2 * time.Hour)
	store := newETLStore(storage.NewMemoryStorage(), t.TempDir(), "cluster-a", time.Hour, 0.5)
	revision := mustETLRevision(t, store)
	require.NoError(t, store.writeAllocation(
		testETLAllocationSet(end.Add(-time.Hour), end, 1),
		revision,
		now.Add(-2*time.Hour),
		etlSourceCoverage{},
	))

	worker := &ETLWorker{store: store}
	tasks := worker.selectTasksWithPolicy(start, end, time.Hour, etlTaskSelectionPolicy{
		now:             now,
		revision:        revision,
		refreshLookback: 2 * time.Hour,
		refreshInterval: 30 * time.Minute,
		refreshLimit:    1,
		maxTasksPerRun:  1,
	})
	require.Len(t, tasks, 1)
	assert.True(t, tasks[0].start.Equal(start), "the missing older bucket must not be starved by a late-data refresh")
}

func TestETLWorkerDefersBucketUntilSourceCoverageIsComplete(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	store := newETLStore(storage.NewMemoryStorage(), t.TempDir(), "cluster-a", time.Hour, 0.5)
	worker := &ETLWorker{store: store}

	result := worker.buildTask(
		etlBuildTask{start: start, end: end, resolution: time.Hour},
		etlSourceCoverage{Start: start, End: end.Add(-etlCoverageTolerance - time.Second)},
	)
	require.NoError(t, result.err)
	assert.False(t, result.written)
	assert.False(t, store.has(etlAllocationKind, start, end, time.Hour))
}

func TestETLSourceCoverageRequiresBothEdges(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	coverage := etlSourceCoverage{Start: start, End: end}
	assert.True(t, coverage.covers(start, end))
	toleranceCoverage := etlSourceCoverage{Start: start.Add(etlCoverageTolerance), End: end.Add(-etlCoverageTolerance)}
	assert.True(t, toleranceCoverage.covers(start, end))
	assert.False(t, coverage.covers(start.Add(-etlCoverageTolerance-time.Second), end))
	assert.False(t, coverage.covers(start, end.Add(etlCoverageTolerance+time.Second)))
}

type permissiveCoverageQuerier struct{}

func (permissiveCoverageQuerier) QueryDataCoverage(int) (time.Time, time.Time, error) {
	now := time.Now().UTC()
	return now.Add(-24 * time.Hour), now, nil
}

type strictCoverageQuerier struct {
	permissiveCoverageQuerier
	called bool
}

func (q *strictCoverageQuerier) QueryDataCoverageStrict(int) (time.Time, time.Time, error) {
	q.called = true
	return time.Time{}, time.Time{}, assert.AnError
}

func TestQueryETLDataCoveragePrefersStrictQuerier(t *testing.T) {
	strict := &strictCoverageQuerier{}
	_, _, err := queryETLDataCoverage(strict, 7)

	require.ErrorIs(t, err, assert.AnError)
	assert.True(t, strict.called, "ETL must not fall back to permissive coverage semantics")
}

func TestQueryAllocationIncludeIdleUsesETLAllocationAndCachedLiveAssets(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	store := newETLStore(storage.NewMemoryStorage(), t.TempDir(), "cluster-a", time.Hour, 0.5)
	require.NoError(t, store.WriteAllocation(testETLAllocationSet(start, end, 2)))

	assetSet := testETLAssetSet(start, end, 5)
	assetCache := cache.New(time.Minute, time.Minute)
	assetCache.Set(assetStepCacheKey(start, end, 0), &assetStepCacheValue{assetSet: assetSet}, cache.DefaultExpiration)

	cm := &CostModel{
		BatchDuration:  time.Hour,
		ETL:            store,
		allocStepCache: newAllocStepCache(),
		assetStepCache: assetCache,
	}
	assets, err := cm.ComputeAssets(start, end)
	require.NoError(t, err)
	require.Equal(t, 5.0, assets.TotalCost())

	asr, err := cm.QueryAllocation(
		opencost.NewClosedWindow(start, end),
		time.Hour,
		[]string{opencost.AllocationClusterProp},
		true,
		false,
		false,
		false,
		false,
		opencost.AccumulateOptionNone,
		false,
		"",
	)
	require.NoError(t, err)
	require.Len(t, asr.Allocations, 1)
	require.Contains(t, asr.Allocations[0].Allocations, "cluster-a")
	require.Contains(t, asr.Allocations[0].Allocations, "__idle__")
	assert.Equal(t, 2.0, asr.Allocations[0].Get("cluster-a").TotalCost())
	assert.Equal(t, 3.0, asr.Allocations[0].Get("__idle__").TotalCost())
}

func testETLAllocationSet(start, end time.Time, cost float64) *opencost.AllocationSet {
	set := opencost.NewAllocationSet(start, end)
	window := opencost.NewClosedWindow(start, end)
	_ = set.Insert(&opencost.Allocation{
		Name: "allocation-a",
		Properties: &opencost.AllocationProperties{
			Cluster:   "cluster-a",
			Namespace: "namespace-a",
			Pod:       "pod-a",
			Container: "container-a",
		},
		Window:       window,
		Start:        start,
		End:          end,
		CPUCoreHours: cost,
		CPUCost:      cost,
	})
	set.Window = window
	return set
}

func testETLAllocationSetWithMetadata(start, end time.Time, cost float64) *opencost.AllocationSet {
	set := testETLAllocationSet(start, end, cost)
	set.Get("allocation-a").RawAllocationOnly = &opencost.RawAllocationOnlyData{}
	return set
}

func testETLAssetSet(start, end time.Time, cost float64) *opencost.AssetSet {
	window := opencost.NewClosedWindow(start, end)
	node := opencost.NewNode("node-a", "cluster-a", "provider-a", start, end, window)
	node.CPUCoreHours = cost
	node.CPUCost = cost
	return opencost.NewAssetSet(start, end, node)
}

func mustETLRevision(t *testing.T, store *ETLStore) etlRevision {
	t.Helper()
	revision, err := store.revision()
	require.NoError(t, err)
	return revision
}
