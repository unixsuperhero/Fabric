package agentcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	debuglog "github.com/danielmiessler/fabric/internal/log"
)

// defaultModel is the alias that leaves model selection to the native CLI.
const defaultModel = "default"

// modelCacheTTL is how long a vendor's model list is reused before the CLI is asked again.
const modelCacheTTL = 24 * time.Hour

// modelCache stores each CLI vendor's model list as <dir>/<vendor>.json so the
// CLI is not spawned on every Fabric run that needs the model catalog.
type modelCache struct {
	dir string
	ttl time.Duration
	now func() time.Time
}

type cachedModels struct {
	FetchedAt time.Time `json:"fetched_at"`
	Models    []string  `json:"models"`
}

func newModelCache() modelCache {
	dir := filepath.Join(os.TempDir(), "fabric-agentcli-models")
	if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, ".config", "fabric", "cache", "models")
	}
	return modelCache{dir: dir, ttl: modelCacheTTL, now: time.Now}
}

// list returns the cached list for vendor while it is fresh. Otherwise it calls
// fetch and stores the result. A failed fetch falls back to a stale cache.
func (c modelCache) list(ctx context.Context, vendor string, fetch func(context.Context) ([]string, error)) ([]string, error) {
	path := filepath.Join(c.dir, vendor+".json")
	cached, cacheErr := c.read(path)
	if cacheErr == nil && c.now().Sub(cached.FetchedAt) < c.ttl {
		return cached.Models, nil
	}

	models, err := fetch(ctx)
	if err != nil {
		if cacheErr == nil {
			debuglog.Debug(debuglog.Basic, "%s model list refresh failed, using cache from %s: %v\n", vendor, cached.FetchedAt.Format(time.RFC3339), err)
			return cached.Models, nil
		}
		return nil, err
	}
	if writeErr := c.write(path, models); writeErr != nil {
		debuglog.Debug(debuglog.Basic, "%s model list not cached: %v\n", vendor, writeErr)
	}
	return models, nil
}

func (c modelCache) read(path string) (cachedModels, error) {
	var cached cachedModels
	data, err := os.ReadFile(path)
	if err != nil {
		return cached, err
	}
	if err = json.Unmarshal(data, &cached); err != nil {
		return cached, err
	}
	return cached, nil
}

func (c modelCache) write(path string, models []string) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cachedModels{FetchedAt: c.now(), Models: models}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// withDefault puts the "default" alias first so it is always selectable.
func withDefault(models []string) []string {
	out := []string{defaultModel}
	for _, model := range models {
		if model != "" && model != defaultModel {
			out = append(out, model)
		}
	}
	return out
}

// ompModels runs `omp models --json` and returns the selectors that `--model` accepts.
func ompModels(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "omp", "models", "--json")
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("omp models: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("omp models: %w", err)
	}
	return parseOMPModels(output)
}

func parseOMPModels(data []byte) ([]string, error) {
	var catalog struct {
		Models []struct {
			Selector string `json:"selector"`
			Kind     string `json:"kind"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("decode omp models: %w", err)
	}
	seen := map[string]bool{}
	var models []string
	for _, model := range catalog.Models {
		if model.Selector == "" || (model.Kind != "" && model.Kind != "chat") || seen[model.Selector] {
			continue
		}
		seen[model.Selector] = true
		models = append(models, model.Selector)
	}
	return models, nil
}

// codexHome is the Codex CLI's state directory, honoring CODEX_HOME like the CLI does.
func codexHome() (string, error) {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

// codexCachedModels reads the model catalog the Codex CLI keeps for itself in
// $CODEX_HOME/models_cache.json, so no network call or Fabric-side login is needed.
func codexCachedModels() ([]string, error) {
	home, err := codexHome()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(home, "models_cache.json"))
	if err != nil {
		return nil, err
	}
	return parseCodexModelsCache(data)
}

func parseCodexModelsCache(data []byte) ([]string, error) {
	var cache struct {
		Models []struct {
			Slug           string `json:"slug"`
			Visibility     string `json:"visibility"`
			SupportedInAPI bool   `json:"supported_in_api"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("decode codex models cache: %w", err)
	}
	var models []string
	for _, model := range cache.Models {
		if model.Slug != "" && model.SupportedInAPI && model.Visibility == "list" {
			models = append(models, model.Slug)
		}
	}
	return models, nil
}

// codexModelLister prefers the Codex CLI's own cache file and otherwise asks
// fallback (the Codex HTTP vendor, when Fabric has it configured) through the
// model cache. With neither available only the "default" alias is listed.
func codexModelLister(cache modelCache, fallback func(context.Context) ([]string, error)) func(context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		models, err := codexCachedModels()
		if err == nil {
			return withDefault(models), nil
		}
		debuglog.Debug(debuglog.Detailed, "Codex CLI models cache unavailable: %v\n", err)
		if fallback == nil {
			return withDefault(nil), nil
		}
		models, err = cache.list(ctx, "CodexCLI", fallback)
		if err != nil {
			debuglog.Debug(debuglog.Basic, "CodexCLI model list unavailable: %v\n", err)
			return withDefault(nil), nil
		}
		return withDefault(models), nil
	}
}

// ompModelLister lists OMP's chat models through the model cache.
func ompModelLister(cache modelCache) func(context.Context) ([]string, error) {
	return func(ctx context.Context) ([]string, error) {
		models, err := cache.list(ctx, "OMP", ompModels)
		if err != nil {
			debuglog.Debug(debuglog.Basic, "OMP model list unavailable: %v\n", err)
			return withDefault(nil), nil
		}
		return withDefault(models), nil
	}
}
