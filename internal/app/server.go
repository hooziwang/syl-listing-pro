package app

import (
	"os"
	"strings"
)

const defaultWorkerBaseURL = "https://worker.aelus.tech"

var workerBaseURL string

var (
	pollIntervalMs     = 800
	pollTimeoutSecond  = 900
	maxConcurrentTasks = 16
)

func resolveWorkerBaseURL() string {
	if explicit := strings.TrimRight(strings.TrimSpace(workerBaseURL), "/"); explicit != "" {
		return explicit
	}
	if fromEnv := strings.TrimRight(strings.TrimSpace(getenv("SYL_LISTING_WORKER_URL")), "/"); fromEnv != "" {
		return fromEnv
	}
	return defaultWorkerBaseURL
}

var getenv = func(key string) string {
	return os.Getenv(key)
}
