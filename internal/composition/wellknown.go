package composition

// The first real keys. Each one is a fact the organization already has and
// already gets wrong occasionally; naming them here does not change any
// behavior yet, it only gives the later reconciler something true to converge
// on. Every one is Exclusive, because every one has exactly one owner today
// and a second owner would be a bug rather than a merge.
const (
	// KeyDatabaseSchemaTip is the migration the database is actually at.
	// It is a fact about the database, not about any binary -- which is
	// why it is not called schema.tip. A single key by that name would
	// have to mean both "where the database is" and "what the runtime can
	// speak", and those come apart exactly when it matters.
	KeyDatabaseSchemaTip Key = "database.schema.tip"

	// KeyRuntimeSchemaCompatibility is the set of migrations the running
	// binary accepts, as a comma-separated list. Keeping it separate from
	// KeyDatabaseSchemaTip is what makes rolling replacement expressible
	// instead of a coincidence: two binaries with overlapping accepted
	// sets can both be admitted at once.
	KeyRuntimeSchemaCompatibility Key = "runtime.schema.compatibility"

	// KeyOrganizationRevision is the canonical registry revision in force.
	KeyOrganizationRevision Key = "canonical.registry.revision"

	// KeyEgressBoundRevision is the revision egress policy is actually
	// bound to. It exists as a key of its own precisely because it can lag
	// KeyOrganizationRevision, and the admission predicate below is what
	// says the lag is not acceptable.
	KeyEgressBoundRevision Key = "canonical.egress.binding"

	// KeyRuntimeDesiredSHA is the build the organization has decided it
	// should be running. A promotion states it.
	KeyRuntimeDesiredSHA Key = "runtime.binary.desired_sha"

	// KeyRuntimeObservedSHA is the build the fleet reports it is actually
	// running. The fleet states it.
	//
	// Two keys, not one, because they have two owners and one fact has one
	// owner. A promotion moves a ref; the fleet running that ref is a
	// different fact that has to be observed rather than assumed. Collapsed
	// into a single key, "what we want" and "what there is" become
	// indistinguishable, and there is nothing left for a reconciler to act
	// on.
	KeyRuntimeObservedSHA Key = "runtime.binary.observed_sha"
)

// BaselineKeys declares the six keys above.
func BaselineKeys() []KeySpec {
	return []KeySpec{
		{Name: KeyDatabaseSchemaTip, Composition: Exclusive},
		{Name: KeyRuntimeSchemaCompatibility, Composition: Exclusive},
		{Name: KeyOrganizationRevision, Composition: Exclusive},
		{Name: KeyEgressBoundRevision, Composition: Exclusive},
		{Name: KeyRuntimeDesiredSHA, Composition: Exclusive},
		{Name: KeyRuntimeObservedSHA, Composition: Exclusive},
	}
}

// CanonicalRevisionBound is the invariant canonicalsync enforces imperatively
// today: the organization is not executable while the revision in force is
// not the one egress policy is bound to.
//
// Stating it as a predicate rather than a dependency is the difference
// between "the binding exists" and "the binding is the right one". Topology
// could only ever say the first.
func CanonicalRevisionBound() Predicate {
	return Equal(KeyEgressBoundRevision, KeyOrganizationRevision)
}

// SchemaAccepted is the other half of executability: the binary has to accept
// the migration the database is at.
func SchemaAccepted() Predicate {
	return MemberOf(KeyDatabaseSchemaTip, KeyRuntimeSchemaCompatibility)
}

// Baseline is the smallest composition worth describing: the components that
// have actually failed to agree with each other in production. It is a
// description, not a deployment -- nothing consumes it yet. Its value today
// is that the shape and its invariants have to survive validation, so what we
// get wrong on paper is caught on paper.
func Baseline() (*Graph, error) {
	return NewGraph(
		BaselineKeys(),
		[]ComponentSpec{
			{
				ID:       "database-schema",
				Provides: []Key{KeyDatabaseSchemaTip},
				Effects: []EffectSpec{
					{Name: "apply-migration", Reversibility: Compensatable},
				},
			},
			{
				ID:       "canonical-registry",
				Requires: []Key{KeyDatabaseSchemaTip},
				Provides: []Key{KeyOrganizationRevision},
				Effects: []EffectSpec{
					{Name: "publish-revision", Reversibility: Compensatable},
				},
			},
			{
				ID:       "egress-binding",
				Requires: []Key{KeyDatabaseSchemaTip, KeyOrganizationRevision},
				Provides: []Key{KeyEgressBoundRevision},
				Effects: []EffectSpec{
					{Name: "bind-revision", Reversibility: Reversible},
				},
			},
			{
				ID:       "release-promotion",
				Provides: []Key{KeyRuntimeDesiredSHA},
				Effects: []EffectSpec{
					// A promoted ref answers to a revert commit. The
					// original promotion stays in the history, which
					// is where it belongs.
					{Name: "promote-ref", Reversibility: Compensatable},
				},
			},
			{
				ID:       "runtime-orgd",
				Requires: []Key{KeyDatabaseSchemaTip, KeyOrganizationRevision, KeyEgressBoundRevision},
				Provides: []Key{KeyRuntimeSchemaCompatibility, KeyRuntimeObservedSHA},
				Admits:   []Predicate{CanonicalRevisionBound(), SchemaAccepted()},
				Effects: []EffectSpec{
					// The two in one component are the reason the
					// type exists. Handing work back is bookkeeping.
					// Sending the request is money.
					{Name: "lease-task", Reversibility: Reversible},
					{Name: "dispatch-to-provider", Reversibility: Irreversible},
				},
			},
			{
				ID:       "assignment-controller",
				Requires: []Key{KeyDatabaseSchemaTip, KeyOrganizationRevision, KeyEgressBoundRevision},
				Admits:   []Predicate{CanonicalRevisionBound()},
				Effects: []EffectSpec{
					{Name: "assign-dispatch", Reversibility: Reversible},
				},
			},
			{
				// The controller that performs replacements cannot be
				// inside the thing it replaces. It is declared here as
				// its own component, depending only on the desired and
				// observed build, so that the topology says out loud
				// that it survives runtime-orgd leaving.
				ID:       "composition-controller",
				Requires: []Key{KeyRuntimeDesiredSHA, KeyRuntimeObservedSHA},
				Effects: []EffectSpec{
					{Name: "replace-runtime", Reversibility: Compensatable},
				},
			},
		},
		ConvergenceSpec{Desired: KeyRuntimeDesiredSHA, Observed: KeyRuntimeObservedSHA},
	)
}
