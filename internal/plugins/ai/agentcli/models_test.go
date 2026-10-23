package agentcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func testCache(t *testing.T, now time.Time) *modelCache {
	t.Helper()
	cache := &modelCache{dir: filepath.Join(t.TempDir(), "models"), ttl: time.Hour}
	cache.now = func() time.Time { return now }
	return cache
}

func counting(models []string, err error) (func(context.Context) ([]string, error), *int) {
	calls := 0
	return func(context.Context) ([]string, error) {
		calls++
		return models, err
	}, &calls
}

func TestModelCacheFetchesOnceWhileFresh(t *testing.T) {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	cache := testCache(t, start)
	fetch, calls := counting([]string{"b", "a"}, nil)

	for i := 0; i < 3; i++ {
		models, err := cache.list(context.Background(), "Vendor", fetch)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(models, []string{"b", "a"}) {
			t.Fatalf("models = %q", models)
		}
	}
	if *calls != 1 {
		t.Errorf("fetch called %d times, want 1", *calls)
	}
	if _, err := os.Stat(filepath.Join(cache.dir, "Vendor.json")); err != nil {
		t.Errorf("cache file not written: %v", err)
	}
}

func TestModelCacheRefreshesAfterTTL(t *testing.T) {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	cache := testCache(t, start)
	first, _ := counting([]string{"old"}, nil)
	if _, err := cache.list(context.Background(), "Vendor", first); err != nil {
		t.Fatal(err)
	}

	cache.now = func() time.Time { return start.Add(2 * time.Hour) }
	second, calls := counting([]string{"new"}, nil)
	models, err := cache.list(context.Background(), "Vendor", second)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 || !slices.Equal(models, []string{"new"}) {
		t.Errorf("calls = %d, models = %q", *calls, models)
	}
}

func TestModelCacheFallsBackToStaleEntryWhenFetchFails(t *testing.T) {
	start := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	cache := testCache(t, start)
	first, _ := counting([]string{"stale"}, nil)
	if _, err := cache.list(context.Background(), "Vendor", first); err != nil {
		t.Fatal(err)
	}

	cache.now = func() time.Time { return start.Add(48 * time.Hour) }
	failing, _ := counting(nil, errors.New("cli missing"))
	models, err := cache.list(context.Background(), "Vendor", failing)
	if err != nil || !slices.Equal(models, []string{"stale"}) {
		t.Errorf("models = %q, err = %v, want stale entry", models, err)
	}
}

func TestModelCacheReportsFailureWithoutAnyEntry(t *testing.T) {
	cache := testCache(t, time.Now())
	failing, _ := counting(nil, errors.New("cli missing"))
	if _, err := cache.list(context.Background(), "Vendor", failing); err == nil {
		t.Fatal("expected the fetch error")
	}
}

func TestWithDefaultIsFirstAndUnique(t *testing.T) {
	got := withDefault([]string{"x", "default", "", "y"})
	if !slices.Equal(got, []string{"default", "x", "y"}) {
		t.Errorf("withDefault = %q", got)
	}
}

func TestParseOMPModelsReturnsChatSelectors(t *testing.T) {
	data := []byte(`{"models":[
		{"provider":"openai-codex","kind":"chat","id":"gpt-5.5","selector":"openai-codex/gpt-5.5"},
		{"provider":"openai","kind":"embedding","id":"e","selector":"openai/e"},
		{"provider":"openai-codex","kind":"chat","id":"gpt-5.5","selector":"openai-codex/gpt-5.5"},
		{"provider":"nvidia","kind":"chat","id":"n","selector":"nvidia/n"}]}`)
	got, err := parseOMPModels(data)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"openai-codex/gpt-5.5", "nvidia/n"}) {
		t.Errorf("models = %q", got)
	}
	if _, err = parseOMPModels([]byte("not json")); err == nil {
		t.Error("malformed output accepted")
	}
}

func TestParseCodexModelsCacheKeepsListedAPIModels(t *testing.T) {
	data := []byte(`{"fetched_at":"2026-09-26T01:16:03Z","models":[
		{"slug":"gpt-6-astra","visibility":"list","supported_in_api":true},
		{"slug":"codex-auto-review","visibility":"hide","supported_in_api":true},
		{"slug":"gpt-old","visibility":"list","supported_in_api":false},
		{"slug":"","visibility":"list","supported_in_api":true}]}`)
	got, err := parseCodexModelsCache(data)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"gpt-6-astra"}) {
		t.Errorf("models = %q", got)
	}
}

func TestCodexModelListerPrefersCLICacheThenFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	cache := testCache(t, time.Now())
	fallback, calls := counting([]string{"from-http"}, nil)
	lister := codexModelLister(*cache, fallback)

	got, err := lister(context.Background())
	if err != nil || !slices.Equal(got, []string{"default", "from-http"}) || *calls != 1 {
		t.Fatalf("without CLI cache: models = %q, err = %v, fallback calls = %d", got, err, *calls)
	}

	cliCache := `{"models":[{"slug":"gpt-6-sol","visibility":"list","supported_in_api":true}]}`
	if err = os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(cliCache), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = lister(context.Background())
	if err != nil || !slices.Equal(got, []string{"default", "gpt-6-sol"}) || *calls != 1 {
		t.Errorf("with CLI cache: models = %q, err = %v, fallback calls = %d", got, err, *calls)
	}
}

func TestCodexModelListerWithoutAnySourceListsDefault(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	got, err := codexModelLister(*testCache(t, time.Now()), nil)(context.Background())
	if err != nil || !slices.Equal(got, []string{"default"}) {
		t.Errorf("models = %q, err = %v", got, err)
	}
}

func TestOMPModelListerUsesCacheAndDegradesToDefault(t *testing.T) {
	cache := testCache(t, time.Now())
	if err := cache.write(filepath.Join(cache.dir, "OMP.json"), []string{"nvidia/n"}); err != nil {
		t.Fatal(err)
	}
	got, err := ompModelLister(*cache)(context.Background())
	if err != nil || !slices.Equal(got, []string{"default", "nvidia/n"}) {
		t.Errorf("cached: models = %q, err = %v", got, err)
	}

	t.Setenv("PATH", t.TempDir())
	empty := testCache(t, time.Now())
	got, err = ompModelLister(*empty)(context.Background())
	if err != nil || !slices.Equal(got, []string{"default"}) {
		t.Errorf("no omp on PATH: models = %q, err = %v", got, err)
	}
}
