package agentcli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"

	"github.com/danielmiessler/fabric/internal/chat"
	"github.com/danielmiessler/fabric/internal/domain"
	"github.com/danielmiessler/fabric/internal/plugins"
)

const maxEventSize = 4 << 20

type argsFunc func(*request, *domain.ChatOptions) []string
type decodeFunc func([]byte) (string, error)

// Client runs a newline-delimited JSON agent CLI as a Fabric AI vendor.
type Client struct {
	*plugins.PluginBase

	binary       string
	args         argsFunc
	decode       decodeFunc
	listModels   func(context.Context) ([]string, error)
	stripEnvVars func(string) bool
}

func newClient(name, binary string, args argsFunc, decode decodeFunc) *Client {
	return &Client{
		PluginBase: plugins.NewVendorPluginBase(name, nil),
		binary:     binary,
		args:       args,
		decode:     decode,
		listModels: func(context.Context) ([]string, error) {
			return []string{defaultModel}, nil
		},
	}
}

func (c *Client) IsConfigured() bool {
	_, err := exec.LookPath(c.binary)
	return err == nil
}

func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	return c.listModels(ctx)
}

func (c *Client) Send(ctx context.Context, msgs []*chat.ChatCompletionMessage, opts *domain.ChatOptions) (string, error) {
	var output strings.Builder
	err := c.run(ctx, msgs, opts, func(text string) error {
		output.WriteString(text)
		return nil
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(output.String()), nil
}

func (c *Client) SendStream(ctx context.Context, msgs []*chat.ChatCompletionMessage, opts *domain.ChatOptions, updates chan domain.StreamUpdate) error {
	defer close(updates)
	return c.run(ctx, msgs, opts, func(text string) error {
		select {
		case updates <- domain.StreamUpdate{Type: domain.StreamTypeContent, Content: text}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}

func (c *Client) run(ctx context.Context, msgs []*chat.ChatCompletionMessage, opts *domain.ChatOptions, emit func(string) error) error {
	if opts == nil {
		opts = &domain.ChatOptions{}
	}
	req, err := prepareRequest(msgs, opts)
	if err != nil {
		return fmt.Errorf("%s: %w", c.GetName(), err)
	}
	defer req.cleanup()

	cmd := exec.CommandContext(ctx, c.binary, c.args(req, opts)...)
	cmd.Dir = req.Dir
	cmd.Stdin = strings.NewReader(req.Prompt)
	cmd.Env = cmd.Environ()
	if c.stripEnvVars != nil {
		cmd.Env = slices.DeleteFunc(cmd.Env, c.stripEnvVars)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("%s: reading command output: %w", c.GetName(), err)
	}
	if err = cmd.Start(); err != nil {
		return commandError(c.GetName(), err, &stderr)
	}

	if err = scanEvents(stdout, c.decode, emit); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%s: %w", c.GetName(), ctxErr)
		}
		return withStderr(fmt.Errorf("%s: reading command output: %w", c.GetName(), err), &stderr)
	}
	if err = cmd.Wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%s: %w", c.GetName(), ctxErr)
		}
		return commandError(c.GetName(), err, &stderr)
	}
	return nil
}

func scanEvents(stdout io.Reader, decode decodeFunc, emit func(string) error) error {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(nil, maxEventSize)
	for scanner.Scan() {
		text, err := decode(scanner.Bytes())
		if err != nil {
			return err
		}
		if text != "" {
			if err = emit(text); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}

func commandError(name string, err error, stderr *bytes.Buffer) error {
	return withStderr(fmt.Errorf("%s: %w", name, err), stderr)
}

func withStderr(err error, stderr *bytes.Buffer) error {
	message := strings.TrimSpace(stderr.String())
	if message == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, message)
}
