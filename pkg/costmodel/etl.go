package costmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	coreenv "github.com/opencost/opencost/core/pkg/env"
	"github.com/opencost/opencost/core/pkg/log"
	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/opencost/opencost/core/pkg/storage"
	"github.com/opencost/opencost/core/pkg/util/interval"
	"github.com/opencost/opencost/pkg/cloud/models"
	"github.com/patrickmn/go-cache"
)

const (
	etlEnabledEnvVar                 = "OPENCOST_ETL_ENABLED"
	etlStorageDirEnvVar              = "OPENCOST_ETL_STORAGE_DIR"
	etlResolutionSecondsEnvVar       = "OPENCOST_ETL_RESOLUTION_SECONDS"
	etlHourlyRetentionHoursEnvVar    = "OPENCOST_ETL_HOURLY_STORE_DURATION_HOURS"
	etlDailyRetentionDaysEnvVar      = "OPENCOST_ETL_DAILY_STORE_DURATION_DAYS"
	etlWorkerIntervalSecondsEnvVar   = "OPENCOST_ETL_INTERVAL_SECONDS"
	etlWorkerStartupDelaySecondsVar  = "OPENCOST_ETL_STARTUP_DELAY_SECONDS"
	etlWorkerConcurrencyEnvVar       = "OPENCOST_ETL_QUERY_CONCURRENCY"
	etlMaxHourlyBucketsPerRunEnvVar  = "OPENCOST_ETL_MAX_HOURLY_BUCKETS_PER_RUN"
	etlMaxDailyBucketsPerRunEnvVar   = "OPENCOST_ETL_MAX_DAILY_BUCKETS_PER_RUN"
	etlRefreshHourlyBucketsEnvVar    = "OPENCOST_ETL_REFRESH_HOURLY_BUCKETS"
	etlRefreshDailyBucketsEnvVar     = "OPENCOST_ETL_REFRESH_DAILY_BUCKETS"
	etlLateDataHourlyLookbackEnvVar  = "OPENCOST_ETL_LATE_DATA_HOURLY_LOOKBACK_HOURS"
	etlLateDataDailyLookbackEnvVar   = "OPENCOST_ETL_LATE_DATA_DAILY_LOOKBACK_DAYS"
	etlLateDataRefreshIntervalEnvVar = "OPENCOST_ETL_LATE_DATA_REFRESH_INTERVAL_SECONDS"
	etlMinimumCoveragePercentEnvVar  = "OPENCOST_ETL_MINIMUM_COVERAGE_PERCENT"
	etlSchemaVersion                 = "v3"
	defaultETLPricingFingerprint     = "pricing-v1-default"
	defaultETLStorageDir             = "/var/configs/costwise-etl"
	defaultETLResolution             = time.Hour
	defaultETLHourlyRetention        = 7 * 24 * time.Hour
	defaultETLDailyRetention         = 30 * 24 * time.Hour
	defaultETLWorkerInterval         = 5 * time.Minute
	defaultETLWorkerStartupDelay     = 30 * time.Second
	defaultETLWorkerConcurrency      = 2
	defaultETLMaxHourlyBucketsPerRun = 48
	defaultETLMaxDailyBucketsPerRun  = 7
	defaultETLRefreshHourlyBuckets   = 2
	defaultETLRefreshDailyBuckets    = 1
	defaultETLLateDataHourlyLookback = 48 * time.Hour
	defaultETLLateDataDailyLookback  = 3 * 24 * time.Hour
	defaultETLLateDataRefresh        = 30 * time.Minute
	defaultETLMinimumCoveragePercent = 50
	etlCoverageTolerance             = 5 * time.Minute
	etlMemoryCacheTTL                = 10 * time.Minute
	etlMemoryCacheCleanupInterval    = 5 * time.Minute
)

type etlDataKind string

const etlAllocationKind etlDataKind = "allocation"

type etlRangePart struct {
	Start      time.Time
	End        time.Time
	Resolution time.Duration
	FromETL    bool
}

type etlRevision struct {
	pricingFingerprint string
}

type etlBucketMarker struct {
	Complete                bool      `json:"complete"`
	WrittenAt               time.Time `json:"writtenAt"`
	Start                   time.Time `json:"start"`
	End                     time.Time `json:"end"`
	ResolutionSeconds       int64     `json:"resolutionSeconds"`
	PricingFingerprint      string    `json:"pricingFingerprint"`
	AllocationCount         int       `json:"allocationCount"`
	PrometheusCoverageStart time.Time `json:"prometheusCoverageStart,omitempty"`
	PrometheusCoverageEnd   time.Time `json:"prometheusCoverageEnd,omitempty"`
}

type etlSourceCoverage struct {
	Start     time.Time
	End       time.Time
	Tolerance time.Duration
}

type etlDataCoverageQuerier interface {
	QueryDataCoverage(limitDays int) (time.Time, time.Time, error)
}

type etlStrictDataCoverageQuerier interface {
	etlDataCoverageQuerier
	QueryDataCoverageStrict(limitDays int) (time.Time, time.Time, error)
}

func queryETLDataCoverage(querier etlDataCoverageQuerier, limitDays int) (time.Time, time.Time, error) {
	if strict, ok := querier.(etlStrictDataCoverageQuerier); ok {
		return strict.QueryDataCoverageStrict(limitDays)
	}
	return querier.QueryDataCoverage(limitDays)
}

// ETLStore persists revisioned allocation buckets. It is deliberately separate
// from the response cache so data remains useful across restarts and queries
// with different aggregation/filter parameters.
type ETLStore struct {
	storage       storage.Storage
	root          string
	clusterID     string
	resolution    time.Duration
	minCoverage   float64
	dailyDuration time.Duration
	memory        *cache.Cache
	now           func() time.Time
	pricing       func() (string, error)
}

func newETLStore(store storage.Storage, root, clusterID string, resolution time.Duration, minCoverage float64) *ETLStore {
	if resolution < time.Second {
		resolution = defaultETLResolution
	}
	if minCoverage < 0 {
		minCoverage = 0
	}
	if minCoverage > 1 {
		minCoverage = 1
	}

	return &ETLStore{
		storage:       store,
		root:          root,
		clusterID:     sanitizeETLPathComponent(clusterID),
		resolution:    resolution,
		minCoverage:   minCoverage,
		dailyDuration: 24 * time.Hour,
		memory:        cache.New(etlMemoryCacheTTL, etlMemoryCacheCleanupInterval),
		now:           time.Now,
		pricing: func() (string, error) {
			return defaultETLPricingFingerprint, nil
		},
	}
}

func newETLStoreFromEnv(clusterID string) (*ETLStore, bool, error) {
	if !strings.EqualFold(coreenv.Get(etlEnabledEnvVar, "false"), "true") {
		return nil, false, nil
	}

	resolutionSeconds := coreenv.GetInt(etlResolutionSecondsEnvVar, int(defaultETLResolution/time.Second))
	if resolutionSeconds < 1 {
		return nil, false, fmt.Errorf("%s must be at least 1 second", etlResolutionSecondsEnvVar)
	}

	minCoveragePercent := coreenv.GetInt(etlMinimumCoveragePercentEnvVar, defaultETLMinimumCoveragePercent)
	root := coreenv.Get(etlStorageDirEnvVar, defaultETLStorageDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, false, fmt.Errorf("creating ETL storage directory %q: %w", root, err)
	}

	store := storage.NewFileStorage(root)
	return newETLStore(
		store,
		root,
		clusterID,
		time.Duration(resolutionSeconds)*time.Second,
		float64(minCoveragePercent)/100,
	), true, nil
}

func (s *ETLStore) Resolution() time.Duration {
	if s == nil {
		return 0
	}
	return s.resolution
}

func (s *ETLStore) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *ETLStore) WriteAllocation(set *opencost.AllocationSet) error {
	revision, err := s.revision()
	if err != nil {
		return err
	}
	return s.writeAllocation(set, revision, time.Now().UTC(), etlSourceCoverage{})
}

func (s *ETLStore) writeAllocation(set *opencost.AllocationSet, revision etlRevision, writtenAt time.Time, coverage etlSourceCoverage) error {
	if s == nil {
		return errors.New("cannot write allocation to nil ETL store")
	}
	if set == nil || set.Window.Start() == nil || set.Window.End() == nil {
		return errors.New("cannot write allocation with an invalid window")
	}

	data, err := set.MarshalBinary()
	if err != nil {
		return fmt.Errorf("encoding allocation ETL bucket: %w", err)
	}

	name := s.recordPath(etlAllocationKind, set.Window.Duration(), set.Start(), set.End(), revision)
	markerName := etlBucketMarkerPath(name)
	marker := etlBucketMarker{
		Complete:           true,
		WrittenAt:          writtenAt.UTC(),
		Start:              set.Start().UTC(),
		End:                set.End().UTC(),
		ResolutionSeconds:  int64(set.Window.Duration() / time.Second),
		PricingFingerprint: revision.pricingFingerprint,
		AllocationCount:    set.Length(),
	}
	if !coverage.Start.IsZero() && !coverage.End.IsZero() {
		marker.PrometheusCoverageStart = coverage.Start.UTC()
		marker.PrometheusCoverageEnd = coverage.End.UTC()
	}
	markerData, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encoding allocation ETL marker: %w", err)
	}

	// A ready marker is the commit record. Remove it before replacing the data
	// so a failed rewrite cannot leave an old marker pointing at partial data.
	if err := s.removeIfExists(markerName); err != nil {
		return fmt.Errorf("invalidating allocation ETL marker %q: %w", markerName, err)
	}
	s.memory.Delete(name)
	if err := s.storage.Write(name, data); err != nil {
		return fmt.Errorf("writing allocation ETL bucket %q: %w", name, err)
	}
	if err := s.storage.Write(markerName, markerData); err != nil {
		return fmt.Errorf("writing allocation ETL marker %q: %w", markerName, err)
	}

	s.memory.Set(name, set.Clone(), cache.DefaultExpiration)
	return nil
}

func (s *ETLStore) ReadAllocation(start, end time.Time, resolution time.Duration) (*opencost.AllocationSet, bool, error) {
	revision, err := s.revision()
	if err != nil {
		return nil, false, err
	}
	return s.readAllocation(start, end, resolution, revision)
}

func (s *ETLStore) readAllocation(start, end time.Time, resolution time.Duration, revision etlRevision) (*opencost.AllocationSet, bool, error) {
	if s == nil {
		return nil, false, nil
	}

	name := s.recordPath(etlAllocationKind, resolution, start, end, revision)
	if value, found := s.memory.Get(name); found {
		set, ok := value.(*opencost.AllocationSet)
		if ok && set != nil {
			recordQueryCacheResult("etl_allocation_bucket", true)
			return set.Clone(), true, nil
		}
		s.memory.Delete(name)
	}

	markerName := etlBucketMarkerPath(name)
	markerData, err := s.storage.Read(markerName)
	if err != nil {
		if errors.Is(err, storage.DoesNotExistError) {
			recordQueryCacheResult("etl_allocation_bucket", false)
			return nil, false, nil
		}
		recordQueryComputeError("etl_allocation_bucket")
		return nil, false, fmt.Errorf("reading allocation ETL marker %q: %w", markerName, err)
	}
	var marker etlBucketMarker
	if err := json.Unmarshal(markerData, &marker); err != nil {
		_ = s.removeIfExists(markerName)
		recordQueryComputeError("etl_allocation_bucket")
		return nil, false, fmt.Errorf("decoding allocation ETL marker %q: %w", markerName, err)
	}
	if !marker.Complete ||
		!marker.Start.Equal(start.UTC()) ||
		!marker.End.Equal(end.UTC()) ||
		marker.ResolutionSeconds != int64(resolution/time.Second) ||
		marker.PricingFingerprint != revision.pricingFingerprint {
		_ = s.removeIfExists(markerName)
		recordQueryComputeError("etl_allocation_bucket")
		return nil, false, fmt.Errorf("allocation ETL marker %q has invalid bucket metadata", markerName)
	}

	data, err := s.storage.Read(name)
	if err != nil {
		if errors.Is(err, storage.DoesNotExistError) {
			_ = s.removeIfExists(markerName)
			recordQueryCacheResult("etl_allocation_bucket", false)
			return nil, false, nil
		}
		recordQueryComputeError("etl_allocation_bucket")
		return nil, false, fmt.Errorf("reading allocation ETL bucket %q: %w", name, err)
	}

	set := &opencost.AllocationSet{}
	if err := set.UnmarshalBinary(data); err != nil {
		_ = s.removeIfExists(markerName)
		recordQueryComputeError("etl_allocation_bucket")
		return nil, false, fmt.Errorf("decoding allocation ETL bucket %q: %w", name, err)
	}
	if set.Window.Start() == nil || set.Window.End() == nil ||
		!set.Start().Equal(start) || !set.End().Equal(end) {
		_ = s.removeIfExists(markerName)
		recordQueryComputeError("etl_allocation_bucket")
		return nil, false, fmt.Errorf("allocation ETL bucket %q has unexpected window %s", name, set.Window)
	}

	recordQueryCacheResult("etl_allocation_bucket", true)
	s.memory.Set(name, set.Clone(), cache.DefaultExpiration)
	return set, true, nil
}

func (s *ETLStore) has(kind etlDataKind, start, end time.Time, resolution time.Duration) bool {
	revision, err := s.revision()
	if err != nil {
		log.Warnf("ETLStore: resolving pricing revision: %v", err)
		return false
	}
	return s.hasForRevision(kind, start, end, resolution, revision)
}

func (s *ETLStore) hasForRevision(kind etlDataKind, start, end time.Time, resolution time.Duration, revision etlRevision) bool {
	if s == nil {
		return false
	}
	name := s.recordPath(kind, resolution, start, end, revision)
	if _, found := s.memory.Get(name); found {
		return true
	}
	exists, err := s.storage.Exists(etlBucketMarkerPath(name))
	if err != nil {
		log.Warnf("ETLStore: checking %s bucket marker %q: %v", kind, etlBucketMarkerPath(name), err)
		return false
	}
	return exists
}

func (s *ETLStore) bucketWrittenAt(kind etlDataKind, start, end time.Time, resolution time.Duration, revision etlRevision) (time.Time, bool) {
	name := s.recordPath(kind, resolution, start, end, revision)
	markerName := etlBucketMarkerPath(name)
	data, err := s.storage.Read(markerName)
	if err != nil {
		if !errors.Is(err, storage.DoesNotExistError) {
			log.Warnf("ETLStore: checking %s bucket marker age %q: %v", kind, name, err)
		}
		return time.Time{}, false
	}
	var marker etlBucketMarker
	if err := json.Unmarshal(data, &marker); err != nil ||
		!marker.Complete ||
		!marker.Start.Equal(start.UTC()) ||
		!marker.End.Equal(end.UTC()) ||
		marker.ResolutionSeconds != int64(resolution/time.Second) ||
		marker.PricingFingerprint != revision.pricingFingerprint {
		return time.Time{}, false
	}
	return marker.WrittenAt.UTC(), true
}

func (s *ETLStore) revision() (etlRevision, error) {
	if s == nil {
		return etlRevision{}, errors.New("cannot resolve revision for nil ETL store")
	}
	if s.pricing == nil {
		return etlRevision{pricingFingerprint: defaultETLPricingFingerprint}, nil
	}
	fingerprint, err := s.pricing()
	if err != nil {
		return etlRevision{}, fmt.Errorf("resolving pricing fingerprint: %w", err)
	}
	fingerprint = sanitizeETLPathComponent(fingerprint)
	if fingerprint == "" {
		return etlRevision{}, errors.New("pricing fingerprint is empty")
	}
	return etlRevision{pricingFingerprint: fingerprint}, nil
}

func (s *ETLStore) partition(kind etlDataKind, start, end time.Time, revision etlRevision) ([]etlRangePart, float64) {
	if s == nil || !end.After(start) {
		return []etlRangePart{{Start: start, End: end}}, 0
	}

	// Calendar-based windows such as "7d" can end at a future day boundary.
	// ETL only contains completed buckets, so never send the unelapsed tail to
	// Prometheus as a live range. OpenCost otherwise extrapolates it as a full
	// range, which materially inflates asset cost and idle allocations.
	effectiveEnd := end
	if s.now != nil {
		if now := s.now().UTC(); now.Before(effectiveEnd) {
			effectiveEnd = now
		}
	}
	if !effectiveEnd.After(start) {
		return []etlRangePart{{Start: start, End: end}}, 0
	}

	parts := make([]etlRangePart, 0, 32)
	dayStart := ceilToETLBoundary(start, s.dailyDuration)
	dayEnd := floorToETLBoundary(effectiveEnd, s.dailyDuration)

	if dayEnd.After(dayStart) {
		appendETLPart(&parts, etlRangePart{Start: start, End: dayStart})
		for bucketStart := dayStart; bucketStart.Before(dayEnd); bucketStart = bucketStart.Add(s.dailyDuration) {
			bucketEnd := bucketStart.Add(s.dailyDuration)
			if s.hasForRevision(kind, bucketStart, bucketEnd, s.dailyDuration, revision) {
				appendETLPart(&parts, etlRangePart{
					Start:      bucketStart,
					End:        bucketEnd,
					Resolution: s.dailyDuration,
					FromETL:    true,
				})
				continue
			}
			s.appendHourlyPartition(&parts, kind, bucketStart, bucketEnd, revision)
		}
		appendETLPart(&parts, etlRangePart{Start: dayEnd, End: effectiveEnd})
	} else {
		s.appendHourlyPartition(&parts, kind, start, effectiveEnd, revision)
	}

	parts = compactETLParts(parts)
	covered := time.Duration(0)
	for _, part := range parts {
		if part.FromETL {
			covered += part.End.Sub(part.Start)
		}
	}

	return parts, float64(covered) / float64(effectiveEnd.Sub(start))
}

func (s *ETLStore) appendHourlyPartition(parts *[]etlRangePart, kind etlDataKind, start, end time.Time, revision etlRevision) {
	hourStart := ceilToETLBoundary(start, s.resolution)
	hourEnd := floorToETLBoundary(end, s.resolution)
	if !hourEnd.After(hourStart) {
		appendETLPart(parts, etlRangePart{Start: start, End: end})
		return
	}

	appendETLPart(parts, etlRangePart{Start: start, End: hourStart})
	for bucketStart := hourStart; bucketStart.Before(hourEnd); bucketStart = bucketStart.Add(s.resolution) {
		bucketEnd := bucketStart.Add(s.resolution)
		appendETLPart(parts, etlRangePart{
			Start:      bucketStart,
			End:        bucketEnd,
			Resolution: s.resolution,
			FromETL:    s.hasForRevision(kind, bucketStart, bucketEnd, s.resolution, revision),
		})
	}
	appendETLPart(parts, etlRangePart{Start: hourEnd, End: end})
}

func appendETLPart(parts *[]etlRangePart, part etlRangePart) {
	if !part.End.After(part.Start) {
		return
	}

	if len(*parts) > 0 {
		last := &(*parts)[len(*parts)-1]
		if !last.FromETL &&
			!part.FromETL &&
			last.Resolution == part.Resolution &&
			last.End.Equal(part.Start) {
			last.End = part.End
			return
		}
	}

	*parts = append(*parts, part)
}

func compactETLParts(parts []etlRangePart) []etlRangePart {
	compacted := make([]etlRangePart, 0, len(parts))
	for _, part := range parts {
		appendETLPart(&compacted, part)
	}
	return compacted
}

func (s *ETLStore) recordPath(kind etlDataKind, resolution time.Duration, start, end time.Time, revision etlRevision) string {
	resolutionSeconds := int64(resolution / time.Second)
	if resolutionSeconds < 1 {
		resolutionSeconds = 1
	}
	return path.Join(
		"etl",
		etlSchemaVersion,
		s.clusterID,
		revision.pricingFingerprint,
		string(kind),
		fmt.Sprintf("%ds", resolutionSeconds),
		fmt.Sprintf("%d-%d.bingen", start.Unix(), end.Unix()),
	)
}

func etlBucketMarkerPath(dataPath string) string {
	return strings.TrimSuffix(dataPath, ".bingen") + ".ready"
}

func (s *ETLStore) removeIfExists(name string) error {
	err := s.storage.Remove(name)
	if err == nil || errors.Is(err, storage.DoesNotExistError) {
		return nil
	}
	return err
}

func (s *ETLStore) cleanup(now time.Time, hourlyRetention, dailyRetention time.Duration) (int, error) {
	if s == nil || s.storage == nil {
		return 0, nil
	}
	if hourlyRetention < 0 {
		hourlyRetention = 0
	}
	if dailyRetention < 0 {
		dailyRetention = 0
	}

	versionDirs, err := s.listDirectories("etl")
	if err != nil {
		return 0, fmt.Errorf("listing ETL schema directories: %w", err)
	}

	removed := 0
	var cleanupErrors []error
	for _, versionDir := range versionDirs {
		revisionDirs, err := s.listDirectories(path.Join(versionDir, s.clusterID))
		if err != nil {
			cleanupErrors = append(cleanupErrors, err)
			continue
		}
		for _, revisionDir := range revisionDirs {
			resolutionDirs, err := s.listDirectories(path.Join(revisionDir, string(etlAllocationKind)))
			if err != nil {
				cleanupErrors = append(cleanupErrors, err)
				continue
			}
			for _, resolutionDir := range resolutionDirs {
				resolution, ok := parseETLResolutionDirectory(path.Base(resolutionDir))
				if !ok {
					continue
				}
				retention := hourlyRetention
				if resolution >= s.dailyDuration {
					retention = dailyRetention
				}
				cutoff := now.UTC().Add(-retention)

				files, err := s.storage.List(resolutionDir)
				if err != nil {
					cleanupErrors = append(cleanupErrors, err)
					continue
				}
				for _, file := range files {
					_, bucketEnd, ok := parseETLBucketFilename(file.Name)
					if !ok || bucketEnd.After(cutoff) {
						continue
					}

					dataPath := path.Join(resolutionDir, path.Base(file.Name))
					markerPath := etlBucketMarkerPath(dataPath)
					if err := s.removeIfExists(dataPath); err != nil {
						cleanupErrors = append(cleanupErrors, err)
						continue
					}
					if err := s.removeIfExists(markerPath); err != nil {
						cleanupErrors = append(cleanupErrors, err)
						continue
					}
					s.memory.Delete(dataPath)
					if strings.HasSuffix(file.Name, ".bingen") {
						removed++
					}
				}
			}
		}
	}

	return removed, errors.Join(cleanupErrors...)
}

func (s *ETLStore) listDirectories(name string) ([]string, error) {
	directories, err := s.storage.ListDirectories(name)
	if err != nil {
		return nil, err
	}

	result := make([]string, 0, len(directories))
	for _, directory := range directories {
		if directory == nil {
			continue
		}
		cleaned := path.Clean(strings.TrimSuffix(directory.Name, "/"))
		if cleaned == "." || cleaned == "" {
			continue
		}
		if cleaned != name && !strings.HasPrefix(cleaned, name+"/") {
			cleaned = path.Join(name, path.Base(cleaned))
		}
		result = append(result, cleaned)
	}
	return result, nil
}

func parseETLResolutionDirectory(name string) (time.Duration, bool) {
	if !strings.HasSuffix(name, "s") {
		return 0, false
	}
	seconds, err := strconv.ParseInt(strings.TrimSuffix(name, "s"), 10, 64)
	if err != nil || seconds <= 0 {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

func parseETLBucketFilename(name string) (time.Time, time.Time, bool) {
	name = path.Base(name)
	name = strings.TrimSuffix(strings.TrimSuffix(name, ".bingen"), ".ready")
	parts := strings.Split(name, "-")
	if len(parts) != 2 {
		return time.Time{}, time.Time{}, false
	}
	startUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	endUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	return time.Unix(startUnix, 0).UTC(), time.Unix(endUnix, 0).UTC(), true
}

func etlPricingFingerprint(pricing *models.CustomPricing) string {
	if pricing == nil {
		return defaultETLPricingFingerprint
	}

	sanitized := *pricing
	sanitized.Description = ""
	sanitized.AwsServiceKeyName = ""
	sanitized.AwsServiceKeySecret = ""
	sanitized.AlibabaServiceKeyName = ""
	sanitized.AlibabaServiceKeySecret = ""
	sanitized.AzureClientID = ""
	sanitized.AzureClientSecret = ""
	sanitized.AzureTenantID = ""
	sanitized.AzureStorageAccessKey = ""

	payload, err := json.Marshal(sanitized)
	if err != nil {
		return defaultETLPricingFingerprint
	}
	sum := sha256.Sum256(payload)
	return "pricing-v1-" + hex.EncodeToString(sum[:8])
}

func sanitizeETLPathComponent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}

	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}

	result := strings.Trim(builder.String(), "._-")
	if result == "" {
		return "default"
	}
	return result
}

func floorToETLBoundary(t time.Time, resolution time.Duration) time.Time {
	seconds := int64(resolution / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	unix := t.UTC().Unix()
	remainder := unix % seconds
	if remainder < 0 {
		remainder += seconds
	}
	return time.Unix(unix-remainder, 0).UTC()
}

func ceilToETLBoundary(t time.Time, resolution time.Duration) time.Time {
	floor := floorToETLBoundary(t, resolution)
	if floor.Equal(t.UTC()) {
		return floor
	}
	return floor.Add(resolution)
}

type etlWorkerConfig struct {
	interval                time.Duration
	startupDelay            time.Duration
	hourlyRetention         time.Duration
	dailyRetention          time.Duration
	lateDataHourlyLookback  time.Duration
	lateDataDailyLookback   time.Duration
	lateDataRefreshInterval time.Duration
	concurrency             int
	maxHourlyPerRun         int
	maxDailyPerRun          int
	refreshHourly           int
	refreshDaily            int
}

func etlWorkerConfigFromEnv() etlWorkerConfig {
	return etlWorkerConfig{
		interval:        time.Duration(coreenv.GetInt(etlWorkerIntervalSecondsEnvVar, int(defaultETLWorkerInterval/time.Second))) * time.Second,
		startupDelay:    time.Duration(coreenv.GetInt(etlWorkerStartupDelaySecondsVar, int(defaultETLWorkerStartupDelay/time.Second))) * time.Second,
		hourlyRetention: time.Duration(coreenv.GetInt(etlHourlyRetentionHoursEnvVar, int(defaultETLHourlyRetention/time.Hour))) * time.Hour,
		dailyRetention:  time.Duration(coreenv.GetInt(etlDailyRetentionDaysEnvVar, int(defaultETLDailyRetention/(24*time.Hour)))) * 24 * time.Hour,
		lateDataHourlyLookback: time.Duration(coreenv.GetInt(
			etlLateDataHourlyLookbackEnvVar,
			int(defaultETLLateDataHourlyLookback/time.Hour),
		)) * time.Hour,
		lateDataDailyLookback: time.Duration(coreenv.GetInt(
			etlLateDataDailyLookbackEnvVar,
			int(defaultETLLateDataDailyLookback/(24*time.Hour)),
		)) * 24 * time.Hour,
		lateDataRefreshInterval: time.Duration(coreenv.GetInt(
			etlLateDataRefreshIntervalEnvVar,
			int(defaultETLLateDataRefresh/time.Second),
		)) * time.Second,
		concurrency:     max(coreenv.GetInt(etlWorkerConcurrencyEnvVar, defaultETLWorkerConcurrency), 1),
		maxHourlyPerRun: max(coreenv.GetInt(etlMaxHourlyBucketsPerRunEnvVar, defaultETLMaxHourlyBucketsPerRun), 0),
		maxDailyPerRun:  max(coreenv.GetInt(etlMaxDailyBucketsPerRunEnvVar, defaultETLMaxDailyBucketsPerRun), 0),
		refreshHourly:   max(coreenv.GetInt(etlRefreshHourlyBucketsEnvVar, defaultETLRefreshHourlyBuckets), 0),
		refreshDaily:    max(coreenv.GetInt(etlRefreshDailyBucketsEnvVar, defaultETLRefreshDailyBuckets), 0),
	}
}

type ETLWorkerStatus struct {
	Enabled                bool      `json:"enabled"`
	Running                bool      `json:"running"`
	SchemaVersion          string    `json:"schemaVersion"`
	StoragePath            string    `json:"storagePath"`
	ResolutionSeconds      int64     `json:"resolutionSeconds"`
	HourlyRetentionHours   int64     `json:"hourlyRetentionHours"`
	DailyRetentionDays     int64     `json:"dailyRetentionDays"`
	LastRunStartedAt       time.Time `json:"lastRunStartedAt,omitempty"`
	LastRunCompletedAt     time.Time `json:"lastRunCompletedAt,omitempty"`
	LastRunDurationSeconds float64   `json:"lastRunDurationSeconds,omitempty"`
	LastSuccessfulRunAt    time.Time `json:"lastSuccessfulRunAt,omitempty"`
	LastError              string    `json:"lastError,omitempty"`
	HourlyBucketsAttempted int       `json:"hourlyBucketsAttempted"`
	HourlyBucketsWritten   int       `json:"hourlyBucketsWritten"`
	HourlyBucketsSkipped   int       `json:"hourlyBucketsSkipped"`
	HourlyBucketsFailed    int       `json:"hourlyBucketsFailed"`
	DailyBucketsAttempted  int       `json:"dailyBucketsAttempted"`
	DailyBucketsWritten    int       `json:"dailyBucketsWritten"`
	DailyBucketsSkipped    int       `json:"dailyBucketsSkipped"`
	DailyBucketsFailed     int       `json:"dailyBucketsFailed"`
}

// ETLWorker incrementally builds fixed-resolution allocation buckets.
// Requests never wait for this worker; they consume whichever immutable buckets
// are already present and compute only the uncovered ranges live.
type ETLWorker struct {
	accesses *Accesses
	store    *ETLStore
	config   etlWorkerConfig
	runner   *interval.IntervalRunner
	enabled  bool
	runMu    sync.Mutex
	statusMu sync.RWMutex
	status   ETLWorkerStatus
}

func NewETLWorker(a *Accesses) *ETLWorker {
	if a == nil || a.Model == nil || a.Model.ETL == nil {
		return &ETLWorker{}
	}

	config := etlWorkerConfigFromEnv()
	return &ETLWorker{
		accesses: a,
		store:    a.Model.ETL,
		config:   config,
		enabled:  true,
		status: ETLWorkerStatus{
			Enabled:              true,
			SchemaVersion:        etlSchemaVersion,
			StoragePath:          a.Model.ETL.Root(),
			ResolutionSeconds:    int64(a.Model.ETL.Resolution() / time.Second),
			HourlyRetentionHours: int64(config.hourlyRetention / time.Hour),
			DailyRetentionDays:   int64(config.dailyRetention / (24 * time.Hour)),
		},
	}
}

func (w *ETLWorker) Start() bool {
	if w == nil || !w.enabled {
		return false
	}
	if w.runner != nil {
		return false
	}

	time.AfterFunc(w.config.startupDelay, w.run)
	w.runner = interval.NewIntervalRunner(w.run, w.config.interval)
	return w.runner.Start()
}

func (w *ETLWorker) Stop() bool {
	if w == nil || w.runner == nil {
		return false
	}
	stopped := w.runner.Stop()
	w.runner = nil
	return stopped
}

func (w *ETLWorker) Status() ETLWorkerStatus {
	if w == nil {
		return ETLWorkerStatus{}
	}
	w.statusMu.RLock()
	defer w.statusMu.RUnlock()
	return w.status
}

type etlBuildTask struct {
	start      time.Time
	end        time.Time
	resolution time.Duration
}

type etlBuildResult struct {
	resolution time.Duration
	written    bool
	err        error
}

func (w *ETLWorker) run() {
	if w == nil || !w.enabled || w.store == nil {
		return
	}

	w.runMu.Lock()
	defer w.runMu.Unlock()

	started := time.Now().UTC()
	w.updateRunningStatus(started, true)

	now := started
	var backgroundErrors []error
	if removed, cleanupErr := w.store.cleanup(now, w.config.hourlyRetention, w.config.dailyRetention); cleanupErr != nil {
		log.Warnf("ETLWorker: cleaning expired buckets: %v", cleanupErr)
		backgroundErrors = append(backgroundErrors, cleanupErr)
	} else if removed > 0 {
		log.Infof("ETLWorker: removed %d expired allocation buckets", removed)
	}
	coverage, err := w.sourceCoverage(now)
	if err != nil {
		w.finishRun(started, nil, append(backgroundErrors, err)...)
		return
	}
	tasks, err := w.buildTaskList(now, coverage)
	if err != nil {
		w.finishRun(started, nil, append(backgroundErrors, err)...)
		return
	}
	if len(tasks) == 0 {
		w.finishRun(started, nil, backgroundErrors...)
		return
	}

	results := make(chan etlBuildResult, len(tasks))
	taskQueue := make(chan etlBuildTask)
	var workers sync.WaitGroup
	for range w.config.concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for task := range taskQueue {
				results <- w.buildTask(task, coverage)
			}
		}()
	}

	go func() {
		for _, task := range tasks {
			taskQueue <- task
		}
		close(taskQueue)
		workers.Wait()
		close(results)
	}()

	taskResults := make([]etlBuildResult, 0, len(tasks))
	for result := range results {
		taskResults = append(taskResults, result)
	}

	w.finishRun(started, taskResults, backgroundErrors...)
}

func (w *ETLWorker) buildTaskList(now time.Time, coverage etlSourceCoverage) ([]etlBuildTask, error) {
	resolution := w.store.resolution
	revision, err := w.store.revision()
	if err != nil {
		return nil, fmt.Errorf("resolving pricing revision for task selection: %w", err)
	}
	hourlyEnd := floorToETLBoundary(now, resolution)
	hourlyStart := hourlyEnd.Add(-w.config.hourlyRetention)
	hourlyTasks := w.selectTasksWithPolicy(hourlyStart, hourlyEnd, resolution, etlTaskSelectionPolicy{
		now:             now,
		revision:        revision,
		refreshLookback: w.config.lateDataHourlyLookback,
		refreshInterval: w.config.lateDataRefreshInterval,
		refreshLimit:    w.config.refreshHourly,
		maxTasksPerRun:  w.config.maxHourlyPerRun,
		coverage:        coverage,
	})

	dailyEnd := floorToETLBoundary(now, w.store.dailyDuration)
	dailyStart := dailyEnd.Add(-w.config.dailyRetention)
	dailyTasks := w.selectTasksWithPolicy(dailyStart, dailyEnd, w.store.dailyDuration, etlTaskSelectionPolicy{
		now:             now,
		revision:        revision,
		refreshLookback: w.config.lateDataDailyLookback,
		refreshInterval: w.config.lateDataRefreshInterval,
		refreshLimit:    w.config.refreshDaily,
		maxTasksPerRun:  w.config.maxDailyPerRun,
		coverage:        coverage,
	})

	tasks := make([]etlBuildTask, 0, len(hourlyTasks)+len(dailyTasks))
	tasks = append(tasks, hourlyTasks...)
	tasks = append(tasks, dailyTasks...)
	return tasks, nil
}

func (w *ETLWorker) selectTasks(start, end time.Time, resolution time.Duration, refreshCount, limit int) []etlBuildTask {
	revision, err := w.store.revision()
	if err != nil {
		return nil
	}
	return w.selectTasksWithPolicy(start, end, resolution, etlTaskSelectionPolicy{
		now:             end,
		revision:        revision,
		refreshLookback: time.Duration(refreshCount) * resolution,
		maxTasksPerRun:  limit,
	})
}

type etlTaskSelectionPolicy struct {
	now             time.Time
	revision        etlRevision
	refreshLookback time.Duration
	refreshInterval time.Duration
	refreshLimit    int
	maxTasksPerRun  int
	coverage        etlSourceCoverage
}

func (w *ETLWorker) selectTasksWithPolicy(start, end time.Time, resolution time.Duration, policy etlTaskSelectionPolicy) []etlBuildTask {
	if !end.After(start) {
		return nil
	}

	refreshStart := end.Add(-policy.refreshLookback)
	missing := make([]etlBuildTask, 0, min(int(end.Sub(start)/resolution), limitOrAll(policy.maxTasksPerRun)))
	refresh := make([]etlBuildTask, 0, max(policy.refreshLimit, 0))
	for bucketStart := end.Add(-resolution); !bucketStart.Before(start); bucketStart = bucketStart.Add(-resolution) {
		bucketEnd := bucketStart.Add(resolution)
		if policy.coverage.excludes(bucketStart, bucketEnd) {
			continue
		}
		allocationReady := w.store.hasForRevision(
			etlAllocationKind,
			bucketStart,
			bucketEnd,
			resolution,
			policy.revision,
		)
		if !allocationReady {
			missing = append(missing, etlBuildTask{
				start:      bucketStart,
				end:        bucketEnd,
				resolution: resolution,
			})
			continue
		}

		inLateDataWindow := !bucketStart.Before(refreshStart)
		if !inLateDataWindow {
			continue
		}
		writtenAt, validMarker := w.store.bucketWrittenAt(
			etlAllocationKind,
			bucketStart,
			bucketEnd,
			resolution,
			policy.revision,
		)
		if !validMarker {
			missing = append(missing, etlBuildTask{
				start:      bucketStart,
				end:        bucketEnd,
				resolution: resolution,
			})
			continue
		}
		refreshDue := policy.refreshInterval <= 0 ||
			!writtenAt.After(policy.now.Add(-policy.refreshInterval))
		if inLateDataWindow && refreshDue && len(refresh) < policy.refreshLimit {
			refresh = append(refresh, etlBuildTask{
				start:      bucketStart,
				end:        bucketEnd,
				resolution: resolution,
			})
		}
	}

	tasks := append(missing, refresh...)
	if policy.maxTasksPerRun > 0 && len(tasks) > policy.maxTasksPerRun {
		tasks = tasks[:policy.maxTasksPerRun]
	}
	return tasks
}

func limitOrAll(limit int) int {
	if limit > 0 {
		return limit
	}
	return 1
}

func (w *ETLWorker) sourceCoverage(now time.Time) (etlSourceCoverage, error) {
	if w == nil || w.accesses == nil || w.accesses.Model == nil || w.accesses.Model.DataSource == nil {
		return etlSourceCoverage{}, errors.New("ETL data source is unavailable")
	}

	resolution := w.store.resolution
	hourlyEnd := floorToETLBoundary(now, resolution)
	earliest := hourlyEnd.Add(-w.config.hourlyRetention)
	dailyEnd := floorToETLBoundary(now, w.store.dailyDuration)
	dailyStart := dailyEnd.Add(-w.config.dailyRetention)
	if dailyStart.Before(earliest) {
		earliest = dailyStart
	}
	limitDays := max(int(now.Sub(earliest).Hours()/24)+2, 1)
	metrics := w.accesses.Model.DataSource.Metrics()
	if metrics == nil {
		return etlSourceCoverage{}, errors.New("ETL metrics querier is unavailable")
	}
	oldest, newest, err := queryETLDataCoverage(metrics, limitDays)
	if err != nil {
		return etlSourceCoverage{}, fmt.Errorf("querying data source coverage: %w", err)
	}
	if oldest.IsZero() || newest.IsZero() {
		return etlSourceCoverage{}, errors.New("data source returned empty coverage window")
	}
	tolerance := w.accesses.Model.DataSource.Resolution()
	if tolerance <= 0 {
		tolerance = etlCoverageTolerance
	}
	return etlSourceCoverage{Start: oldest.UTC(), End: newest.UTC(), Tolerance: tolerance}, nil
}

func (c etlSourceCoverage) covers(start, end time.Time) bool {
	if c.Start.IsZero() || c.End.IsZero() {
		return false
	}
	tolerance := c.tolerance()
	return !c.Start.After(start.Add(tolerance)) &&
		!c.End.Add(tolerance).Before(end)
}

func (c etlSourceCoverage) excludes(start, end time.Time) bool {
	if c.Start.IsZero() || c.End.IsZero() {
		return false
	}
	tolerance := c.tolerance()
	if !end.After(c.Start.Add(tolerance)) {
		return true
	}
	return !c.End.Add(tolerance).After(start)
}

func (c etlSourceCoverage) tolerance() time.Duration {
	if c.Tolerance > 0 {
		return c.Tolerance
	}
	return etlCoverageTolerance
}

func (w *ETLWorker) buildTask(task etlBuildTask, coverage etlSourceCoverage) etlBuildResult {
	result := etlBuildResult{resolution: task.resolution}
	started := time.Now()
	defer func() {
		observeQueryComputeDuration("etl_bucket_build", time.Since(started))
	}()

	if !coverage.covers(task.start, task.end) {
		log.Debugf(
			"ETLWorker: deferring %s bucket %s; source coverage is %s",
			task.resolution,
			opencost.NewClosedWindow(task.start, task.end),
			opencost.NewClosedWindow(coverage.Start, coverage.End),
		)
		return result
	}

	revision, err := w.store.revision()
	if err != nil {
		recordQueryComputeError("etl_bucket_build")
		result.err = err
		return result
	}

	allocationSet, err := w.allocationForTask(task)
	if err == nil {
		currentRevision, revisionErr := w.store.revision()
		if revisionErr != nil {
			err = revisionErr
		} else if currentRevision != revision {
			// Pricing changed while the bucket was being computed. Do not label
			// old-priced data with the new revision; the next run will rebuild it.
			log.Infof("ETLWorker: pricing changed while building %s starting %s; deferring bucket", task.resolution, task.start)
			return result
		} else {
			err = w.store.writeAllocation(allocationSet, revision, time.Now().UTC(), coverage)
		}
	}
	if err != nil {
		recordQueryComputeError("etl_bucket_build")
		result.err = err
		log.Warnf("ETLWorker: building %s starting %s: %v", task.resolution, task.start, err)
		return result
	}

	result.written = true
	return result
}

func (w *ETLWorker) allocationForTask(task etlBuildTask) (*opencost.AllocationSet, error) {
	// Daily backfill must not reuse hourly buckets that another task in this run
	// may still be refreshing. A direct query gives the daily bucket one coherent
	// snapshot of late-arriving data and current pricing.
	return w.computeAllocationBucket(task.start, task.end)
}

func (w *ETLWorker) computeAllocationBucket(start, end time.Time) (*opencost.AllocationSet, error) {
	resolution := time.Duration(0)
	if w.accesses.Model.DataSource != nil {
		resolution = w.accesses.Model.DataSource.Resolution()
	}
	set, _, err := w.accesses.Model.computeAllocationStepUncached(start, end, resolution, nil)
	if err != nil {
		return nil, fmt.Errorf("computing allocation bucket %s: %w", opencost.NewClosedWindow(start, end), err)
	}
	set.Window = opencost.NewClosedWindow(start, end)
	return set, nil
}

func (w *ETLWorker) updateRunningStatus(started time.Time, running bool) {
	w.statusMu.Lock()
	defer w.statusMu.Unlock()
	w.status.Running = running
	w.status.LastRunStartedAt = started
	w.status.HourlyBucketsAttempted = 0
	w.status.HourlyBucketsWritten = 0
	w.status.HourlyBucketsSkipped = 0
	w.status.HourlyBucketsFailed = 0
	w.status.DailyBucketsAttempted = 0
	w.status.DailyBucketsWritten = 0
	w.status.DailyBucketsSkipped = 0
	w.status.DailyBucketsFailed = 0
}

func (w *ETLWorker) finishRun(started time.Time, results []etlBuildResult, backgroundErrors ...error) {
	completed := time.Now().UTC()
	runErrors := append([]error(nil), backgroundErrors...)

	w.statusMu.Lock()
	defer w.statusMu.Unlock()

	for _, result := range results {
		if result.resolution == w.store.dailyDuration {
			w.status.DailyBucketsAttempted++
			if result.err != nil {
				w.status.DailyBucketsFailed++
				runErrors = append(runErrors, result.err)
			} else if result.written {
				w.status.DailyBucketsWritten++
			} else {
				w.status.DailyBucketsSkipped++
			}
			continue
		}

		w.status.HourlyBucketsAttempted++
		if result.err != nil {
			w.status.HourlyBucketsFailed++
			runErrors = append(runErrors, result.err)
		} else if result.written {
			w.status.HourlyBucketsWritten++
		} else {
			w.status.HourlyBucketsSkipped++
		}
	}

	w.status.Running = false
	w.status.LastRunCompletedAt = completed
	w.status.LastRunDurationSeconds = completed.Sub(started).Seconds()
	if len(runErrors) > 0 {
		w.status.LastError = errors.Join(runErrors...).Error()
	} else {
		w.status.LastError = ""
		w.status.LastSuccessfulRunAt = completed
	}
}
