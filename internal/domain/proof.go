package domain

import (
	"time"
)

// Proof represents an observed fact with its source and observation timestamp.
// AD-2: "Elle rend une preuve : le fait constaté, sa source et l'heure."
type Proof struct {
	Fact       string            `json:"fact"`
	Source     string            `json:"source"`
	ObservedAt time.Time         `json:"observed_at"`
	Details    map[string]string `json:"details,omitempty"`
}

// Attestation represents a human action without trace in reality.
// AD-2: "Attestation : un geste humain sans trace dans le réel. En v1, il n'y en a qu'une, le kit de récupération vérifié."
type Attestation struct {
	Type       string    `json:"type"`
	AttestedAt time.Time `json:"attested_at"`
	AttestedBy string    `json:"attested_by,omitempty"`
	Notes      string    `json:"notes,omitempty"`
}

const (
	AttestationRecoveryKitVerified = "recovery_kit_verified"
)

// Milestone represents a one-way progression threshold.
// AD-2: "Jalon : un passage à sens unique. En v1, il n'y en a qu'un, activated_at."
type Milestone struct {
	Name      string    `json:"name"`
	ReachedAt time.Time `json:"reached_at"`
	Details   string    `json:"details,omitempty"`
}

const (
	MilestoneActivatedAt = "activated_at"
)

// IsKnownMilestone reports whether name is a milestone of v1: only activated_at (AD-2).
func IsKnownMilestone(name string) bool { return name == MilestoneActivatedAt }

// IsKnownAttestation reports whether t is an attestation of v1: only the verified recovery kit (AD-2).
func IsKnownAttestation(t string) bool { return t == AttestationRecoveryKitVerified }
