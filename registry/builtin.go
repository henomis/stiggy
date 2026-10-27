// Copyright 2026 Simone Vellei
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package registry

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"math"
	"slices"

	"github.com/henomis/packtrail/flow"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/llm/anthropic"
	"github.com/henomis/phero/v2/llm/google"
	"github.com/henomis/phero/v2/llm/openai"
	"github.com/henomis/phero/v2/tool/bash"
	"github.com/henomis/phero/v2/tool/file"
	"github.com/henomis/phero/v2/tool/kv"
	"github.com/henomis/phero/v2/tool/skill"
	"github.com/henomis/phero/v2/tool/web"

	"github.com/henomis/stiggy/spec"
)

// Names of built-ins shared by several kinds of entries.
const (
	nameOpenAI = "openai"
	nameOllama = "ollama"
	namePsql   = "psql"
)

// ollamaAPIKey is what Ollama's OpenAI-compatible endpoint expects when no
// key is configured; it ignores the value.
const ollamaAPIKey = "ollama"

func builtinProviders() map[string]ModelFactory {
	return map[string]ModelFactory{
		nameOpenAI:  newOpenAI,
		"anthropic": newAnthropic,
		"google":    newGoogle,
		nameOllama:  newOllama,
	}
}

func newOpenAI(m spec.Model) (llm.LLM, error) {
	opts, err := openAIOptions(m)
	if err != nil {
		return nil, err
	}

	if m.MaxTokens > 0 {
		opts = append(opts, openai.WithMaxTokens(int64(m.MaxTokens)))
	}

	return openai.New(m.APIKey, opts...), nil
}

// newOllama uses Ollama's OpenAI-compatible endpoint. It caps output with the
// classic max_tokens field, which compatible servers honour more widely than
// max_completion_tokens.
func newOllama(m spec.Model) (llm.LLM, error) {
	if m.Model == "" {
		return nil, fmt.Errorf("%w: the ollama provider needs a model", ErrOptions)
	}

	if m.BaseURL == "" {
		m.BaseURL = openai.OllamaBaseURL
	}

	if m.APIKey == "" {
		m.APIKey = ollamaAPIKey
	}

	opts, err := openAIOptions(m)
	if err != nil {
		return nil, err
	}

	if m.MaxTokens > 0 {
		opts = append(opts, openai.WithLegacyMaxTokens(int64(m.MaxTokens)))
	}

	return openai.New(m.APIKey, opts...), nil
}

func openAIOptions(m spec.Model) ([]openai.Option, error) {
	if m.PromptCacheTTL != "" {
		return nil, fmt.Errorf("%w: %s models take no prompt_cache_ttl", ErrOptions, m.Provider)
	}

	opts := []openai.Option{openai.WithBaseURL(m.BaseURL)}
	if m.Model != "" {
		opts = append(opts, openai.WithModel(m.Model))
	}

	if m.Temperature != nil {
		opts = append(opts, openai.WithTemperature(float32(*m.Temperature)))
	}

	if m.StrictTools != nil {
		opts = append(opts, openai.WithStrictTools(*m.StrictTools))
	}

	return opts, nil
}

func newAnthropic(m spec.Model) (llm.LLM, error) {
	opts := []anthropic.Option{anthropic.WithBaseURL(m.BaseURL)}
	if m.Model != "" {
		opts = append(opts, anthropic.WithModel(m.Model))
	}

	if m.MaxTokens > 0 {
		opts = append(opts, anthropic.WithMaxTokens(int64(m.MaxTokens)))
	}

	if m.Temperature != nil {
		opts = append(opts, anthropic.WithTemperature(float32(*m.Temperature)))
	}

	if m.StrictTools != nil {
		opts = append(opts, anthropic.WithStrictTools(*m.StrictTools))
	}

	if m.PromptCacheTTL != "" {
		opts = append(opts, anthropic.WithPromptCacheTTL(anthropic.CacheTTL(m.PromptCacheTTL)))
	}

	return anthropic.New(m.APIKey, opts...), nil
}

// newGoogle uses the Gemini API through phero's native Google provider, which
// keeps Gemini 3's thought signatures across tool calls (the OpenAI-compatible
// endpoint loses them, so tools fail there). An empty api_key falls back to
// GOOGLE_API_KEY or GEMINI_API_KEY.
func newGoogle(m spec.Model) (llm.LLM, error) {
	if m.StrictTools != nil || m.PromptCacheTTL != "" {
		return nil, fmt.Errorf("%w: google models take no strict_tools or prompt_cache_ttl", ErrOptions)
	}

	opts := []google.Option{google.WithBaseURL(m.BaseURL), google.WithModel(m.Model)}

	if m.MaxTokens > 0 {
		maxTokens := min(m.MaxTokens, math.MaxInt32)
		opts = append(opts, google.WithMaxTokens(int32(maxTokens))) //nolint:gosec // clamped to MaxInt32 above
	}

	if m.Temperature != nil {
		opts = append(opts, google.WithTemperature(float32(*m.Temperature)))
	}

	return google.New(m.APIKey, opts...), nil
}

type bashOptions struct {
	Allowlist      []string      `json:"allowlist"`
	Blocklist      []string      `json:"blocklist"`
	SafeMode       bool          `json:"safe_mode"`
	WorkingDir     string        `json:"working_dir"`
	Timeout        flow.Duration `json:"timeout"`
	MaxTimeout     flow.Duration `json:"max_timeout"`
	MaxOutputChars int           `json:"max_output_chars"`
	// Env adds variables to every command's environment; EnvAllowlist
	// limits the inherited environment to the names listed.
	Env          map[string]string `json:"env"`
	EnvAllowlist []string          `json:"env_allowlist"`
	// BackgroundTimeout and MaxBackgroundOutput bound commands run in the
	// background.
	BackgroundTimeout   flow.Duration `json:"background_timeout"`
	MaxBackgroundOutput int           `json:"max_background_output"`
	// Sandbox runs every command in a sandbox.
	Sandbox *sandboxOptions `json:"sandbox"`
}

// sandboxOptions configures the sandbox of bash commands. The only type is
// bubblewrap: commands may write in workspace (default: the tool's
// working_dir) and the writable paths, read the read_only ones, and reach
// the network only with network set.
type sandboxOptions struct {
	Type      string   `json:"type"`
	Workspace string   `json:"workspace"`
	Writable  []string `json:"writable"`
	ReadOnly  []string `json:"read_only"`
	Network   bool     `json:"network"`
}

type fileOptions struct {
	WorkingDir     string   `json:"working_dir"`
	MaxFileSize    int64    `json:"max_file_size"`
	ReadOnlyRoots  []string `json:"read_only_roots"`
	MaxOutputChars int      `json:"max_output_chars"`
	MaxResults     int      `json:"max_results"`
	NoIgnore       bool     `json:"no_ignore"`
}

type fileWriteOptions struct {
	fileOptions

	NoOverwrite bool `json:"no_overwrite"`
}

// skillOptions reads skills from root, or from several roots; an earlier
// root wins a skill name held by several.
type skillOptions struct {
	Root  string   `json:"root"`
	Roots []string `json:"roots"`
}

type webOptions struct {
	AllowedHosts    []string      `json:"allowed_hosts"`
	PrivateNetworks bool          `json:"private_networks"`
	Timeout         flow.Duration `json:"timeout"`
	MaxBytes        int64         `json:"max_bytes"`
	MaxOutputChars  int           `json:"max_output_chars"`
}

type kvOptions struct {
	Bucket string `json:"bucket"`
}

// toolOf adapts phero's typed tool constructors, which all expose Tool().
func toolOf[T interface{ Tool() *llm.Tool }](t T, err error) ([]*llm.Tool, error) {
	if err != nil {
		return nil, err
	}

	return []*llm.Tool{t.Tool()}, nil
}

func (o fileOptions) phero() []file.Option {
	var opts []file.Option
	if o.WorkingDir != "" {
		opts = append(opts, file.WithWorkingDirectory(o.WorkingDir))
	}

	if o.MaxFileSize > 0 {
		opts = append(opts, file.WithMaxFileSize(o.MaxFileSize))
	}

	if len(o.ReadOnlyRoots) > 0 {
		opts = append(opts, file.WithReadOnlyRoots(o.ReadOnlyRoots...))
	}

	if o.MaxOutputChars > 0 {
		opts = append(opts, file.WithMaxOutputChars(o.MaxOutputChars))
	}

	if o.MaxResults > 0 {
		opts = append(opts, file.WithMaxResults(o.MaxResults))
	}

	if o.NoIgnore {
		opts = append(opts, file.WithNoIgnore())
	}

	return opts
}

func builtinToolTypes() map[string]ToolType {
	return map[string]ToolType{
		"bash": NewToolType(newBash),
		"file_read": NewToolType(func(_ context.Context, o fileOptions, _ Env) ([]*llm.Tool, error) {
			return toolOf(file.NewReadTool(o.phero()...))
		}),
		"file_write": NewToolType(func(_ context.Context, o fileWriteOptions, _ Env) ([]*llm.Tool, error) {
			opts := o.phero()
			if o.NoOverwrite {
				opts = append(opts, file.WithNoOverwrite())
			}

			return toolOf(file.NewWriteTool(opts...))
		}),
		"file_edit": NewToolType(func(_ context.Context, o fileOptions, _ Env) ([]*llm.Tool, error) {
			return toolOf(file.NewEditTool(o.phero()...))
		}),
		"file_multi_edit": NewToolType(func(_ context.Context, o fileOptions, _ Env) ([]*llm.Tool, error) {
			return toolOf(file.NewMultiEditTool(o.phero()...))
		}),
		"glob": NewToolType(func(_ context.Context, o fileOptions, _ Env) ([]*llm.Tool, error) {
			return toolOf(file.NewGlobTool(o.phero()...))
		}),
		"grep": NewToolType(func(_ context.Context, o fileOptions, _ Env) ([]*llm.Tool, error) {
			return toolOf(file.NewGrepTool(o.phero()...))
		}),
		"skill":     NewToolType(newSkill),
		"web_fetch": NewToolType(newWebFetch),
		"mcp":       NewToolType(newMCPTools),
		"kv": NewToolType(func(_ context.Context, o kvOptions, env Env) ([]*llm.Tool, error) {
			if o.Bucket == "" {
				return nil, fmt.Errorf("%w: kv needs bucket", ErrOptions)
			}

			if env.Conn == nil {
				return nil, fmt.Errorf("%w: kv needs a NATS connection", ErrOptions)
			}

			store, err := kv.Open(env.Conn, o.Bucket)
			if err != nil {
				return nil, err
			}

			return store.Tools()
		}),
	}
}

// newBash builds a bash tool. Its background commands are killed when the
// app stops.
func newBash(_ context.Context, o bashOptions, env Env) ([]*llm.Tool, error) {
	opts, err := o.phero()
	if err != nil {
		return nil, err
	}

	t, err := bash.New(opts...)
	if err != nil {
		return nil, err
	}

	env.deferCleanup(t.Close)

	return []*llm.Tool{t.Tool()}, nil
}

func newSkill(_ context.Context, o skillOptions, env Env) ([]*llm.Tool, error) {
	roots := o.Roots
	if o.Root != "" {
		roots = append([]string{o.Root}, roots...)
	}

	if len(roots) == 0 {
		return nil, fmt.Errorf("%w: skill needs root or roots", ErrOptions)
	}

	t, err := skill.New(roots[0], skill.WithRoots(roots...))
	if err != nil {
		return nil, err
	}

	// A skill that cannot be used is left out; the others still load.
	if env.Logger != nil {
		for _, skipped := range t.Skipped() {
			env.Logger.Warn("stiggy: skill skipped", "err", skipped)
		}
	}

	return []*llm.Tool{t.Tool()}, nil
}

func newWebFetch(_ context.Context, o webOptions, _ Env) ([]*llm.Tool, error) {
	var opts []web.Option

	if len(o.AllowedHosts) > 0 {
		opts = append(opts, web.WithAllowedHosts(o.AllowedHosts...))
	}

	if o.PrivateNetworks {
		opts = append(opts, web.WithPrivateNetworks(true))
	}

	if o.Timeout > 0 {
		opts = append(opts, web.WithTimeout(o.Timeout.D()))
	}

	if o.MaxBytes > 0 {
		opts = append(opts, web.WithMaxBytes(o.MaxBytes))
	}

	if o.MaxOutputChars > 0 {
		opts = append(opts, web.WithMaxOutputChars(o.MaxOutputChars))
	}

	t, err := web.New(opts...).Tool()
	if err != nil {
		return nil, err
	}

	return []*llm.Tool{t}, nil
}

func (o bashOptions) phero() ([]bash.Option, error) {
	var opts []bash.Option

	if len(o.Allowlist) > 0 {
		opts = append(opts, bash.WithAllowlist(o.Allowlist...))
	}

	if len(o.Blocklist) > 0 {
		opts = append(opts, bash.WithBlocklist(o.Blocklist...))
	}

	if o.SafeMode {
		opts = append(opts, bash.WithSafeMode())
	}

	if o.WorkingDir != "" {
		opts = append(opts, bash.WithWorkingDirectory(o.WorkingDir))
	}

	if o.Timeout > 0 {
		opts = append(opts, bash.WithDefaultTimeout(o.Timeout.D()))
	}

	if o.MaxTimeout > 0 {
		opts = append(opts, bash.WithMaxTimeout(o.MaxTimeout.D()))
	}

	if o.MaxOutputChars > 0 {
		opts = append(opts, bash.WithMaxOutputChars(o.MaxOutputChars))
	}

	opts = append(opts, o.envOptions()...)

	if o.Sandbox != nil {
		runner, err := o.sandbox()
		if err != nil {
			return nil, err
		}

		opts = append(opts, bash.WithRunner(runner))
	}

	return opts, nil
}

// envOptions returns the options for the commands' environment and their
// background runs.
func (o bashOptions) envOptions() []bash.Option {
	var opts []bash.Option

	if len(o.Env) > 0 {
		entries := make([]string, 0, len(o.Env))
		for _, k := range slices.Sorted(maps.Keys(o.Env)) {
			entries = append(entries, k+"="+o.Env[k])
		}

		opts = append(opts, bash.WithEnv(entries...))
	}

	if len(o.EnvAllowlist) > 0 {
		opts = append(opts, bash.WithEnvAllowlist(o.EnvAllowlist...))
	}

	if o.BackgroundTimeout > 0 {
		opts = append(opts, bash.WithBackgroundTimeout(o.BackgroundTimeout.D()))
	}

	if o.MaxBackgroundOutput > 0 {
		opts = append(opts, bash.WithMaxBackgroundOutput(o.MaxBackgroundOutput))
	}

	return opts
}

// sandbox returns the runner of o.Sandbox.
func (o bashOptions) sandbox() (bash.Runner, error) {
	sb := o.Sandbox
	if sb.Type != "bubblewrap" {
		return nil, fmt.Errorf("%w: unknown bash sandbox type %q", ErrOptions, sb.Type)
	}

	workspace := cmp.Or(sb.Workspace, o.WorkingDir)
	if workspace == "" {
		return nil, fmt.Errorf("%w: a bash sandbox needs workspace or working_dir", ErrOptions)
	}

	return bash.NewBubblewrapRunner(workspace, bash.WithWritablePaths(sb.Writable...),
		bash.WithReadOnlyPaths(sb.ReadOnly...), bash.WithNetwork(sb.Network))
}
