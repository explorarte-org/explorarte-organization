package composition

// The first real keys. Each one is a fact the organization already has and
// already gets wrong occasionally; naming them here does not change any
// behavior yet, it only gives the later reconciler something true to converge
// on. Every one of them is Exclusive, because every one of them has exactly
// one owner today and a second owner would be a bug rather than a merge.
const (
	// KeySchemaTip is the migration the running fleet is compatible with.
	// The launcher that pinned only one service and left the rest on an
	// older binary was a disagreement about this key.
	KeySchemaTip Key = "schema.tip"

	// KeyOrganizationRevision is the canonical registry revision in force.
	KeyOrganizationRevision Key = "canonical.registry.revision"

	// KeyEgressBoundRevision is the revision egress policy is actually
	// bound to. It exists as a key of its own precisely because it can
	// lag KeyOrganizationRevision, which is the seam canonicalsync
	// already refuses to declare success across.
	KeyEgressBoundRevision Key = "canonical.egress.binding"

	// KeyRuntimeBinarySHA is the build the fleet is running -- not the
	// build that was promoted. Keeping them separate is the whole point:
	// a promotion moves a ref, and the fleet running that ref is a
	// different fact that has to be observed rather than assumed.
	KeyRuntimeBinarySHA Key = "runtime.binary.sha"
)

// BaselineKeys declares the four keys above.
func BaselineKeys() []KeySpec {
	return []KeySpec{
		{Name: KeySchemaTip, Composition: Exclusive},
		{Name: KeyOrganizationRevision, Composition: Exclusive},
		{Name: KeyEgressBoundRevision, Composition: Exclusive},
		{Name: KeyRuntimeBinarySHA, Composition: Exclusive},
	}
}

// Baseline is the smallest composition worth describing: the five operating
// components that have actually failed to agree with each other in
// production. It is a description, not a deployment -- nothing consumes it
// yet. Its value today is that the shape has to survive validation, so a
// dependency we get wrong on paper is caught on paper.
func Baseline() (*Graph, error) {
	return NewGraph(BaselineKeys(), []ComponentSpec{
		{
			ID:       "schema-compatibility",
			Provides: []Key{KeySchemaTip},
			Effects: []EffectSpec{
				{Name: "apply-migration", Reversibility: Compensatable},
			},
		},
		{
			ID:       "canonical-registry",
			Requires: []Key{KeySchemaTip},
			Provides: []Key{KeyOrganizationRevision},
			Effects: []EffectSpec{
				{Name: "publish-revision", Reversibility: Compensatable},
			},
		},
		{
			ID:       "egress-binding",
			Requires: []Key{KeySchemaTip, KeyOrganizationRevision},
			Provides: []Key{KeyEgressBoundRevision},
			Effects: []EffectSpec{
				{Name: "bind-revision", Reversibility: Reversible},
			},
		},
		{
			ID:       "runtime-orgd",
			Requires: []Key{KeySchemaTip, KeyOrganizationRevision, KeyEgressBoundRevision},
			Provides: []Key{KeyRuntimeBinarySHA},
			Effects: []EffectSpec{
				// The two that end up in the same component are the
				// reason the type exists. Handing work back is a
				// bookkeeping move; sending the request is money.
				{Name: "lease-task", Reversibility: Reversible},
				{Name: "dispatch-to-provider", Reversibility: Irreversible},
			},
		},
		{
			ID:       "assignment-controller",
			Requires: []Key{KeySchemaTip, KeyOrganizationRevision},
			Effects: []EffectSpec{
				{Name: "assign-dispatch", Reversibility: Reversible},
			},
		},
	})
}
