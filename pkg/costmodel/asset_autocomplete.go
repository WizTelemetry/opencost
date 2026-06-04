package costmodel

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/julienschmidt/httprouter"
	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/opencost/opencost/core/pkg/util/httputil"
	"github.com/opencost/opencost/pkg/env"
)

const (
	assetAutocompleteFieldLabel      = "label"
	assetAutocompleteFieldCluster    = "cluster"
	assetAutocompleteFieldNode       = "node"
	assetAutocompleteFieldProviderID = "providerid"
	assetAutocompleteFieldName       = "name"
	assetAutocompleteFieldAssetType  = "assettype"
)

type assetAutocompleteFieldSpec struct {
	field string
	key   string
}

// ComputeAssetAutocompleteHandler returns autocomplete candidates for asset fields.
// @Summary      查询资产字段自动补全候选项
// @Tags         Asset
// @Description  资产 autocomplete 接口。当前支持 label、label[<key>]、cluster、node、providerID、name、assetType。只从 node 和 disk 类型的资产中提取数据。
// @Param        window  query  string  true   "时间窗口。必填。"
// @Param        field   query  string  true   "字段名。支持 label、label[<key>]、cluster、node、providerID、name、assetType。"
// @Param        search  query  string  false  "搜索关键字，按包含关系过滤候选项。"
// @Param        cluster query  string  false  "按集群过滤资产。会与 filter 做 AND 合并。"
// @Param        filter  query  string  false  "资产过滤条件。语法与 /assets 保持一致。"
// @Success      200  {object}  costmodel.Response
// @Failure      400  {object}  costmodel.Response
// @Failure      500  {object}  costmodel.Response
// @Router       /kapis/costwise.wiztelemetry.io/v1alpha1/assets/autocomplete [get]
func (a *Accesses) ComputeAssetAutocompleteHandler(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	w.Header().Set("Content-Type", "application/json")

	if resp, ok := a.getQueryCacheResponse("asset-autocomplete", r); ok {
		w.Write(resp)
		return
	}

	qp := httputil.NewQueryParams(r.URL.Query())
	window, err := opencost.ParseWindowWithOffset(qp.Get("window", ""), env.GetParsedUTCOffset())
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid 'window' parameter: %s", err), http.StatusBadRequest)
		return
	}

	if a == nil || a.Model == nil {
		http.Error(w, "asset model is not initialized", http.StatusInternalServerError)
		return
	}

	filterString := buildAssetFilterString(qp.Get("filter", ""), qp.Get("cluster", ""))

	values, err := buildAssetAutocomplete(window, qp.Get("field", ""), qp.Get("search", ""), filterString, a.computeAssetsFromCostmodel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp := WrapData(map[string]any{
		"data": values,
	}, nil)
	a.setQueryCacheResponseWithTTL("asset-autocomplete", r, resp, cacheTTLForWindow(&window))
	w.Write(resp)
}

func buildAssetAutocomplete(
	window opencost.Window,
	field, search, filterString string,
	queryFunc func(opencost.Window, string) (*opencost.AssetSet, error),
) ([]string, error) {
	fieldSpec, err := normalizeAssetAutocompleteFieldSpec(field)
	if err != nil {
		return nil, fmt.Errorf("invalid 'field' parameter: %w", err)
	}

	if queryFunc == nil {
		return nil, fmt.Errorf("asset query function is nil")
	}

	assetSet, err := queryFunc(window, filterString)
	if err != nil {
		return nil, err
	}

	return collectAssetAutocompleteValues(assetSet, fieldSpec, search), nil
}

func normalizeAssetAutocompleteFieldSpec(field string) (assetAutocompleteFieldSpec, error) {
	trimmed := strings.TrimSpace(field)
	normalized := strings.ToLower(trimmed)

	if mappedField, key, ok := parseAutocompleteMappedField(trimmed, assetAutocompleteFieldLabel, "labels"); ok {
		return assetAutocompleteFieldSpec{field: mappedField, key: key}, nil
	}

	switch normalized {
	case assetAutocompleteFieldLabel, "labels":
		return assetAutocompleteFieldSpec{field: assetAutocompleteFieldLabel}, nil
	case assetAutocompleteFieldCluster:
		return assetAutocompleteFieldSpec{field: assetAutocompleteFieldCluster}, nil
	case assetAutocompleteFieldNode:
		return assetAutocompleteFieldSpec{field: assetAutocompleteFieldNode}, nil
	case assetAutocompleteFieldProviderID, "provider":
		return assetAutocompleteFieldSpec{field: assetAutocompleteFieldProviderID}, nil
	case assetAutocompleteFieldName:
		return assetAutocompleteFieldSpec{field: assetAutocompleteFieldName}, nil
	case assetAutocompleteFieldAssetType:
		return assetAutocompleteFieldSpec{field: assetAutocompleteFieldAssetType}, nil
	default:
		return assetAutocompleteFieldSpec{}, fmt.Errorf("unsupported field %q", field)
	}
}

func collectAssetAutocompleteValues(
	assetSet *opencost.AssetSet,
	fieldSpec assetAutocompleteFieldSpec,
	search string,
) []string {
	search = strings.ToLower(strings.TrimSpace(search))
	seen := make(map[string]struct{})

	addValue := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if search != "" && !strings.Contains(strings.ToLower(value), search) {
			return
		}
		seen[value] = struct{}{}
	}

	addMapKeys := func(values map[string]string) {
		for key := range values {
			addValue(key)
		}
	}

	addMapValue := func(values map[string]string, key string) {
		if value, ok := values[key]; ok {
			addValue(value)
		}
	}

	joinAutocompletePath := func(parts ...string) string {
		filtered := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.Trim(part, "/ ")
			if part == "" {
				continue
			}
			filtered = append(filtered, part)
		}
		return strings.Join(filtered, "/")
	}

	if assetSet != nil {
		for _, asset := range assetSet.Assets {
			if asset == nil {
				continue
			}

			// Only consider node and disk asset types.
			assetType := asset.Type()
			if assetType != opencost.NodeAssetType && assetType != opencost.DiskAssetType {
				continue
			}

			props := asset.GetProperties()
			if props == nil {
				continue
			}

			switch fieldSpec.field {
			case assetAutocompleteFieldLabel:
				if fieldSpec.key != "" {
					addMapValue(asset.GetLabels(), fieldSpec.key)
				} else {
					addMapKeys(asset.GetLabels())
				}
			case assetAutocompleteFieldCluster:
				addValue(props.Cluster)
			case assetAutocompleteFieldNode:
				if assetType == opencost.NodeAssetType {
					addValue(joinAutocompletePath(props.Cluster, props.Name))
				}
			case assetAutocompleteFieldProviderID:
				addValue(props.ProviderID)
			case assetAutocompleteFieldName:
				addValue(props.Name)
			case assetAutocompleteFieldAssetType:
				addValue(assetType.String())
			}
		}
	}

	values := make([]string, 0, len(seen))
	for value := range seen {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

// ParseAssetAutocompleteField parses an asset autocomplete field string for external callers
// (e.g. MCP tools) and returns the normalized field name and optional key.
func ParseAssetAutocompleteField(field string) (string, string, error) {
	spec, err := normalizeAssetAutocompleteFieldSpec(field)
	if err != nil {
		return "", "", err
	}
	return spec.field, spec.key, nil
}
