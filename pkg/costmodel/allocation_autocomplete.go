package costmodel

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/julienschmidt/httprouter"
	"github.com/opencost/opencost/core/pkg/opencost"
	"github.com/opencost/opencost/core/pkg/util/httputil"
	"github.com/opencost/opencost/core/pkg/util/promutil"
	"github.com/opencost/opencost/pkg/env"
)

type allocationQueryFunc func(window opencost.Window, step time.Duration, aggregate []string, includeIdle, idleByNode, includeProportionalAssetResourceCosts, includeAggregatedMetadata, sharedLoadBalancer bool, accumulateBy opencost.AccumulateOption, shareIdle bool, filterString string) (*opencost.AllocationSetRange, error)

const (
	allocationAutocompleteFieldLabel          = "label"
	allocationAutocompleteFieldAnnotation     = "annotation"
	allocationAutocompleteFieldCluster        = "cluster"
	allocationAutocompleteFieldNode           = "node"
	allocationAutocompleteFieldNamespace      = "namespace"
	allocationAutocompleteFieldPod            = "pod"
	allocationAutocompleteFieldContainer      = "container"
	allocationAutocompleteFieldController     = "controller"
	allocationAutocompleteFieldControllerKind = "controllerkind"
	allocationAutocompleteFieldProviderID     = "providerid"
	allocationAutocompleteFieldService        = "service"
)

type allocationAutocompleteFieldSpec struct {
	field string
	key   string
}

// ComputeAllocationAutocompleteHandler returns autocomplete candidates for allocation fields.
// @Summary      查询分配字段自动补全候选项
// @Tags         Allocation
// @Description  兼容 Kubecost 的 allocation autocomplete 接口。当前支持 label、label[<key>]、annotation、annotation[<key>]、cluster、node、namespace、pod、container、controller、controllerKind、providerID、service。node 返回 cluster/node，pod 返回 cluster/namespace/pod，container 返回 cluster/namespace/pod/container，service 返回 cluster/namespace/service。
// @Param        window  query  string  true   "时间窗口。必填。"
// @Param        field   query  string  true   "字段名。支持 label、label[<key>]、annotation、annotation[<key>]、cluster、node、namespace、pod、container、controller、controllerKind、providerID、service。"
// @Param        search  query  string  false  "搜索关键字，按包含关系过滤候选项。"
// @Param        filter  query  string  false  "分配过滤条件。"
// @Success      200  {object}  costmodel.Response
// @Failure      400  {object}  costmodel.Response
// @Failure      500  {object}  costmodel.Response
// @Router       /allocation/autocomplete [get]
// @Router       /kapis/costwise.wiztelemetry.io/v1alpha1/allocation/autocomplete [get]
func (a *Accesses) ComputeAllocationAutocompleteHandler(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	w.Header().Set("Content-Type", "application/json")

	if resp, ok := a.getQueryCacheResponse("allocation-autocomplete", r); ok {
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
		http.Error(w, "allocation model is not initialized", http.StatusInternalServerError)
		return
	}

	values, err := buildAllocationAutocomplete(window, qp.Get("field", ""), qp.Get("search", ""), qp.Get("filter", ""), a.Model.QueryAllocation)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	resp := WrapData(map[string]any{
		"data": values,
	}, nil)
	a.setQueryCacheResponseWithTTL("allocation-autocomplete", r, resp, cacheTTLForWindow(&window))
	w.Write(resp)
}

func buildAllocationAutocomplete(window opencost.Window, field, search, filter string, query allocationQueryFunc) ([]string, error) {
	fieldSpec, err := normalizeAllocationAutocompleteFieldSpec(field)
	if err != nil {
		return nil, fmt.Errorf("invalid 'field' parameter: %w", err)
	}

	if query == nil {
		return nil, fmt.Errorf("allocation query function is nil")
	}

	asr, err := query(window, window.Duration(), nil, false, false, false, true, false, opencost.AccumulateOptionNone, false, filter)
	if err != nil {
		return nil, err
	}

	return collectAllocationAutocompleteValuesForField(asr, fieldSpec, search), nil
}

func normalizeAllocationAutocompleteField(field string) (string, error) {
	fieldSpec, err := normalizeAllocationAutocompleteFieldSpec(field)
	if err != nil {
		return "", err
	}
	return fieldSpec.field, nil
}

func normalizeAllocationAutocompleteFieldSpec(field string) (allocationAutocompleteFieldSpec, error) {
	trimmed := strings.TrimSpace(field)
	normalized := strings.ToLower(trimmed)
	if mappedField, key, ok := parseAutocompleteMappedField(trimmed, allocationAutocompleteFieldLabel, "labels"); ok {
		return allocationAutocompleteFieldSpec{field: mappedField, key: key}, nil
	}
	if mappedField, key, ok := parseAutocompleteMappedField(trimmed, allocationAutocompleteFieldAnnotation, "annotations"); ok {
		return allocationAutocompleteFieldSpec{field: mappedField, key: key}, nil
	}

	switch normalized {
	case allocationAutocompleteFieldLabel, "labels":
		return allocationAutocompleteFieldSpec{field: allocationAutocompleteFieldLabel}, nil
	case allocationAutocompleteFieldAnnotation, "annotations":
		return allocationAutocompleteFieldSpec{field: allocationAutocompleteFieldAnnotation}, nil
	case allocationAutocompleteFieldCluster:
		return allocationAutocompleteFieldSpec{field: allocationAutocompleteFieldCluster}, nil
	case allocationAutocompleteFieldNode:
		return allocationAutocompleteFieldSpec{field: allocationAutocompleteFieldNode}, nil
	case allocationAutocompleteFieldNamespace:
		return allocationAutocompleteFieldSpec{field: allocationAutocompleteFieldNamespace}, nil
	case allocationAutocompleteFieldPod:
		return allocationAutocompleteFieldSpec{field: allocationAutocompleteFieldPod}, nil
	case allocationAutocompleteFieldContainer:
		return allocationAutocompleteFieldSpec{field: allocationAutocompleteFieldContainer}, nil
	case allocationAutocompleteFieldController, "controllername":
		return allocationAutocompleteFieldSpec{field: allocationAutocompleteFieldController}, nil
	case allocationAutocompleteFieldControllerKind:
		return allocationAutocompleteFieldSpec{field: allocationAutocompleteFieldControllerKind}, nil
	case allocationAutocompleteFieldProviderID, "provider":
		return allocationAutocompleteFieldSpec{field: allocationAutocompleteFieldProviderID}, nil
	case allocationAutocompleteFieldService, "services":
		return allocationAutocompleteFieldSpec{field: allocationAutocompleteFieldService}, nil
	default:
		return allocationAutocompleteFieldSpec{}, fmt.Errorf("unsupported field %q", field)
	}
}

func parseAutocompleteMappedField(field, mappedField string, aliases ...string) (string, string, bool) {
	normalized := strings.ToLower(field)
	fields := append([]string{mappedField}, aliases...)
	for _, candidate := range fields {
		if strings.HasPrefix(normalized, candidate+":") {
			key := strings.TrimSpace(field[len(candidate)+1:])
			if key == "" {
				return mappedField, "", true
			}
			return mappedField, promutil.SanitizeLabelName(key), true
		}

		prefix := candidate + "["
		if strings.HasPrefix(normalized, prefix) && strings.HasSuffix(field, "]") {
			key := strings.TrimSpace(field[len(prefix) : len(field)-1])
			if key == "" {
				return mappedField, "", true
			}
			return mappedField, promutil.SanitizeLabelName(key), true
		}
	}

	return "", "", false
}

func collectAllocationAutocompleteValues(asr *opencost.AllocationSetRange, field, search string) []string {
	fieldSpec, err := normalizeAllocationAutocompleteFieldSpec(field)
	if err != nil {
		fieldSpec = allocationAutocompleteFieldSpec{field: field}
	}
	return collectAllocationAutocompleteValuesForField(asr, fieldSpec, search)
}

func collectAllocationAutocompleteValuesForField(asr *opencost.AllocationSetRange, fieldSpec allocationAutocompleteFieldSpec, search string) []string {
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

	addPathValue := func(parts ...string) {
		addValue(joinAutocompletePath(parts...))
	}

	normalizeServicePath := func(namespace, service string) string {
		service = strings.Trim(service, "/ ")
		if service == "" {
			return ""
		}
		if strings.Contains(service, "/") {
			return service
		}
		return joinAutocompletePath(namespace, service)
	}

	if asr != nil {
		for _, as := range asr.Allocations {
			if as == nil {
				continue
			}
			for _, alloc := range as.Allocations {
				if alloc == nil || alloc.Properties == nil {
					continue
				}

				props := alloc.Properties
				switch fieldSpec.field {
				case allocationAutocompleteFieldLabel:
					if fieldSpec.key != "" {
						addMapValue(props.Labels, fieldSpec.key)
						addMapValue(props.NamespaceLabels, fieldSpec.key)
					} else {
						addMapKeys(props.Labels)
						addMapKeys(props.NamespaceLabels)
					}
				case allocationAutocompleteFieldAnnotation:
					if fieldSpec.key != "" {
						addMapValue(props.Annotations, fieldSpec.key)
						addMapValue(props.NamespaceAnnotations, fieldSpec.key)
					} else {
						addMapKeys(props.Annotations)
						addMapKeys(props.NamespaceAnnotations)
					}
				case allocationAutocompleteFieldCluster:
					addValue(props.Cluster)
				case allocationAutocompleteFieldNode:
					addPathValue(props.Cluster, props.Node)
				case allocationAutocompleteFieldNamespace:
					addPathValue(props.Cluster, props.Namespace)
				case allocationAutocompleteFieldPod:
					addPathValue(props.Cluster, props.Namespace, props.Pod)
				case allocationAutocompleteFieldContainer:
					addPathValue(props.Cluster, props.Namespace, props.Pod, props.Container)
				case allocationAutocompleteFieldController:
					addPathValue(props.Cluster, props.Namespace, props.Controller)
				case allocationAutocompleteFieldControllerKind:
					addValue(props.ControllerKind)
				case allocationAutocompleteFieldProviderID:
					addValue(props.ProviderID)
				case allocationAutocompleteFieldService:
					for _, service := range props.Services {
						addPathValue(props.Cluster, normalizeServicePath(props.Namespace, service))
					}
				}
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
