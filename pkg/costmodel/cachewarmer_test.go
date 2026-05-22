package costmodel

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewCacheWarmer_DisabledByDefault(t *testing.T) {
	t.Setenv(cacheWarmerEnabledEnvVar, "")
	cw := NewCacheWarmer(nil)
	assert.False(t, cw.enabled)
}

func TestNewCacheWarmer_EnabledWithDefaults(t *testing.T) {
	t.Setenv(cacheWarmerEnabledEnvVar, "true")
	cw := NewCacheWarmer(&Accesses{})
	assert.True(t, cw.enabled)
	assert.Equal(t, defaultCacheWarmerInterval, cw.interval)
	assert.Equal(t, defaultCacheWarmerStartupDelay, cw.startupDelay)
	assert.NotEmpty(t, cw.endpoints)
}

func TestNewCacheWarmer_CustomEndpoints(t *testing.T) {
	t.Setenv(cacheWarmerEnabledEnvVar, "true")
	t.Setenv(cacheWarmerEndpointsEnvVar, "/allocation?window=1d;/assets?window=1d")
	cw := NewCacheWarmer(&Accesses{})
	assert.Equal(t, []string{"/allocation?window=1d", "/assets?window=1d"}, cw.endpoints)
}

func TestNewCacheWarmer_ExcludesMutatingEndpoints(t *testing.T) {
	t.Setenv(cacheWarmerEnabledEnvVar, "true")
	t.Setenv(cacheWarmerEndpointsEnvVar, "/allocation?window=1d;POST /allocation;DELETE /assets")
	cw := NewCacheWarmer(&Accesses{})
	assert.Equal(t, []string{"/allocation?window=1d"}, cw.endpoints)
}

func TestNewCacheWarmer_CustomInterval(t *testing.T) {
	t.Setenv(cacheWarmerEnabledEnvVar, "true")
	t.Setenv(cacheWarmerIntervalEnvVar, "600")
	cw := NewCacheWarmer(&Accesses{})
	assert.Equal(t, 10*time.Minute, cw.interval)
}

func TestCacheWarmer_StartStop(t *testing.T) {
	t.Setenv(cacheWarmerEnabledEnvVar, "true")
	cw := NewCacheWarmer(&Accesses{})
	assert.True(t, cw.Start())
	assert.False(t, cw.Start(), "double start should return false")
	assert.True(t, cw.Stop())
}

func TestWarmHandlerForPath(t *testing.T) {
	a := &Accesses{}

	tests := []struct {
		path     string
		hasMatch bool
	}{
		{"/allocation", true},
		{"/allocation/compute", true},
		{"/allocation/summary", true},
		{"/allocation/summary/topline", true},
		{"/assets", true},
		{"/assets/graph", true},
		{"/efficiency", true},
		{"/efficiency/clusters", true},
		{"/efficiency/clusters/summary", true},
		{RoutePrefix + "/allocation", true},
		{RoutePrefix + "/allocation/summary", true},
		{RoutePrefix + "/assets/graph", true},
		{"/unknown", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			h := a.warmHandlerForPath(tt.path)
			if tt.hasMatch {
				assert.NotNil(t, h, "expected handler for %s", tt.path)
			} else {
				assert.Nil(t, h, "expected no handler for %s", tt.path)
			}
		})
	}
}

func TestParseEndpoints_StripsGetPrefix(t *testing.T) {
	result := parseEndpoints("GET /allocation?window=7d;GET /assets?window=7d")
	assert.Equal(t, []string{"/allocation?window=7d", "/assets?window=7d"}, result)
}
