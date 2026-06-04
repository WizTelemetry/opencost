package costmodel

import (
	"testing"
	"time"

	"github.com/opencost/opencost/core/pkg/opencost"
)

func TestBuildAssetAutocompleteQueriesAssetsAndReturnsSortedUniqueValues(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)
	window := opencost.NewClosedWindow(start, end)

	called := false
	values, err := buildAssetAutocomplete(window, "label", "team", `assetType:"node"`, func(window opencost.Window, filter string) (*opencost.AssetSet, error) {
		called = true
		if filter != `assetType:"node"` {
			t.Fatalf("expected filter to pass through, got %q", filter)
		}

		return mockAssetSetForAutocomplete(start, end), nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatalf("expected asset query to be called")
	}

	expected := []string{"team"}
	if len(values) != len(expected) {
		t.Fatalf("expected %d values, got %d: %#v", len(expected), len(values), values)
	}
	for i := range expected {
		if values[i] != expected[i] {
			t.Fatalf("expected values[%d]=%q, got %q", i, expected[i], values[i])
		}
	}
}

func TestCollectAssetAutocompleteValuesSupportsMultipleFields(t *testing.T) {
	start := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	assetSet := mockAssetSetForAutocomplete(start, end)

	testCases := []struct {
		field    string
		search   string
		expected []string
	}{
		{field: assetAutocompleteFieldCluster, expected: []string{"cluster-a", "cluster-b"}},
		{field: assetAutocompleteFieldNode, expected: []string{"cluster-a/node-a"}},
		{field: assetAutocompleteFieldProviderID, expected: []string{"disk-a-id", "node-a-id"}},
		{field: assetAutocompleteFieldName, expected: []string{"disk-a", "node-a"}},
		{field: assetAutocompleteFieldAssetType, expected: []string{"Disk", "Node"}},
		{field: assetAutocompleteFieldLabel, search: "team", expected: []string{"team"}},
		{field: "labels", search: "team", expected: []string{"team"}},
		{field: "label[team]", expected: []string{"platform", "storage"}},
		{field: "labels[team]", expected: []string{"platform", "storage"}},
		{field: "label:owner", expected: []string{"finops"}},
	}

	for _, tc := range testCases {
		t.Run(tc.field, func(t *testing.T) {
			values := collectAssetAutocompleteValues(assetSet, assetAutocompleteFieldSpec{field: tc.field}, tc.search)
			if fieldSpec, err := normalizeAssetAutocompleteFieldSpec(tc.field); err == nil {
				values = collectAssetAutocompleteValues(assetSet, fieldSpec, tc.search)
			}

			if len(values) != len(tc.expected) {
				t.Fatalf("expected %d values, got %d: %#v", len(tc.expected), len(values), values)
			}
			for i := range tc.expected {
				if values[i] != tc.expected[i] {
					t.Fatalf("expected values[%d]=%q, got %q", i, tc.expected[i], values[i])
				}
			}
		})
	}
}

func TestNormalizeAssetAutocompleteFieldSpecSupportsMappedFields(t *testing.T) {
	testCases := []struct {
		field         string
		expectedField string
		expectedKey   string
	}{
		{field: "label[team]", expectedField: assetAutocompleteFieldLabel, expectedKey: "team"},
		{field: "labels", expectedField: assetAutocompleteFieldLabel},
		{field: "labels:owner", expectedField: assetAutocompleteFieldLabel, expectedKey: "owner"},
		{field: "provider", expectedField: assetAutocompleteFieldProviderID},
	}

	for _, tc := range testCases {
		t.Run(tc.field, func(t *testing.T) {
			fieldSpec, err := normalizeAssetAutocompleteFieldSpec(tc.field)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if fieldSpec.field != tc.expectedField {
				t.Fatalf("expected field %q, got %q", tc.expectedField, fieldSpec.field)
			}
			if fieldSpec.key != tc.expectedKey {
				t.Fatalf("expected key %q, got %q", tc.expectedKey, fieldSpec.key)
			}
		})
	}
}

func mockAssetSetForAutocomplete(start, end time.Time) *opencost.AssetSet {
	window := opencost.NewClosedWindow(start, end)

	node := opencost.NewNode("node-a", "cluster-a", "node-a-id", start, end, window)
	node.SetLabels(opencost.AssetLabels{
		"team":  "platform",
		"owner": "finops",
	})

	disk := opencost.NewDisk("disk-a", "cluster-b", "disk-a-id", start, end, window)
	disk.SetLabels(opencost.AssetLabels{
		"team": "storage",
	})

	network := opencost.NewNetwork("network-a", "cluster-c", "network-a-id", start, end, window)
	network.SetLabels(opencost.AssetLabels{
		"team": "network",
	})

	return opencost.NewAssetSet(start, end, node, disk, network)
}
