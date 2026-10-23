package agentcli

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/danielmiessler/fabric/internal/chat"
	"github.com/danielmiessler/fabric/internal/domain"
	"github.com/danielmiessler/fabric/internal/util/removal"
)

// remover gates every deletion of a path this package created behind a
// confirmation. Tests replace it; production always asks on the terminal.
var remover = removal.Interactive

type request struct {
	Dir    string
	System string
	Prompt string
	Images []string

	createdAttachments []string
	throwawayDir       string
}

func prepareRequest(msgs []*chat.ChatCompletionMessage, opts *domain.ChatOptions) (*request, error) {
	callerDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cannot determine working directory: %w", err)
	}
	req := &request{Dir: callerDir}
	failed := true
	defer func() {
		if failed {
			req.cleanup()
		}
	}()

	var system, prompt []string
	for _, message := range msgs {
		text := messageText(message)
		if message.Role == chat.ChatMessageRoleSystem {
			if text != "" {
				system = append(system, text)
			}
		} else if text != "" {
			prompt = append(prompt, text)
		}
		for _, part := range message.MultiContent {
			if part.Type != chat.ChatMessagePartTypeImageURL || part.ImageURL == nil || !strings.HasPrefix(part.ImageURL.URL, "data:") {
				continue
			}
			path, decodeErr := req.writeDataURL(part.ImageURL.URL)
			if decodeErr != nil {
				return nil, fmt.Errorf("image not sent: %w", decodeErr)
			}
			req.Images = append(req.Images, path)
		}
	}

	if opts.ImageFile != "" {
		path := expandHome(opts.ImageFile)
		if !filepath.IsAbs(path) {
			path = filepath.Join(callerDir, path)
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve image path %q: %w", opts.ImageFile, err)
		}
		req.Images = append(req.Images, filepath.Clean(path))
	}

	if len(prompt) == 0 {
		prompt, system = system, nil
	}
	if len(system) == 0 {
		system = []string{"You are a helpful assistant."}
	}
	for _, path := range req.Images {
		prompt = append(prompt, "Please analyze the file at: "+path)
	}
	req.System = strings.Join(system, "\n\n")
	req.Prompt = strings.Join(prompt, "\n\n")

	if opts.Throwaway {
		req.throwawayDir, err = os.MkdirTemp(tempDirectory(callerDir), "agentcli-*")
		if err != nil {
			return nil, fmt.Errorf("cannot make a throwaway working directory: %w", err)
		}
		req.Dir = req.throwawayDir
	}

	failed = false
	return req, nil
}

// cleanup offers to delete the attachments and throwaway workspace this request
// created. Each removal is confirmed first; a declined or unconfirmable removal
// leaves the path in place. The caller's directory is never a candidate.
func (r *request) cleanup() {
	for _, path := range r.createdAttachments {
		_ = remover.Remove(path)
	}
	if r.throwawayDir != "" {
		_ = remover.RemoveAll(r.throwawayDir)
	}
}

func (r *request) writeDataURL(dataURL string) (string, error) {
	header, encoded, ok := strings.Cut(dataURL, ",")
	if !ok || !strings.HasSuffix(header, ";base64") {
		return "", fmt.Errorf("invalid data URL format")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}

	file, err := os.CreateTemp(tempDirectory(r.Dir), "agentcli-image-*"+imageExtension(header))
	if err != nil {
		return "", err
	}
	path := file.Name()
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err = file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	r.createdAttachments = append(r.createdAttachments, path)
	return path, nil
}

func imageExtension(header string) string {
	switch {
	case strings.Contains(header, "image/png"):
		return ".png"
	case strings.Contains(header, "image/gif"):
		return ".gif"
	case strings.Contains(header, "image/webp"):
		return ".webp"
	default:
		return ".jpg"
	}
}

func messageText(message *chat.ChatCompletionMessage) string {
	parts := []string{message.Content}
	for _, part := range message.MultiContent {
		if part.Type == chat.ChatMessagePartTypeText {
			parts = append(parts, part.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	current, err := user.Current()
	if err != nil {
		return path
	}
	return filepath.Join(current.HomeDir, path[2:])
}

func tempDirectory(cwd string) string {
	dir := os.TempDir()
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	return dir
}
