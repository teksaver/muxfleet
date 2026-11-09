package domain_test

import (
	"testing"
	"time"

	"muxfleet/internal/domain"
)

func TestDeriveServerState(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name          string
		facts         domain.ServerFacts
		expectedState domain.ServerState
		expectedLabel string
	}{
		{
			name:          "0 preuve, 0 attestation, 0 jalon => Aucun",
			facts:         domain.ServerFacts{},
			expectedState: domain.StateNone,
			expectedLabel: "Aucun",
		},
		{
			// Proofs is not read yet: the derivation uses the ServerReachable flag (F8 c).
			name: "Serveur joignable (drapeau), jalon activated_at absent => Démo",
			facts: domain.ServerFacts{
				ServerReachable: true,
			},
			expectedState: domain.StateDemo,
			expectedLabel: "Démo",
		},
		{
			name: "Serveur joignable (drapeau) + jalon activated_at présent => Actif",
			facts: domain.ServerFacts{
				ServerReachable: true,
				Milestones: []domain.Milestone{
					{
						Name:      domain.MilestoneActivatedAt,
						ReachedAt: now,
					},
				},
			},
			expectedState: domain.StateActive,
			expectedLabel: "Actif",
		},
		{
			name: "Activation commencée (attestation kit vérifié) sans jalon activated_at => Activation",
			facts: domain.ServerFacts{
				ServerReachable: true,
				Attestations: []domain.Attestation{
					{
						Type:       domain.AttestationRecoveryKitVerified,
						AttestedAt: now,
					},
				},
			},
			expectedState: domain.StateActivating,
			expectedLabel: "Activation",
		},
		{
			name: "Drapeau activation_started sans jalon activated_at => Activation",
			facts: domain.ServerFacts{
				HasServer:         true,
				ActivationStarted: true,
			},
			expectedState: domain.StateActivating,
			expectedLabel: "Activation",
		},
		{
			name: "Serveur actif en cours de migration => Migration",
			facts: domain.ServerFacts{
				ServerReachable: true,
				Migrating:       true,
				Milestones: []domain.Milestone{
					{
						Name:      domain.MilestoneActivatedAt,
						ReachedAt: now,
					},
				},
			},
			expectedState: domain.StateMigrating,
			expectedLabel: "Migration",
		},
		{
			name: "Échec de nettoyage avant activation => NettoyageIncomplet",
			facts: domain.ServerFacts{
				ServerReachable: true,
				CleanupFailed:   true,
			},
			expectedState: domain.StateCleanupIncomplete,
			expectedLabel: "Nettoyage incomplet",
		},
		{
			name: "Échec de nettoyage sans serveur avant activation => NettoyageIncomplet",
			facts: domain.ServerFacts{
				CleanupFailed:     true,
				OrphanedResources: true,
			},
			expectedState: domain.StateCleanupIncomplete,
			expectedLabel: "Nettoyage incomplet",
		},
		{
			name: "Échec de nettoyage après activated_at => reste Actif (pas d'arête Active -> NettoyageIncomplet)",
			facts: domain.ServerFacts{
				ServerReachable: true,
				CleanupFailed:   true,
				Milestones:      []domain.Milestone{{Name: domain.MilestoneActivatedAt, ReachedAt: now}},
			},
			expectedState: domain.StateActive,
			expectedLabel: "Actif",
		},
		{
			// Domain guard only: prj reads the milestone on the server itself, so its fact loader never
			// produces this case. It protects callers that gather facts elsewhere (vps, later stories).
			name: "Garde du domaine : jalon activated_at sans serveur => reste Actif, jamais Aucun ni Orphelines",
			facts: domain.ServerFacts{
				OrphanedResources: true,
				Milestones:        []domain.Milestone{{Name: domain.MilestoneActivatedAt, ReachedAt: now}},
			},
			expectedState: domain.StateActive,
			expectedLabel: "Actif",
		},
		{
			name: "Migration demandée en démo, sans kit attesté => reste Démo (pas d'arête Demo -> Migration)",
			facts: domain.ServerFacts{
				HasServer: true,
				Migrating: true,
			},
			expectedState: domain.StateDemo,
			expectedLabel: "Démo",
		},
		{
			name: "Migration pendant l'activation, kit attesté => Migration",
			facts: domain.ServerFacts{
				HasServer:    true,
				Migrating:    true,
				Attestations: []domain.Attestation{{Type: domain.AttestationRecoveryKitVerified, AttestedAt: now}},
			},
			expectedState: domain.StateMigrating,
			expectedLabel: "Migration",
		},
		{
			name: "Activation commencée puis migration demandée, sans kit attesté => reste Activation",
			facts: domain.ServerFacts{
				HasServer:         true,
				ActivationStarted: true,
				Migrating:         true,
			},
			expectedState: domain.StateActivating,
			expectedLabel: "Activation",
		},
		{
			name: "Kit attesté mais plus de serveur => Aucun (arête Activation -> Aucun, Tout éteindre)",
			facts: domain.ServerFacts{
				Attestations: []domain.Attestation{{Type: domain.AttestationRecoveryKitVerified, AttestedAt: now}},
			},
			expectedState: domain.StateNone,
			expectedLabel: "Aucun",
		},
		{
			name: "Échec de nettoyage seul, sans serveur ni ressource connue => NettoyageIncomplet",
			facts: domain.ServerFacts{
				CleanupFailed: true,
			},
			expectedState: domain.StateCleanupIncomplete,
			expectedLabel: "Nettoyage incomplet",
		},
		{
			name: "Jalon + migration + échec de nettoyage => Migration (le nettoyage ne fait pas sortir d'Active)",
			facts: domain.ServerFacts{
				HasServer:     true,
				Migrating:     true,
				CleanupFailed: true,
				Milestones:    []domain.Milestone{{Name: domain.MilestoneActivatedAt, ReachedAt: now}},
			},
			expectedState: domain.StateMigrating,
			expectedLabel: "Migration",
		},
		{
			// Known gap, F10 open: there is no "unverified" outcome yet. An existing server that does not
			// answer, whose milestone cannot be read, is derived as a demo. Change this case when F10 lands.
			name: "LACUNE F10 : serveur existant mais injoignable, jalon illisible => Démo",
			facts: domain.ServerFacts{
				HasServer:       true,
				ServerReachable: false,
			},
			expectedState: domain.StateDemo,
			expectedLabel: "Démo",
		},
		{
			name: "Aucun serveur mais ressources étiquetées orphelines => RessourcesOrphelines",
			facts: domain.ServerFacts{
				HasServer:         false,
				ServerReachable:   false,
				OrphanedResources: true,
			},
			expectedState: domain.StateOrphanedResources,
			expectedLabel: "Ressources orphelines",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := domain.DeriveServerState(tc.facts)
			if got != tc.expectedState {
				t.Fatalf("DeriveServerState() = %v, want %v", got, tc.expectedState)
			}
			if got.DisplayLabel() != tc.expectedLabel {
				t.Fatalf("DisplayLabel() = %v, want %v", got.DisplayLabel(), tc.expectedLabel)
			}
		})
	}
}

// TestDeriveServerStateInvariants walks every combination of facts and checks the rules
// of AD-2 and of the architecture's state diagram that must hold whatever the combination.
func TestDeriveServerStateInvariants(t *testing.T) {
	now := time.Now().UTC()
	const nFacts = 8

	for mask := 0; mask < 1<<nFacts; mask++ {
		bit := func(i int) bool { return mask&(1<<i) != 0 }
		facts := domain.ServerFacts{
			HasServer:         bit(0),
			ServerReachable:   bit(1),
			OrphanedResources: bit(2),
			CleanupFailed:     bit(3),
			Migrating:         bit(4),
			ActivationStarted: bit(5),
		}
		kit := bit(6)
		milestone := bit(7)
		if kit {
			facts.Attestations = []domain.Attestation{{Type: domain.AttestationRecoveryKitVerified, AttestedAt: now}}
		}
		if milestone {
			facts.Milestones = []domain.Milestone{{Name: domain.MilestoneActivatedAt, ReachedAt: now}}
		}
		serverExists := facts.HasServer || facts.ServerReachable

		got := domain.DeriveServerState(facts)
		fail := func(rule string) {
			t.Errorf("%s: facts=%+v kit=%v milestone=%v => %s", rule, facts, kit, milestone, got)
		}

		if milestone {
			// One-way milestone: Active only leaves towards Migration.
			want := domain.StateActive
			if facts.Migrating {
				want = domain.StateMigrating
			}
			if got != want {
				fail("après activated_at, seul Active ou Migration")
			}
			continue
		}

		if got == domain.StateActive {
			fail("Actif exige le jalon activated_at")
		}
		if got == domain.StateMigrating && !(kit && serverExists) {
			fail("Migration avant activation exige le kit attesté et un serveur")
		}
		if facts.CleanupFailed && got != domain.StateCleanupIncomplete {
			fail("avant activation, un nettoyage non prouvé donne NettoyageIncomplet")
		}
		if !facts.CleanupFailed && !serverExists && got != domain.StateNone && got != domain.StateOrphanedResources {
			fail("sans serveur, seul Aucun ou RessourcesOrphelines")
		}
		if !facts.CleanupFailed && serverExists && (got == domain.StateNone || got == domain.StateOrphanedResources) {
			fail("un serveur existant n'est jamais Aucun ni RessourcesOrphelines")
		}
	}
}
