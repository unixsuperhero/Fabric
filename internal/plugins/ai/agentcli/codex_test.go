package agentcli

import (
	"strings"
	"testing"
)

func TestCodexEvent(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    string
		wantErr string
	}{
		{
			name: "completed agent message",
			line: `{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"finished"}}`,
			want: "finished",
		},
		{
			name: "thread metadata",
			line: `{"type":"thread.started","thread_id":"thread_1"}`,
		},
		{
			name: "in progress message",
			line: `{"type":"item.started","item":{"id":"item_1","type":"agent_message","text":"draft"}}`,
		},
		{
			name: "reasoning item",
			line: `{"type":"item.completed","item":{"id":"item_2","type":"reasoning","text":"private reasoning"}}`,
		},
		{
			name: "nonfatal item error",
			line: `{"type":"item.completed","item":{"id":"item_3","type":"error","message":"stream lagged"}}`,
		},
		{
			name:    "failed turn",
			line:    `{"type":"turn.failed","error":{"message":"quota exhausted"}}`,
			wantErr: "quota exhausted",
		},
		{
			name:    "fatal stream error",
			line:    `{"type":"error","message":"connection lost"}`,
			wantErr: "connection lost",
		},
		{
			name:    "malformed event",
			line:    `{"type":`,
			wantErr: "decode Codex CLI event:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := codexEvent([]byte(tt.line))
			if got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
