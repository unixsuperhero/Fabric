package agentcli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/danielmiessler/fabric/internal/domain"
)

func NewOMP() *Client {
	client := newClient("OMP", "omp", ompArgs, ompEvent)
	client.listModels = ompModelLister(newModelCache())
	return client
}

func ompArgs(req *request, opts *domain.ChatOptions) []string {
	args := []string{"--print", "--mode", "json", "--no-session", "--cwd", req.Dir}
	if req.System != "" {
		args = append(args, "--system-prompt", req.System)
	}
	if opts.Model != "" && opts.Model != "default" {
		args = append(args, "--model", opts.Model)
	}
	if thinking := ompThinking(opts.Thinking); thinking != "" {
		args = append(args, "--thinking", thinking)
	}
	if opts.Yolo {
		args = append(args, "--auto-approve")
	}
	for _, image := range req.Images {
		args = append(args, "@"+image)
	}
	return args
}

func ompThinking(thinking domain.ThinkingLevel) string {
	switch thinking {
	case "off", "minimal", "low", "medium", "high", "xhigh", "max", "auto":
		return string(thinking)
	default:
		return ""
	}
}

type ompMessage struct {
	Role         string `json:"role"`
	StopReason   string `json:"stopReason"`
	ErrorMessage string `json:"errorMessage"`
}

type ompEventEnvelope struct {
	Type                  string          `json:"type"`
	Level                 string          `json:"level"`
	Message               json.RawMessage `json:"message"`
	Error                 string          `json:"error"`
	Messages              []ompMessage    `json:"messages"`
	AssistantMessageEvent struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
	} `json:"assistantMessageEvent"`
}

func ompEvent(line []byte) (string, error) {
	var event ompEventEnvelope
	if err := json.Unmarshal(line, &event); err != nil {
		return "", fmt.Errorf("decode OMP event: %w", err)
	}

	switch event.Type {
	case "message_update":
		if event.AssistantMessageEvent.Type == "text_delta" {
			return event.AssistantMessageEvent.Delta, nil
		}
	case "message_end", "turn_end":
		var message ompMessage
		if err := json.Unmarshal(event.Message, &message); err != nil {
			return "", fmt.Errorf("decode OMP %s message: %w", event.Type, err)
		}
		return "", ompTerminalError(message)
	case "agent_end":
		for i := len(event.Messages) - 1; i >= 0; i-- {
			if event.Messages[i].Role == "assistant" {
				return "", ompTerminalError(event.Messages[i])
			}
		}
	case "notice":
		if event.Level == "error" {
			return "", ompRawError(event.Message, "OMP error")
		}
	case "error", "failure":
		if event.Error != "" {
			return "", errors.New(event.Error)
		}
		return "", ompRawError(event.Message, "OMP request failed")
	}
	return "", nil
}

func ompTerminalError(message ompMessage) error {
	if message.StopReason != "error" && message.StopReason != "aborted" {
		return nil
	}
	if message.ErrorMessage != "" {
		return errors.New(message.ErrorMessage)
	}
	return fmt.Errorf("OMP request %s", message.StopReason)
}

func ompRawError(raw json.RawMessage, fallback string) error {
	var message string
	if len(raw) != 0 && json.Unmarshal(raw, &message) == nil && message != "" {
		return errors.New(message)
	}
	return errors.New(fallback)
}
