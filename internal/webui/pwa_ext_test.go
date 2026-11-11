package webui_test

import "testing"

func TestOfflineWorkerLifecycle(t *testing.T) {
	runNodeTests(t, "pwa.test.mjs")
}

// The cached shell cannot know the appearance cookie the server never
// saw, so the status bar it ships is only right for an explicit
// "system": every other answer is settled in the page.
func TestOfflineShellThemeColor(t *testing.T) {
	runNodeTests(t, "offlinetheme.test.mjs")
}
