package costmodel

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClampQueryEndToNow(t *testing.T) {
	now := time.Date(2026, 11, 1, 11, 27, 42, 0, time.UTC)

	t.Run("historical end is unchanged", func(t *testing.T) {
		end := now.Add(-time.Hour)
		assert.Equal(t, end, clampQueryEndToNow(end, now, time.Minute))
	})

	t.Run("future end is clamped to data resolution", func(t *testing.T) {
		end := now.Add(12 * time.Hour)
		assert.Equal(t, time.Date(2026, 11, 1, 11, 27, 0, 0, time.UTC), clampQueryEndToNow(end, now, time.Minute))
	})

	t.Run("zero resolution defaults to minute", func(t *testing.T) {
		end := now.Add(time.Hour)
		assert.Equal(t, time.Date(2026, 11, 1, 11, 27, 0, 0, time.UTC), clampQueryEndToNow(end, now, 0))
	})
}
