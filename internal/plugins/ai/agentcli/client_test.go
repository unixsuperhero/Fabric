package agentcli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielmiessler/fabric/internal/chat"
	"github.com/danielmiessler/fabric/internal/domain"
)

const helperMarker = "--agentcli-helper"

func TestClientSendCollectsDecodedEvents(t *testing.T) {
	client := helperClient(t, "success")
	output, err := client.Send(context.Background(), userMessage("hello"), &domain.ChatOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if output != "hello world" {
		t.Errorf("output = %q, want %q", output, "hello world")
	}
}

func TestClientSendStreamEmitsTextAndClosesChannel(t *testing.T) {
	client := helperClient(t, "success")
	updates := make(chan domain.StreamUpdate, 2)
	if err := client.SendStream(context.Background(), userMessage("hello"), &domain.ChatOptions{}, updates); err != nil {
		t.Fatal(err)
	}

	var text strings.Builder
	for update := range updates {
		if update.Type != domain.StreamTypeContent {
			t.Errorf("update type = %q, want content", update.Type)
		}
		text.WriteString(update.Content)
	}
	if text.String() != "hello world" {
		t.Errorf("streamed text = %q, want %q", text.String(), "hello world")
	}
}

func TestClientCancellationStopsChildAndClosesStream(t *testing.T) {
	client := helperClient(t, "sleep")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	updates := make(chan domain.StreamUpdate)

	err := client.SendStream(ctx, userMessage("hello"), &domain.ChatOptions{}, updates)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if _, open := <-updates; open {
		t.Fatal("stream channel remained open after cancellation")
	}
}

func TestClientKillsChildOnOversizedEvent(t *testing.T) {
	client := helperClient(t, "oversized")
	done := make(chan error, 1)
	go func() {
		_, err := client.Send(context.Background(), userMessage("hello"), &domain.ChatOptions{})
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, bufio.ErrTooLong) {
			t.Fatalf("error = %v, want bufio.ErrTooLong", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send did not kill and wait for the child after a scanner error")
	}
}

func TestClientKillsChildOnDecoderFailureAndClosesStream(t *testing.T) {
	client := helperClient(t, "malformed")
	updates := make(chan domain.StreamUpdate)
	done := make(chan error, 1)
	go func() {
		done <- client.SendStream(context.Background(), userMessage("hello"), &domain.ChatOptions{}, updates)
	}()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "decode Claude Code event") {
			t.Fatalf("error = %v, want decoder failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SendStream did not kill and wait for the child after a decoder error")
	}
	if _, open := <-updates; open {
		t.Fatal("stream channel remained open after decoder failure")
	}
}

func TestClientReturnsSubprocessFailure(t *testing.T) {
	client := helperClient(t, "failure")
	_, err := client.Send(context.Background(), userMessage("hello"), &domain.ChatOptions{})
	if err == nil || !strings.Contains(err.Error(), "final harness failure") {
		t.Fatalf("error = %v, want final subprocess failure", err)
	}
}

func TestClaudeCommandEnvironmentUsesCommandDirectoryAndFiltersOnlyAnthropic(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "secret")
	t.Setenv("KEEP_AGENTCLI_TEST", "present")
	client := helperClient(t, "environment")
	client.stripEnvVars = NewClaudeCode().stripEnvVars

	output, err := client.Send(context.Background(), userMessage("hello"), &domain.ChatOptions{Throwaway: true})
	if err != nil {
		t.Fatal(err)
	}
	if output != "pwdMatches=true|anthropic=|keep=present" {
		t.Errorf("child environment = %q", output)
	}
}

func helperClient(t *testing.T, behavior string) *Client {
	t.Helper()
	approveRemovals(t)
	t.Chdir(t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return newClient("AgentCLITest", executable, func(*request, *domain.ChatOptions) []string {
		return []string{"-test.run=^TestAgentCLIHelperProcess$", "--", helperMarker, behavior}
	}, claudeEvent)
}

func userMessage(content string) []*chat.ChatCompletionMessage {
	return []*chat.ChatCompletionMessage{{Role: chat.ChatMessageRoleUser, Content: content}}
}

func TestAgentCLIHelperProcess(t *testing.T) {
	marker := slices.Index(os.Args, helperMarker)
	if marker < 0 || marker+1 >= len(os.Args) {
		return
	}

	encoder := json.NewEncoder(os.Stdout)
	emitText := func(text string) {
		_ = encoder.Encode(map[string]any{
			"type": "stream_event",
			"event": map[string]any{
				"type":  "content_block_delta",
				"delta": map[string]string{"type": "text_delta", "text": text},
			},
		})
	}

	switch os.Args[marker+1] {
	case "success":
		emitText("hello ")
		_ = encoder.Encode(map[string]string{"type": "system", "subtype": "init"})
		emitText("world")
		_ = encoder.Encode(map[string]any{"type": "result", "subtype": "success", "is_error": false, "result": "hello world"})
	case "environment":
		cwd, err := os.Getwd()
		if err != nil {
			panic(err)
		}
		emitText(fmt.Sprintf("pwdMatches=%t|anthropic=%s|keep=%s", os.Getenv("PWD") == cwd, os.Getenv("ANTHROPIC_API_KEY"), os.Getenv("KEEP_AGENTCLI_TEST")))
	case "sleep":
		time.Sleep(time.Minute)
	case "oversized":
		fmt.Fprintln(os.Stdout, strings.Repeat("x", maxEventSize+1))
		time.Sleep(time.Minute)
	case "malformed":
		fmt.Fprintln(os.Stdout, `{"type":`)
		time.Sleep(time.Minute)
	case "failure":
		fmt.Fprintln(os.Stderr, "final harness failure")
		os.Exit(7)
	default:
		fmt.Fprintln(os.Stderr, "unknown helper behavior")
		os.Exit(2)
	}
	os.Exit(0)
}
