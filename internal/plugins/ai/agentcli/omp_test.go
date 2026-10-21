package agentcli

import (
	"strings"
	"testing"
)

func TestOMPEventEmitsOnlyTextDeltas(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "text delta",
			line: `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"hello "}}`,
			want: "hello ",
		},
		{
			name: "thinking delta",
			line: `{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","contentIndex":0,"delta":"private reasoning"}}`,
		},
		{
			name: "tool call delta",
			line: `{"type":"message_update","assistantMessageEvent":{"type":"toolcall_delta","contentIndex":1,"delta":"{\"path\":"}}`,
		},
		{
			name: "tool execution",
			line: `{"type":"tool_execution_end","toolCallId":"call-1","toolName":"read","isError":false}`,
		},
		{
			name: "successful final message does not duplicate deltas",
			line: `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hello "}],"stopReason":"stop"}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ompEvent([]byte(test.line))
			if err != nil {
				t.Fatalf("ompEvent() error = %v", err)
			}
			if got != test.want {
				t.Errorf("ompEvent() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestOMPEventSurfacesTerminalFailures(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantErr string
	}{
		{
			name:    "provider error",
			line:    `{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"provider unavailable"}}`,
			wantErr: "provider unavailable",
		},
		{
			name:    "abort without detail",
			line:    `{"type":"message_end","message":{"role":"assistant","content":[],"stopReason":"aborted"}}`,
			wantErr: "OMP request aborted",
		},
		{
			name:    "terminal agent event fallback",
			line:    `{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"final failure"}]}`,
			wantErr: "final failure",
		},
		{
			name:    "harness notice",
			line:    `{"type":"notice","level":"error","message":"strict MCP startup failed"}`,
			wantErr: "strict MCP startup failed",
		},
		{
			name:    "explicit failure event",
			line:    `{"type":"failure","error":"session initialization failed"}`,
			wantErr: "session initialization failed",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ompEvent([]byte(test.line))
			if got != "" {
				t.Errorf("failure emitted text %q", got)
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ompEvent() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestOMPEventRejectsMalformedJSON(t *testing.T) {
	if _, err := ompEvent([]byte(`{"type":`)); err == nil {
		t.Fatal("ompEvent() accepted malformed JSON")
	}
}
