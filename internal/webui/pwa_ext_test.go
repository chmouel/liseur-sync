package webui_test

import "testing"

func TestOfflineWorkerLifecycle(t *testing.T) {
	runNodeTests(t, "pwa.test.mjs")
}
