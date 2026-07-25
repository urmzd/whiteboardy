// Package llm adapts the saige agent SDK to whiteboardy's needs: building a
// provider from config, and running one-shot structured generations that come
// back as typed Go values.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	agentsdk "github.com/urmzd/saige/agent"
	"github.com/urmzd/saige/agent/provider/anthropic"
	"github.com/urmzd/saige/agent/provider/google"
	"github.com/urmzd/saige/agent/provider/ollama"
	"github.com/urmzd/saige/agent/provider/openai"
	"github.com/urmzd/saige/agent/types"
)

// Kind identifies a supported provider backend.
type Kind string

const (
	KindOllama    Kind = "ollama"
	KindOpenAI    Kind = "openai"
	KindAnthropic Kind = "anthropic"
	KindGoogle    Kind = "google"
)

// Config describes how to reach a model.
type Config struct {
	Kind Kind `json:"kind"`
	// Model is the model identifier, e.g. "qwen3.5:9b" or "claude-sonnet-5".
	Model string `json:"model"`
	// Host is the base URL. Only meaningful for ollama; defaults to the local
	// daemon when empty.
	Host string `json:"host"`
	// APIKey authenticates hosted providers. Ignored by ollama.
	APIKey string `json:"apiKey"`
}

// DefaultOllamaHost is where a local ollama daemon listens.
const DefaultOllamaHost = "http://localhost:11434"

// ErrNoModel is returned when a config names no model.
var ErrNoModel = errors.New("llm: no model configured")

// Client wraps a provider and exposes the generation helpers the harness uses.
type Client struct {
	provider types.Provider
	cfg      Config
	log      *slog.Logger
}

// New builds a Client from config. It does not contact the provider, so a bad
// host or key surfaces on first use rather than here.
func New(ctx context.Context, cfg Config, log *slog.Logger) (*Client, error) {
	if log == nil {
		log = slog.Default()
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, ErrNoModel
	}

	var p types.Provider
	switch cfg.Kind {
	case KindOllama, "":
		host := cfg.Host
		if host == "" {
			host = DefaultOllamaHost
		}
		// num_ctx is set explicitly because ollama's default window is far
		// smaller than a review prompt (problem, hidden rubric, whole board,
		// event log) and it truncates past the limit silently.
		p = ollama.NewAdapter(ollama.NewClient(host, cfg.Model, "",
			ollama.WithChatOptions(ollama.Options{
				NumCtx:      contextBudget,
				Temperature: Temperature,
			}),
		))
	case KindOpenAI:
		if cfg.APIKey == "" {
			return nil, errors.New("llm: openai requires an api key")
		}
		p = openai.NewAdapter(cfg.APIKey, cfg.Model)
	case KindAnthropic:
		if cfg.APIKey == "" {
			return nil, errors.New("llm: anthropic requires an api key")
		}
		p = anthropic.NewAdapter(cfg.APIKey, cfg.Model)
	case KindGoogle:
		if cfg.APIKey == "" {
			return nil, errors.New("llm: google requires an api key")
		}
		adapter, err := google.NewAdapter(ctx, cfg.APIKey, cfg.Model)
		if err != nil {
			return nil, fmt.Errorf("llm: google adapter: %w", err)
		}
		p = adapter
	default:
		return nil, fmt.Errorf("llm: unknown provider kind %q", cfg.Kind)
	}

	return &Client{provider: p, cfg: cfg, log: log}, nil
}

// Config returns the config the client was built from, with the API key blanked.
func (c *Client) Config() Config {
	safe := c.cfg
	if safe.APIKey != "" {
		safe.APIKey = "***"
	}
	return safe
}

// ProviderName reports the backend in use.
func (c *Client) ProviderName() string { return string(c.cfg.Kind) }

// Model reports the model in use.
func (c *Client) Model() string { return c.cfg.Model }

// Text runs a single-turn completion and returns the accumulated text.
func (c *Client) Text(ctx context.Context, system, user string) (string, error) {
	return c.run(ctx, system, user, nil)
}

// TextStream runs a single-turn completion and calls onDelta for each chunk as
// it arrives, returning the accumulated text. Used for prose the user watches
// being written; schema-constrained calls go through Structured instead,
// because partial JSON is not something a UI can render.
func (c *Client) TextStream(ctx context.Context, system, user string, onDelta func(string)) (string, error) {
	a := agentsdk.NewAgent(agentsdk.AgentConfig{
		Name:         "whiteboardy",
		SystemPrompt: system,
		Provider:     c.provider,
	}, agentsdk.WithMaxIter(1), agentsdk.WithLogger(c.log))

	stream := a.Invoke(ctx, []types.Message{types.NewUserMessage(user)})

	var b strings.Builder
	var streamErr error
	for delta := range stream.Deltas() {
		switch d := delta.(type) {
		case types.TextContentDelta:
			b.WriteString(d.Content)
			if onDelta != nil && d.Content != "" {
				onDelta(d.Content)
			}
		case types.ErrorDelta:
			streamErr = d.Error
		}
	}
	if err := stream.Wait(); err != nil {
		return b.String(), fmt.Errorf("llm: %w", err)
	}
	if streamErr != nil {
		return b.String(), fmt.Errorf("llm: %w", streamErr)
	}
	return b.String(), nil
}

// Structured runs a single-turn completion constrained to T's JSON schema and
// unmarshals the result. Schema mutators let callers narrow enums that depend
// on runtime state (for example the skill areas valid for the current mode).
func Structured[T any](ctx context.Context, c *Client, system, user string, mutators ...func(*types.ParameterSchema)) (T, error) {
	var out T
	schema := types.SchemaFrom[T]()
	for _, m := range mutators {
		m(&schema)
	}

	raw, err := c.run(ctx, system, user, &schema)
	if err != nil {
		return out, err
	}

	body, err := extractJSON(raw)
	if err != nil {
		return out, fmt.Errorf("llm: %w (model returned %d chars)", err, len(raw))
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return out, fmt.Errorf("llm: decode %T: %w", out, err)
	}
	return out, nil
}

// Temperature is the sampling temperature used for every generation. Problem
// generation wants some variety so repeated sessions do not converge on the
// same exercise; grading wants determinism. This sits closer to the grading
// end, and variety comes from the topic instead.
const Temperature = 0.4

// contextBudget is the context window whiteboardy asks a local model for.
// Large enough for a full review prompt, small enough that a small model on a
// laptop still fits it in memory.
const contextBudget = 16384

func (c *Client) run(ctx context.Context, system, user string, schema *types.ParameterSchema) (string, error) {
	opts := []agentsdk.AgentOption{
		agentsdk.WithMaxIter(1),
		agentsdk.WithLogger(c.log),
	}
	if schema != nil {
		opts = append(opts, agentsdk.WithResponseSchema(schema))
	}

	a := agentsdk.NewAgent(agentsdk.AgentConfig{
		Name:         "whiteboardy",
		SystemPrompt: system,
		Provider:     c.provider,
	}, opts...)

	stream := a.Invoke(ctx, []types.Message{types.NewUserMessage(user)})

	var b, thinking strings.Builder
	var streamErr error
	for delta := range stream.Deltas() {
		switch d := delta.(type) {
		case types.TextContentDelta:
			b.WriteString(d.Content)
		case types.ThinkingContentDelta:
			// Reasoning models sometimes emit the whole answer inside the
			// thinking channel when a response schema is in play. Keep it as a
			// fallback rather than reporting an empty response.
			thinking.WriteString(d.Content)
		case types.ErrorDelta:
			streamErr = d.Error
		}
	}
	if err := stream.Wait(); err != nil {
		return b.String(), fmt.Errorf("llm: %w", err)
	}
	if streamErr != nil {
		return b.String(), fmt.Errorf("llm: %w", streamErr)
	}
	if strings.TrimSpace(b.String()) == "" {
		if t := strings.TrimSpace(thinking.String()); t != "" {
			c.log.Warn("llm: model emitted only reasoning content, falling back to it",
				"model", c.cfg.Model, "chars", len(t))
			return t, nil
		}
		return "", errors.New("llm: model returned empty response")
	}
	return b.String(), nil
}

// extractJSON pulls a JSON object out of a model response. Schema-constrained
// providers return bare JSON, but local models sometimes wrap it in a fence or
// prefix it with a sentence, and reasoning models emit a <think> block first.
func extractJSON(raw string) (string, error) {
	s := strings.TrimSpace(raw)

	// Drop reasoning preambles.
	if i := strings.LastIndex(s, "</think>"); i >= 0 {
		s = strings.TrimSpace(s[i+len("</think>"):])
	}

	// Unwrap a fenced block.
	if strings.HasPrefix(s, "```") {
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}

	if json.Valid([]byte(s)) {
		return s, nil
	}

	// Fall back to the widest balanced object in the response.
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start >= 0 && end > start {
		candidate := s[start : end+1]
		if json.Valid([]byte(candidate)) {
			return candidate, nil
		}
	}
	return "", errors.New("no valid JSON object in response")
}
