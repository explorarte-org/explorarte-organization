package bootstrap

import (
	"errors"

	modelruntimeadapter "github.com/Mireuz13/explorarte-organization/internal/executionharness/modelruntimeadapter"
	"github.com/Mireuz13/explorarte-organization/internal/executionharness/tasksauthority"
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

// NewHarnessAuthority composes the real durable task lease verifier with the
// canonical active execution-principal store used by Model Dispatch. It is a
// separate seam so callers cannot accidentally replace authority with the
// Model Runtime adapter or a provider-facing identity.
func (r *Runtime) NewHarnessAuthority() (*tasksauthority.Adapter, error) {
	if r == nil || r.Dispatcher == nil || r.Dispatcher.Store == nil || r.TaskLeases == nil {
		return nil, errors.New("model runtime authority dependencies are not open")
	}
	principalReader, err := tasksauthority.NewCanonicalPrincipalReader(r.Dispatcher.Store)
	if err != nil {
		return nil, err
	}
	return tasksauthority.New(r.TaskLeases, principalReader)
}
