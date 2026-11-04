package webui

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chmouel/liseur-sync/internal/audiomap"
	"github.com/chmouel/liseur-sync/internal/config"
	"github.com/chmouel/liseur-sync/internal/store"
)

// The speech-to-text page edits the [speech_to_text] section the way the
// Audiobookshelf page edits its own (ADR-0062, ADR-0063): it reads the
// file on every request, writes one table, and a saved change applies at
// the next start.

// speechToTextTestTimeout bounds the one transcription a save makes.
const speechToTextTestTimeout = 20 * time.Second

type speechToTextAdminView struct {
	// Running is whether this process transcribes at all.
	Running bool
	// SavedURL is the address as the page may show it.
	SavedURL     string
	SavedModel   string
	SavedEnabled bool
	HasKey       bool
	// CredentialSource names where the effective key comes from.
	CredentialSource  string
	CredentialFromEnv bool
	// EnvManaged names the non-secret LISEUR_STT_* variables that make the
	// form read-only.
	EnvManaged      []string
	NoConfigPath    bool
	FileError       string
	RestartRequired bool
}

func (v speechToTextAdminView) Editable() bool {
	return !v.NoConfigPath && v.FileError == "" && len(v.EnvManaged) == 0
}

func (v speechToTextAdminView) KeyPlaceholder() string {
	if v.HasKey {
		return "saved; leave blank to keep it"
	}
	return "required by hosted services"
}

func (s *Server) speechToTextView() speechToTextAdminView {
	v := speechToTextAdminView{Running: s.Cfg.SpeechToText.Active()}
	saved, err := config.ReadSpeechToText(s.ConfigPath)
	if err != nil {
		v.FileError = err.Error()
		saved = s.Cfg.SpeechToText
	}
	v.SavedEnabled, v.SavedModel = saved.Enabled, saved.Model
	v.SavedURL = audiobookshelfCleanURL(saved.URL)
	v.HasKey = saved.APIKey != ""
	v.CredentialSource = speechToTextCredentialSource(saved.APIKey)
	_, v.CredentialFromEnv = os.LookupEnv("LISEUR_STT_API_KEY")
	v.NoConfigPath = strings.TrimSpace(s.ConfigPath) == ""
	v.EnvManaged = config.SpeechToTextEnvironmentManaged()
	v.RestartRequired = config.EffectiveSpeechToText(saved) != s.Cfg.SpeechToText
	return v
}

func speechToTextCredentialSource(fileKey string) string {
	if value, ok := os.LookupEnv("LISEUR_STT_API_KEY"); ok {
		if value == "" {
			return "LISEUR_STT_API_KEY (set but empty)"
		}
		return "environment: LISEUR_STT_API_KEY"
	}
	if fileKey != "" {
		return "the configuration file"
	}
	return "none"
}

// speechToTextFromForm is the entry the form describes. A blank key field
// keeps the saved key; the remove box drops it.
func speechToTextFromForm(r *http.Request, previous config.SpeechToTextConfig) config.SpeechToTextConfig {
	next := previous
	next.Enabled = r.FormValue("enabled") == "on"
	next.URL = strings.TrimSpace(r.FormValue("url"))
	next.Model = strings.TrimSpace(r.FormValue("model"))
	if r.FormValue("remove_key") == "on" {
		next.APIKey = ""
	}
	if key := strings.TrimSpace(r.FormValue("api_key")); key != "" {
		next.APIKey = key
	}
	return next
}

// saveSpeechToText writes the section, after one real transcription when
// it ends up enabled. A disabled save is never tested, so turning the
// feature off works with the service down.
func (s *Server) saveSpeechToText(r *http.Request) (string, error) {
	var tested bool
	err := config.UpdateSpeechToText(s.ConfigPath, func(st *config.SpeechToTextConfig) error {
		next := speechToTextFromForm(r, *st)
		candidate, err := config.SpeechToTextCandidate(next)
		if err != nil {
			return err
		}
		*st = next
		if !candidate.Enabled {
			return nil
		}
		if !candidate.Active() {
			return errors.New("the default Groq endpoint needs an API key")
		}
		ctx, cancel := context.WithTimeout(r.Context(), speechToTextTestTimeout)
		defer cancel()
		if _, err := audiomap.TranscribeAudio(ctx, candidate.URL, candidate.APIKey,
			candidate.Model, silentWAV(), "liseur-sync-test.wav"); err != nil {
			return speechToTextTestError(err)
		}
		tested = true
		return nil
	})
	if err != nil {
		return "", err
	}
	if tested {
		return "Speech-to-text saved and tested. Restart liseur-sync to apply it.", nil
	}
	return "Speech-to-text saved and disabled. Restart liseur-sync to apply it.", nil
}

// speechToTextTestError says why a test failed without repeating anything
// the service or the transport chose to say: either can carry the key in
// some encoding, and this text reaches the page and the audit log.
func speechToTextTestError(err error) error {
	var status *audiomap.StatusError
	switch {
	case errors.As(err, &status):
		return fmt.Errorf("the transcription test failed: the service answered HTTP %d", status.Code)
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("the transcription test failed: the service did not answer in time")
	default:
		return errors.New("the transcription test failed: the service could not be reached or answered something other than a transcript")
	}
}

// silentWAV is one second of 16 kHz mono silence: the smallest request
// that proves the address, the key and the model name together.
func silentWAV() []byte {
	const rate, samples = 16000, 16000
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+samples*2))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(rate), uint32(rate * 2), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(samples*2))
	b.Write(make([]byte, samples*2))
	return b.Bytes()
}

// handleSaveSpeechToText is gated like every other configuration save:
// CSRF, the administrator's own password, one audit line.
func (s *Server) handleSaveSpeechToText(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User,
) {
	const action = "speech_to_text.save"
	if !s.checkCSRF(r, a) {
		logAdminAction(r, u, action, "speech_to_text", errForgedCSRF)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if managed := config.SpeechToTextEnvironmentManaged(); len(managed) > 0 {
		err := fmt.Errorf("%s comes from the environment; change the variable and restart",
			strings.Join(managed, ", "))
		logAdminAction(r, u, action, "speech_to_text", err)
		s.renderSpeechToText(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	if err := s.reauth(r, u); err != nil {
		logAdminAction(r, u, action, "speech_to_text", err)
		if errors.Is(err, errRateLimited) {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			s.renderSettings(w, r, a, u, settingsAdmin, settingsAdminSpeechToText, "",
				Flash{Error: err.Error()}, false, "", false)
			return
		}
		s.renderSpeechToText(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	notice, err := s.saveSpeechToText(r)
	logAdminAction(r, u, action, "speech_to_text", err)
	if err != nil {
		s.renderSpeechToText(w, r, a, u, Flash{Error: err.Error()})
		return
	}
	s.renderSpeechToText(w, r, a, u, Flash{Notice: notice})
}

func (s *Server) renderSpeechToText(
	w http.ResponseWriter, r *http.Request, a store.AuthSession, u *store.User, flash Flash,
) {
	if settingsRedirect(w, r, settingsAdmin, settingsAdminSpeechToText, "", flash) {
		return
	}
	s.renderSettings(w, r, a, u, settingsAdmin, settingsAdminSpeechToText, "", flash, false, "", false)
}
