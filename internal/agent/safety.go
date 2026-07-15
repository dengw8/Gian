package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/dengw8/Gian/internal/runtime"
	"github.com/dengw8/Gian/internal/tools"
)

type ShellApprovalMiddleware struct {
	NoopMiddleware
	Workspace string
}

func (m ShellApprovalMiddleware) Name() string {
	return "shell_approval"
}

func (m ShellApprovalMiddleware) BeforeTool(ctx context.Context, call runtime.ToolCall, state *State) error {
	if call.Name != "shell" {
		return nil
	}
	input, err := tools.DecodeShellInput(call.Arguments)
	if err != nil {
		return err
	}
	analysis, err := tools.AnalyzeShellScript(input.Command)
	if err != nil {
		return err
	}
	if !analysis.RequiresApproval {
		return nil
	}
	id, err := newShellApprovalID()
	if err != nil {
		return err
	}
	hash := sha256.Sum256([]byte(input.Command))
	return approvalRequiredError{approval: runtime.PendingShellApproval{
		ID:          id,
		Version:     1,
		ToolCall:    call,
		Workspace:   m.Workspace,
		RiskReasons: append([]string(nil), analysis.Reasons...),
		ScriptHash:  hex.EncodeToString(hash[:]),
		RequestedAt: time.Now().UTC(),
		Status:      runtime.ShellApprovalPending,
	}}
}

type approvalRequiredError struct {
	approval runtime.PendingShellApproval
}

func (e approvalRequiredError) Error() string {
	return "shell approval required"
}

func newShellApprovalID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate shell approval ID: %w", err)
	}
	return "approval_" + hex.EncodeToString(bytes), nil
}
