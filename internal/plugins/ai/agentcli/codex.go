package agentcli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/danielmiessler/fabric/internal/domain"
)

func NewCodexCLI() *Client {
	return newClient("CodexCLI", "codex", codexArgs, codexEvent)
}

func codexArgs(req *request, opts *domain.ChatOptions) []string {
	args := []string{"exec", "--json", "--ephemeral", "--skip-git-repo-check", "--cd", req.Dir}
	if req.System != "" {
		system, _ := json.Marshal(req.System)
		args = append(args, "--config", "developer_instructions="+string(system))
	}
	if opts.Model != "" && opts.Model != "default" {
		args = append(args, "--model", opts.Model)
	}
	if thinking := codexThinking(opts.Thinking); thinking != "" {
		args = append(args, "--config", "model_reasoning_effort="+thinking)
	}
	if opts.Yolo {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	for _, image := range req.Images {
		args = append(args, "--image", image)
	}
	return args
}

func codexThinking(thinking domain.ThinkingLevel) string {
	switch thinking {
	case domain.ThinkingLow, domain.ThinkingMedium, domain.ThinkingHigh:
		return string(thinking)
	default:
		return ""
	}
}

type codexEventEnvelope struct {
	Type    string          `json:"type"`
	Message string          `json:"message"`
	Error   codexEventError `json:"error"`
	Item    codexEventItem  `json:"item"`
}

type codexEventError struct {
	Message string `json:"message"`
}

type codexEventItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func codexEvent(line []byte) (string, error) {
	var event codexEventEnvelope
	if err := json.Unmarshal(line, &event); err != nil {
		return "", fmt.Errorf("decode Codex CLI event: %w", err)
	}

	switch event.Type {
	case "item.completed":
		if event.Item.Type == "agent_message" {
			return event.Item.Text, nil
		}
	case "turn.failed":
		return "", codexFailure(event.Error.Message, "Codex CLI turn failed")
	case "error":
		return "", codexFailure(event.Message, "Codex CLI request failed")
	}
	return "", nil
}

func codexFailure(message, fallback string) error {
	if message != "" {
		return errors.New(message)
	}
	return errors.New(fallback)
}
