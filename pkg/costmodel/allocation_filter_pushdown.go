package costmodel

import (
	"github.com/opencost/opencost/core/pkg/filter/allocation"
	"github.com/opencost/opencost/core/pkg/filter/ast"
	"github.com/opencost/opencost/core/pkg/source"
)

type allocationQueryPushdown struct {
	cluster   string
	namespace string
}

func (p *allocationQueryPushdown) empty() bool {
	return p == nil || (p.cluster == "" && p.namespace == "")
}

type allocationPushdownMetricsQuerier interface {
	WithAllocationFilter(cluster, namespace string) source.MetricsQuerier
}

func allocationPushdownFromFilter(filter ast.FilterNode) *allocationQueryPushdown {
	pushdown, ok := extractAllocationPushdown(filter)
	if !ok || pushdown.empty() {
		return nil
	}
	return pushdown
}

func extractAllocationPushdown(filter ast.FilterNode) (*allocationQueryPushdown, bool) {
	switch node := filter.(type) {
	case *ast.VoidOp:
		return nil, true
	case *ast.EqualOp:
		return pushdownFromEqual(node)
	case *ast.AndOp:
		result := &allocationQueryPushdown{}
		for _, operand := range node.Operands {
			next, ok := extractAllocationPushdown(operand)
			if !ok {
				continue
			}
			if next == nil {
				continue
			}
			if !mergeAllocationPushdown(result, next) {
				return nil, false
			}
		}
		return result, true
	default:
		return nil, false
	}
}

func pushdownFromEqual(eq *ast.EqualOp) (*allocationQueryPushdown, bool) {
	if eq == nil || eq.Left.Field == nil || eq.Left.Key != "" || eq.Right == "" {
		return nil, false
	}

	switch allocation.AllocationField(eq.Left.Field.Name) {
	case allocation.FieldClusterID:
		return &allocationQueryPushdown{cluster: eq.Right}, true
	case allocation.FieldNamespace:
		return &allocationQueryPushdown{namespace: eq.Right}, true
	default:
		return nil, false
	}
}

func mergeAllocationPushdown(dst, src *allocationQueryPushdown) bool {
	if dst == nil || src == nil {
		return true
	}
	if src.cluster != "" {
		if dst.cluster != "" && dst.cluster != src.cluster {
			return false
		}
		dst.cluster = src.cluster
	}
	if src.namespace != "" {
		if dst.namespace != "" && dst.namespace != src.namespace {
			return false
		}
		dst.namespace = src.namespace
	}
	return true
}
