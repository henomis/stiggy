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
	"context"
	"fmt"

	"github.com/henomis/packtrail/flow"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/llm/anthropic"
	"github.com/henomis/phero/v2/llm/openai"
	"github.com/henomis/phero/v2/tool/bash"
	"github.com/henomis/phero/v2/tool/file"
	"github.com/henomis/phero/v2/tool/kv"
	"github.com/henomis/phero/v2/tool/skill"

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
		nameOllama:  newOllama,
	}
}

func newOpenAI(m spec.Model) (llm.LLM, error) {
	opts := openAIOptions(m)
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

	opts := openAIOptions(m)
	if m.MaxTokens > 0 {
		opts = append(opts, openai.WithLegacyMaxTokens(int64(m.MaxTokens)))
	}

	return openai.New(m.APIKey, opts...), nil
}

func openAIOptions(m spec.Model) []openai.Option {
	opts := []openai.Option{openai.WithBaseURL(m.BaseURL)}
	if m.Model != "" {
		opts = append(opts, openai.WithModel(m.Model))
	}

	if m.Temperature != nil {
		opts = append(opts, openai.WithTemperature(float32(*m.Temperature)))
	}

	return opts
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

	return anthropic.New(m.APIKey, opts...), nil
}

type bashOptions struct {
	Allowlist      []string      `json:"allowlist"`
	Blocklist      []string      `json:"blocklist"`
	SafeMode       bool          `json:"safe_mode"`
	WorkingDir     string        `json:"working_dir"`
	Timeout        flow.Duration `json:"timeout"`
	MaxTimeout     flow.Duration `json:"max_timeout"`
	MaxOutputChars int           `json:"max_output_chars"`
}

type fileOptions struct {
	WorkingDir  string `json:"working_dir"`
	MaxFileSize int64  `json:"max_file_size"`
}

type fileWriteOptions struct {
	fileOptions

	NoOverwrite bool `json:"no_overwrite"`
}

type skillOptions struct {
	Root string `json:"root"`
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

	return opts
}

func builtinToolTypes() map[string]ToolType {
	return map[string]ToolType{
		"bash": NewToolType(func(_ context.Context, o bashOptions, _ Env) ([]*llm.Tool, error) {
			return toolOf(bash.New(o.phero()...))
		}),
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
		"glob": NewToolType(func(_ context.Context, o fileOptions, _ Env) ([]*llm.Tool, error) {
			return toolOf(file.NewGlobTool(o.phero()...))
		}),
		"grep": NewToolType(func(_ context.Context, o fileOptions, _ Env) ([]*llm.Tool, error) {
			return toolOf(file.NewGrepTool(o.phero()...))
		}),
		"skill": NewToolType(func(_ context.Context, o skillOptions, _ Env) ([]*llm.Tool, error) {
			if o.Root == "" {
				return nil, fmt.Errorf("%w: skill needs root", ErrOptions)
			}

			return toolOf(skill.New(o.Root))
		}),
		"mcp": NewToolType(newMCPTools),
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

func (o bashOptions) phero() []bash.Option {
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

	return opts
}
