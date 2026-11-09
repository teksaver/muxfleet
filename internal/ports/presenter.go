package ports

import (
	"context"
)

// StatusType designates the semantic state of a status line.
type StatusType string

const (
	StatusInProgress StatusType = "in_progress" // ›
	StatusSuccess    StatusType = "success"     // ✓
	StatusWarning    StatusType = "warning"     // !
	StatusInfo       StatusType = "info"        // ·
)

// QuestionChoice defines a numbered choice question without arrows/mouse (EXPERIENCE.md).
type QuestionChoice struct {
	Title        string
	Options      []string
	DefaultIndex int // 1-based, 0 if no default
	Help         string
}

// QuestionInput defines a single input question.
type QuestionInput struct {
	Prompt       string
	DefaultValue string
	Help         string
}

// Presenter defines user interaction and output formatting (AD-19, EXPERIENCE.md).
// Draft port (see package doc): adjust it to the ADs that bind it before implementing it.
type Presenter interface {
	ShowStatus(statusType StatusType, message string, detail string)
	ShowMessage(message string)
	ShowError(whatHappened string, whatWasDone string, nextStep string)
	AskChoice(ctx context.Context, q QuestionChoice) (int, error)
	AskInput(ctx context.Context, q QuestionInput) (string, error)
	AskSecret(ctx context.Context, prompt string, help string) (string, error)
	AskConfirmation(ctx context.Context, prompt string, expectedTarget string) (bool, error)
	IsNoInput() bool
	IsJSON() bool
	OutputJSON(data any) error
}
