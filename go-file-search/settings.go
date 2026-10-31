//go:build windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"go-file-search/internal/hotkey"
)

// appSettings is what settings.json holds. The first two fields are the
// hotkey, written the same way as in older versions, so existing files work.
type appSettings struct {
	Modifiers   []string `json:"modifiers"`
	Key         string   `json:"key"`
	Theme       string   `json:"theme,omitempty"`       // "dark" (default) or "light"
	InputColor  string   `json:"inputColor,omitempty"`  // "#RRGGBB"; empty follows the theme
	ResultColor string   `json:"resultColor,omitempty"` // "#RRGGBB"; empty follows the theme
	SearchAPIKey string  `json:"searchApiKey,omitempty"` // Serper key for Google results on ~ questions
	AnswerSource string  `json:"answerSource,omitempty"` // "wikipedia" (default), "google" or "ai"
	AIProvider   string  `json:"aiProvider,omitempty"`   // "anthropic", "openai", "deepseek" or "ollama"
	AIModel      string  `json:"aiModel,omitempty"`      // empty uses the provider's default model
	AIAPIKey     string  `json:"aiApiKey,omitempty"`     // not needed for ollama
	AIBaseURL    string  `json:"aiBaseUrl,omitempty"`    // optional: a different OpenAI-compatible address
}

func (s appSettings) spec() hotkey.Spec {
	return hotkey.Spec{Modifiers: s.Modifiers, Key: s.Key}
}

func (s *appSettings) setSpec(spec hotkey.Spec) {
	s.Modifiers = spec.Modifiers
	s.Key = spec.Key
}

// settingsState is the app's configuration. It is touched only on the UI thread.
type settingsState struct {
	path    string
	app     appSettings
	hotkeys *hotkey.Manager
}

var settings settingsState

func defaultSpec() hotkey.Spec {
	return hotkey.Spec{Modifiers: []string{"ctrl"}, Key: "Space"}
}

// loadSettingsFile reads the settings, replacing anything missing or invalid
// with its default.
func loadSettingsFile(path string) appSettings {
	var s appSettings
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &s) != nil {
			s = appSettings{}
		}
	}
	if _, err := hotkey.Parse(s.spec()); err != nil {
		s.setSpec(defaultSpec())
	}
	if s.Theme != "light" {
		s.Theme = "dark"
	}
	if _, ok := parseHexColor(s.InputColor); !ok {
		s.InputColor = ""
	}
	if _, ok := parseHexColor(s.ResultColor); !ok {
		s.ResultColor = ""
	}
	return s
}

func saveSettingsFile(path string, s appSettings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
