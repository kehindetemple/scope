package answer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AIConfig says which AI model answers questions. Providers:
//
//	"anthropic"  - Claude, through Anthropic's API (needs a key)
//	"openai"     - OpenAI (needs a key)
//	"gemini"     - Google Gemini (needs a key)
//	"grok"       - xAI Grok (needs a key)
//	"kimi"       - Moonshot Kimi (needs a key)
//	"deepseek"   - DeepSeek (needs a key)
//	"mistral"    - Mistral AI (needs a key)
//	"groq"       - Groq, fast hosting of open-source models (needs a key)
//	"openrouter" - OpenRouter, many models through one key
//	"ollama"     - a model running on this PC through Ollama (no key)
//
// BaseURL, if set, overrides the provider's address. Any service that speaks
// the OpenAI chat format can be used that way.
type AIConfig struct {
	Provider string
	Model    string
	APIKey   string
	BaseURL  string
}

// aiMaxChars keeps answers short enough to fit the results area.
const aiMaxChars = 900

const systemPrompt = "Answer the user's question directly and clearly in plain text, " +
	"in at most about 120 words. Do not use markdown."

type preset struct {
	anthropic    bool   // uses Anthropic's own message format
	baseURL      string // where the provider lives
	defaultModel string // used when no model name is set
}

// Every provider except Anthropic and Ollama speaks the OpenAI chat format.
// The default model names are only starting points: model names change, so
// the user can type any name in the AI answers window.
var presets = map[string]preset{
	"anthropic":  {anthropic: true, baseURL: "https://api.anthropic.com", defaultModel: "claude-haiku-5-5"},
	"openai":     {baseURL: "https://api.openai.com/v1", defaultModel: "gpt-4o-mini"},
	"gemini":     {baseURL: "https://generativelanguage.googleapis.com/v1beta/openai", defaultModel: "gemini-2.5-flash"},
	"grok":       {baseURL: "https://api.x.ai/v1", defaultModel: "grok-3"},
	"kimi":       {baseURL: "https://api.moonshot.ai/v1", defaultModel: "kimi-k2.6"},
	"deepseek":   {baseURL: "https://api.deepseek.com", defaultModel: "deepseek-chat"},
	"mistral":    {baseURL: "https://api.mistral.ai/v1", defaultModel: "mistral-small-latest"},
	"groq":       {baseURL: "https://api.groq.com/openai/v1", defaultModel: "llama-3.3-70b-versatile"},
	"openrouter": {baseURL: "https://openrouter.ai/api/v1", defaultModel: "openrouter/auto"},
	"ollama":     {baseURL: "http://localhost:11434/v1", defaultModel: "llama3.2:latest"},
}

// aiClient allows for slow answers: a local model can take a while to load.
var aiClient = &http.Client{Timeout: 180 * time.Second}

// userError is a message written for the person using the app.
type userError string

func (e userError) Error() string { return string(e) }

// httpStatusError is a non-success answer from an AI service.
type httpStatusError struct {
	code   int
	detail string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.code, e.detail)
}

// AskAI sends a question to the configured AI model and returns its reply.
// It blocks, so call it from a goroutine.
func AskAI(question string, cfg AIConfig) (Result, error) {
	q := strings.TrimSpace(question)
	if q == "" {
		return Result{}, ErrNoAnswer
	}

	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	p, known := presets[provider]
	if !known {
		// Not one of the presets: treat it as an OpenAI-compatible service at BaseURL.
		p = preset{}
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = p.baseURL
	}
	if base == "" {
		return Result{}, userError("Choose a model provider in AI answers.")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = p.defaultModel
	}
	if model == "" {
		return Result{}, userError("Enter the model name in AI answers.")
	}
	key := strings.TrimSpace(cfg.APIKey)
	if provider != "ollama" && key == "" {
		return Result{}, userError("Add an API key in AI answers first.")
	}

	var text string
	var err error
	if p.anthropic {
		text, err = askAnthropic(base, key, model, q)
	} else {
		text, err = askOpenAI(base, key, model, q)
	}
	if err != nil {
		return Result{}, aiError(provider, err)
	}
	text = shortenTo(text, aiMaxChars)
	if text == "" {
		return Result{}, ErrNoAnswer
	}
	return Result{Title: model, Text: text}, nil
}

func askAnthropic(base, key, model, q string) (string, error) {
	body := map[string]any{
		"model":      model,
		"max_tokens": 600,
		"system":     systemPrompt,
		"messages":   []map[string]string{{"role": "user", "content": q}},
	}
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	headers := map[string]string{
		"x-api-key":         key,
		"anthropic-version": "2023-06-01",
	}
	if err := postAI(base+"/v1/messages", headers, body, &resp); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, c := range resp.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return strings.TrimSpace(b.String()), nil
}

func askOpenAI(base, key, model, q string) (string, error) {
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": q},
		},
	}
	headers := map[string]string{}
	if key != "" {
		headers["Authorization"] = "Bearer " + key
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := postAI(base+"/chat/completions", headers, body, &resp); err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", nil
	}
	return strings.TrimSpace(resp.Choices[0].Message.Content), nil
}

func postAI(u string, headers map[string]string, body any, v any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	for k, hv := range headers {
		if hv != "" {
			req.Header.Set(k, hv)
		}
	}
	resp, err := aiClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return &httpStatusError{code: resp.StatusCode, detail: strings.TrimSpace(string(detail))}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(v)
}

// aiError turns a failed AI request into a message the person can act on.
func aiError(provider string, err error) error {
	var se *httpStatusError
	var ue *url.Error
	switch {
	case errors.As(err, &se):
		switch {
		case se.code == http.StatusUnauthorized || se.code == http.StatusForbidden:
			return userError("The API key was rejected. Check it in AI answers.")
		case se.code == http.StatusNotFound && provider == "ollama":
			return userError("That model isn't installed in Ollama. Run: ollama pull <model name>")
		case se.code == http.StatusNotFound:
			return userError("The model name wasn't found. Check it in AI answers.")
		case se.code == http.StatusTooManyRequests || se.code == http.StatusPaymentRequired:
			return userError("The AI service is rate-limiting or out of credit. Try again later.")
		default:
			return userError(fmt.Sprintf("The AI service returned HTTP %d.", se.code))
		}
	case errors.As(err, &ue) && provider == "ollama":
		return userError("Ollama isn't running. Start Ollama, then try again.")
	}
	return err
}
