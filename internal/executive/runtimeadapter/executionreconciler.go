package runtimeadapter

import (
	"context"

	"github.com/Mireuz13/explorarte-organization/internal/executive"
	"github.com/Mireuz13/explorarte-organization/internal/modelruntime"
)

// ExecutionReconciler adapts Model Runtime's own reconciliation to the port
// the Executive worker calls on every pass.
//
// It exists so the Executive can guarantee the sweep HAPPENS without knowing
// what it does. Model Runtime decides what an unresolved execution is and how
// to settle it; this only carries the call across the boundary and drops the
// result, because the worker acts on none of it -- the recovery is durable
// and the next pass sees it as ordinary state.
type ExecutionReconciler struct {
	Invocations *modelruntime.InvocationService
}

var _ executive.ExecutionReconciler = ExecutionReconciler{}

func (r ExecutionReconciler) Reconcile(ctx context.Context, batch int) error {
	if r.Invocations == nil {
		return nil
	}
	_, err := r.Invocations.Reconcile(ctx, batch)
	return err
}
