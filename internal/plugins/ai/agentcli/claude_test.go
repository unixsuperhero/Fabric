package agentcli

import (
	"strings"
	"testing"
)

func TestClaudeEventEmitsOnlyTextDeltas(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "text delta",
			line: `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}}`,
			want: "hello",
		},
		{
			name: "thinking delta",
			line: `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"private"}}}`,
		},
		{
			name: "assistant aggregate",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`,
		},
		{
			name: "successful result",
			line: `{"type":"result","subtype":"success","is_error":false,"result":"hello"}`,
		},
		{
			name: "system initialization",
			line: `{"type":"system","subtype":"init","session_id":"session"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := claudeEvent([]byte(test.line))
			if err != nil {
				t.Fatalf("claudeEvent() error = %v", err)
			}
			if got != test.want {
				t.Errorf("claudeEvent() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClaudeEventSurfacesHarnessFailures(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantErr string
	}{
		{
			name:    "failed result",
			line:    `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"tool execution failed"}`,
			wantErr: "tool execution failed",
		},
		{
			name:    "explicit error string",
			line:    `{"type":"error","error":"authentication expired"}`,
			wantErr: "authentication expired",
		},
		{
			name:    "explicit failure object",
			line:    `{"type":"failure","error":{"message":"strict MCP startup failed"}}`,
			wantErr: "strict MCP startup failed",
		},
		{
			name:    "failed result fallback",
			line:    `{"type":"result","subtype":"error_max_turns","is_error":true}`,
			wantErr: "error_max_turns",
		},
		{
			name:    "malformed event",
			line:    `{"type":`,
			wantErr: "decode Claude Code event:",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := claudeEvent([]byte(test.line))
			if got != "" {
				t.Errorf("failure emitted text %q", got)
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("claudeEvent() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}
