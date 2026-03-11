package app

import "testing"

func TestResolveWorkerBaseURLUsesDefault(t *testing.T) {
	old := workerBaseURL
	workerBaseURL = ""
	t.Cleanup(func() {
		workerBaseURL = old
	})
	t.Setenv("SYL_LISTING_WORKER_URL", "")

	got := resolveWorkerBaseURL()
	if got != defaultWorkerBaseURL {
		t.Fatalf("resolveWorkerBaseURL() = %q, want %q", got, defaultWorkerBaseURL)
	}
}

func TestResolveWorkerBaseURLUsesEnvOverride(t *testing.T) {
	old := workerBaseURL
	workerBaseURL = ""
	t.Cleanup(func() {
		workerBaseURL = old
	})
	t.Setenv("SYL_LISTING_WORKER_URL", " https://worker.example.test/ ")

	got := resolveWorkerBaseURL()
	if got != "https://worker.example.test" {
		t.Fatalf("resolveWorkerBaseURL() = %q", got)
	}
}

func TestResolveWorkerBaseURLUsesExplicitOverrideFirst(t *testing.T) {
	old := workerBaseURL
	workerBaseURL = "https://override.test"
	t.Cleanup(func() {
		workerBaseURL = old
	})
	t.Setenv("SYL_LISTING_WORKER_URL", "https://worker.example.test")

	got := resolveWorkerBaseURL()
	if got != "https://override.test" {
		t.Fatalf("resolveWorkerBaseURL() = %q", got)
	}
}
