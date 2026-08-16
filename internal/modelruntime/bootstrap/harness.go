package bootstrap

import (
	"errors"

	modelruntimeadapter "github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
)

// NewHarnessModelExecutor wires the Execution Harness to the same invocation
// and dispatch services used by the production Model Runtime. In particular,
// it does not expose the provider registry or persistence stores to the
// Harness and therefore cannot bypass assignment, egress, execution identity,
// usage, or cost gates configured by Open.
func (r *Runtime) NewHarnessModelExecutor(config modelruntimeadapter.Config) (*modelruntimeadapter.Adapter, error) {
	if r == nil || r.Invocations == nil || r.Dispatch == nil {
		return nil, errors.New("model runtime is not open")
	}
	return modelruntimeadapter.New(r.Invocations, r.Dispatch, nil, config)
}
