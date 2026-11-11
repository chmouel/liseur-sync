package webui_test

import "testing"

// TestAudiobookSeekCommitsWithoutChange runs the node unit tests for
// static/audio-seek.js: a tap that fires input and pointerup but no
// change must still commit, and every gesture end clears the latch.
func TestAudiobookSeekCommitsWithoutChange(t *testing.T) {
	runNodeTests(t, "audioseek.test.mjs")
}

// TestReaderScreenWakeLock runs the node unit tests for
// static/audio-wake-lock.js: hidden pages never request, hiding
// releases, and returning visible acquires again.
func TestReaderScreenWakeLock(t *testing.T) {
	runNodeTests(t, "audiowakelock.test.mjs")
}

// TestAudiobookMediaSessionHelper runs the node unit tests for
// static/audio-media-session.js, shared by the book page and the reader.
func TestAudiobookMediaSessionHelper(t *testing.T) {
	runNodeTests(t, "audiomediasession.test.mjs")
}

// TestReaderWakeLockWantsIntent runs the node unit tests for the
// reader's own wake-lock rule: a stall or a seek keeps the screen on,
// and a pause, an end, a failed element or a hidden page drops it.
func TestReaderWakeLockWantsIntent(t *testing.T) {
	runNodeTests(t, "readerwakelock.test.mjs")
}
