package agentcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/danielmiessler/fabric/internal/domain"
	"github.com/danielmiessler/fabric/internal/plugins/ai/anthropic"
)

func NewClaudeCode() *Client {
	client := newClient("ClaudeCode", "claude", claudeArgs, claudeEvent)
	client.listModels = anthropic.NewClient().ListModels
	client.stripEnvVars = func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return strings.HasPrefix(name, "ANTHROPIC_")
	}
	return client
}

func claudeArgs(req *request, opts *domain.ChatOptions) []string {
	args := []string{
		"--print",
		"--no-session-persistence",
		"--setting-sources", "",
		"--strict-mcp-config",
		"--verbose",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--system-prompt", req.System,
	}
	if opts.Model != "" && opts.Model != "default" {
		args = append(args, "--model", opts.Model)
	}
	switch opts.Thinking {
	case domain.ThinkingLow, domain.ThinkingMedium, domain.ThinkingHigh:
		args = append(args, "--effort", string(opts.Thinking))
	}
	if opts.Yolo {
		args = append(args, "--dangerously-skip-permissions")
	}

	seenDirs := make(map[string]bool, len(req.Images))
	for _, image := range req.Images {
		dir := filepath.Dir(image)
		if !seenDirs[dir] {
			args = append(args, "--add-dir", dir)
			seenDirs[dir] = true
		}
	}
	return args
}

type claudeEventEnvelope struct {
	Type    string          `json:"type"`
	Subtype string          `json:"subtype"`
	IsError bool            `json:"is_error"`
	Result  string          `json:"result"`
	Error   json.RawMessage `json:"error"`
	Message json.RawMessage `json:"message"`
	Event   struct {
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
}

func claudeEvent(line []byte) (string, error) {
	var event claudeEventEnvelope
	if err := json.Unmarshal(line, &event); err != nil {
		return "", fmt.Errorf("decode Claude Code event: %w", err)
	}

	switch event.Type {
	case "stream_event":
		if event.Event.Delta.Type == "text_delta" {
			return event.Event.Delta.Text, nil
		}
	case "result":
		if event.IsError || (event.Subtype != "" && event.Subtype != "success") {
			return "", claudeFailure(event, "Claude Code request failed")
		}
	case "error", "failure":
		return "", claudeFailure(event, "Claude Code request failed")
	}
	return "", nil
}

func claudeFailure(event claudeEventEnvelope, fallback string) error {
	for _, raw := range []json.RawMessage{event.Error, event.Message} {
		if message := jsonErrorMessage(raw); message != "" {
			return errors.New(message)
		}
	}
	if event.Result != "" {
		return errors.New(event.Result)
	}
	if event.Subtype != "" {
		return fmt.Errorf("%s: %s", fallback, event.Subtype)
	}
	return errors.New(fallback)
}

func jsonErrorMessage(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var message string
	if json.Unmarshal(raw, &message) == nil {
		return message
	}
	var object struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &object) == nil {
		return object.Message
	}
	return ""
}
