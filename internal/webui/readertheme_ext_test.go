package webui_test

import "testing"

func TestReaderThemeFollowsTheAppTheme(t *testing.T) {
	runNodeTests(t, "readertheme.test.mjs")
}
