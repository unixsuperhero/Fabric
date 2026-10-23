package agentcli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/danielmiessler/fabric/internal/chat"
	"github.com/danielmiessler/fabric/internal/domain"
	"github.com/danielmiessler/fabric/internal/util/removal"
)

// approveRemovals makes cleanup delete without a terminal prompt for one test.
func approveRemovals(t *testing.T) {
	t.Helper()
	previous := remover
	remover = removal.Always
	t.Cleanup(func() { remover = previous })
}

func TestPrepareRequestUsesCallerWorkingDirectoryByDefault(t *testing.T) {
	approveRemovals(t)
	t.Chdir(t.TempDir())
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	req, err := prepareRequest([]*chat.ChatCompletionMessage{
		{Role: chat.ChatMessageRoleUser, Content: "hello"},
	}, &domain.ChatOptions{ImageFile: "images/input.png"})
	if err != nil {
		t.Fatal(err)
	}
	defer req.cleanup()

	if req.Dir != workingDir {
		t.Errorf("request directory = %q, want caller directory %q", req.Dir, workingDir)
	}
	wantImage := filepath.Join(workingDir, "images", "input.png")
	if len(req.Images) != 1 || req.Images[0] != wantImage {
		t.Errorf("images = %q, want [%q]", req.Images, wantImage)
	}
	if current, err := os.Getwd(); err != nil || current != workingDir {
		t.Fatalf("process working directory changed to %q: %v", current, err)
	}
}

func TestPrepareRequestOwnsOnlyThrowawayDirectoryAndAttachments(t *testing.T) {
	approveRemovals(t)
	t.Chdir(t.TempDir())
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	image := chat.ChatMessagePart{
		Type:     chat.ChatMessagePartTypeImageURL,
		ImageURL: &chat.ChatMessageImageURL{URL: "data:image/png;base64,aGk="},
	}
	req, err := prepareRequest([]*chat.ChatCompletionMessage{{
		Role:         chat.ChatMessageRoleUser,
		MultiContent: []chat.ChatMessagePart{image},
	}}, &domain.ChatOptions{Throwaway: true})
	if err != nil {
		t.Fatal(err)
	}
	throwawayDir := req.Dir
	attachment := req.Images[0]
	if throwawayDir == workingDir || req.throwawayDir != throwawayDir {
		t.Fatalf("throwaway directory = %q, caller directory = %q", throwawayDir, workingDir)
	}
	if !filepath.IsAbs(attachment) {
		t.Errorf("attachment path = %q, want absolute path", attachment)
	}
	if _, err = os.Stat(throwawayDir); err != nil {
		t.Fatalf("throwaway directory was not created: %v", err)
	}
	if _, err = os.Stat(attachment); err != nil {
		t.Fatalf("attachment was not created: %v", err)
	}

	req.cleanup()
	if !os.IsNotExist(statError(throwawayDir)) {
		t.Errorf("throwaway directory still exists after cleanup")
	}
	if !os.IsNotExist(statError(attachment)) {
		t.Errorf("attachment still exists after cleanup")
	}
	if _, err = os.Stat(workingDir); err != nil {
		t.Fatalf("caller working directory was removed: %v", err)
	}
}

func TestRequestCleanupNeverRemovesUnownedDirectory(t *testing.T) {
	approveRemovals(t)
	callerDir := t.TempDir()
	attachment, err := os.CreateTemp("", "agentcli-owned-attachment-*")
	if err != nil {
		t.Fatal(err)
	}
	attachmentPath := attachment.Name()
	if err = attachment.Close(); err != nil {
		t.Fatal(err)
	}

	req := &request{Dir: callerDir, createdAttachments: []string{attachmentPath}}
	req.cleanup()

	if _, err = os.Stat(callerDir); err != nil {
		t.Fatalf("cleanup removed the caller directory: %v", err)
	}
	if !os.IsNotExist(statError(attachmentPath)) {
		t.Error("cleanup did not remove its temporary attachment")
	}
}

func TestRequestCleanupLeavesEverythingWhenRemovalIsDeclined(t *testing.T) {
	previous := remover
	remover = removal.Never
	t.Cleanup(func() { remover = previous })

	t.Chdir(t.TempDir())
	image := chat.ChatMessagePart{
		Type:     chat.ChatMessagePartTypeImageURL,
		ImageURL: &chat.ChatMessageImageURL{URL: "data:image/png;base64,aGk="},
	}
	req, err := prepareRequest([]*chat.ChatCompletionMessage{{
		Role:         chat.ChatMessageRoleUser,
		MultiContent: []chat.ChatMessagePart{image},
	}}, &domain.ChatOptions{Throwaway: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(req.throwawayDir); _ = os.Remove(req.Images[0]) })

	req.cleanup()
	if _, err = os.Stat(req.throwawayDir); err != nil {
		t.Errorf("declined cleanup removed the throwaway directory: %v", err)
	}
	if _, err = os.Stat(req.Images[0]); err != nil {
		t.Errorf("declined cleanup removed the attachment: %v", err)
	}
}

func TestPrepareRequestRejectsMalformedDataAttachment(t *testing.T) {
	approveRemovals(t)
	message := &chat.ChatCompletionMessage{
		Role: chat.ChatMessageRoleUser,
		MultiContent: []chat.ChatMessagePart{{
			Type:     chat.ChatMessagePartTypeImageURL,
			ImageURL: &chat.ChatMessageImageURL{URL: "data:image/png;base64"},
		}},
	}
	if _, err := prepareRequest([]*chat.ChatCompletionMessage{message}, &domain.ChatOptions{}); err == nil {
		t.Fatal("prepareRequest accepted a malformed data attachment")
	}
}

func statError(path string) error {
	_, err := os.Stat(path)
	return err
}
