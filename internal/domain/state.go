package domain

// ServerState represents the canonical server state.
// AD-2, consistency conventions: none, demo, activating, active, migrating, orphaned_resources, cleanup_incomplete
type ServerState string

const (
	StateNone              ServerState = "none"
	StateDemo              ServerState = "demo"
	StateActivating        ServerState = "activating"
	StateActive            ServerState = "active"
	StateMigrating         ServerState = "migrating"
	StateOrphanedResources ServerState = "orphaned_resources"
	StateCleanupIncomplete ServerState = "cleanup_incomplete"
)

// DisplayLabel returns the human-readable label according to EXPERIENCE.md / DESIGN.md.
func (s ServerState) DisplayLabel() string {
	switch s {
	case StateNone:
		return "Aucun"
	case StateDemo:
		return "Démo"
	case StateActivating:
		return "Activation"
	case StateActive:
		return "Actif"
	case StateMigrating:
		return "Migration"
	case StateOrphanedResources:
		return "Ressources orphelines"
	case StateCleanupIncomplete:
		return "Nettoyage incomplet"
	default:
		return string(s)
	}
}

// ServerFacts aggregates all known facts, proofs, attestations, and milestones.
type ServerFacts struct {
	ServerReachable   bool          `json:"server_reachable"`
	HasServer         bool          `json:"has_server"`
	OrphanedResources bool          `json:"orphaned_resources"`
	CleanupFailed     bool          `json:"cleanup_failed"`
	Migrating         bool          `json:"migrating"`
	ActivationStarted bool          `json:"activation_started"`
	Proofs            []Proof       `json:"proofs"`
	Attestations      []Attestation `json:"attestations"`
	Milestones        []Milestone   `json:"milestones"`
}

// HasMilestone checks whether a named milestone is reached.
func (f ServerFacts) HasMilestone(name string) bool {
	for _, m := range f.Milestones {
		if m.Name == name {
			return true
		}
	}
	return false
}

// HasAttestation checks whether a named attestation exists.
func (f ServerFacts) HasAttestation(attestationType string) bool {
	for _, a := range f.Attestations {
		if a.Type == attestationType {
			return true
		}
	}
	return false
}

// DeriveServerState is the pure domain function that derives server state (AD-2).
// Prevents a recovery reading a flag instead of reality, or an active server falling back to activation.
// Precedence follows the state diagram of the architecture: activated_at is one-way and outranks
// everything; before it, a failed cleanup outranks the rest.
func DeriveServerState(facts ServerFacts) ServerState {
	serverExists := facts.HasServer || facts.ServerReachable
	kitVerified := facts.HasAttestation(AttestationRecoveryKitVerified)

	// After activated_at, the only way out of Active is Migration (AD-2, state diagram).
	// A failed cleanup is a health warning, not a step back, and a missing server never
	// turns into none or orphaned_resources: "Tout éteindre" no longer exists once activated.
	if facts.HasMilestone(MilestoneActivatedAt) {
		if facts.Migrating {
			return StateMigrating
		}
		return StateActive
	}

	// Before activation: a cleanup that could not be proven (Demo or Activation -> NettoyageIncomplet)
	if facts.CleanupFailed {
		return StateCleanupIncomplete
	}

	// No server: tagged resources left behind, or nothing at all
	if !serverExists {
		if facts.OrphanedResources {
			return StateOrphanedResources
		}
		return StateNone
	}

	// Activation -> Migration requires the attested recovery kit; there is no Demo -> Migration edge.
	if facts.Migrating && kitVerified {
		return StateMigrating
	}

	if facts.ActivationStarted || kitVerified {
		return StateActivating
	}

	// Default state when server exists but is not in activation or active
	return StateDemo
}
