package webui_test

import "testing"

func TestReaderLiveTransport(t *testing.T) {
	runNodeTests(t, "readerlive.test.mjs")
}

func TestReaderLiveState(t *testing.T) {
	runNodeTests(t, "readersync.test.mjs")
}

func TestReaderCredentialLifecycle(t *testing.T) {
	runNodeTests(t, "readerauth.test.mjs")
}

func TestReaderAnnotationReplacement(t *testing.T) {
	runNodeTests(t, "readerannotations.test.mjs")
}
