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

package spec

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"text/template"
	"text/template/parse"
)

// ErrSession is returned for a memory session that is not a valid session
// name: letters, digits, "-", "_", "=", "/" and dots between them.
var ErrSession = errors.New(`spec: a memory session must be letters, digits, "-", "_", "=", "/" and inner dots`)

// sessionPattern is the set of keys every memory backend accepts (NATS KV
// keys being the strictest); it also keeps wildcards out of a session.
var sessionPattern = regexp.MustCompile(`^[A-Za-z0-9_=/-]+(\.[A-Za-z0-9_=/-]+)*$`)

// SessionTemplate is a parsed [Memory.Session]. A session is a template
// over the same job context as a step prompt (see [ParsePrompt]), so
// "user-{{.input.user_id}}" gives every user a memory of their own. Unlike a
// prompt, it fails closed: a missing key is an error, and so is an action
// that renders nothing (an empty user id), so a session never silently
// collapses into one shared by everyone.
type SessionTemplate struct {
	t *template.Template
	// actions are the session's value actions, each as a template of its
	// own, to tell one that renders nothing from the text around it.
	actions []*template.Template
	static  bool
}

// ParseSession parses a memory session. A session without actions is
// static and checked here; a templated one is checked when rendered.
func ParseSession(text string) (*SessionTemplate, error) {
	t, err := template.New("session").Funcs(promptFuncs).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}

	s := &SessionTemplate{t: t, static: IsStatic(t)}
	if s.static {
		if err = ValidSession(text); err != nil {
			return nil, err
		}

		return s, nil
	}

	for _, n := range t.Root.Nodes {
		// An action that only declares a variable renders nothing by design.
		a, ok := n.(*parse.ActionNode)
		if !ok || len(a.Pipe.Decl) > 0 {
			continue
		}

		tree := &parse.Tree{Name: a.String(), Root: &parse.ListNode{NodeType: parse.NodeList, Nodes: []parse.Node{a}}}

		at, aerr := template.New(tree.Name).Funcs(promptFuncs).Option("missingkey=error").AddParseTree(tree.Name, tree)
		if aerr != nil {
			return nil, fmt.Errorf("session: %w", aerr)
		}

		s.actions = append(s.actions, at)
	}

	return s, nil
}

// Static reports whether the session has no template actions.
func (s *SessionTemplate) Static() bool { return s.static }

// Render renders the session over data (the job context) and checks the
// result.
func (s *SessionTemplate) Render(data map[string]any) (string, error) {
	var b strings.Builder
	if err := s.t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("session: %w", err)
	}

	out := b.String()
	if err := ValidSession(out); err != nil {
		return "", err
	}

	for _, a := range s.actions {
		var part strings.Builder

		// An action that cannot run alone uses a variable declared before
		// it; the whole session rendered, so it is left unchecked.
		if err := a.Execute(&part, data); err == nil && part.Len() == 0 {
			return "", fmt.Errorf("%w: %s renders nothing in %q", ErrSession, a.Name(), out)
		}
	}

	return out, nil
}

// ValidSession checks a rendered memory session.
func ValidSession(s string) error {
	if !sessionPattern.MatchString(s) {
		return fmt.Errorf("%w: got %q", ErrSession, s)
	}

	return nil
}

// IsStatic reports whether a parsed template is plain text, with no
// actions: it renders the same whatever the job.
func IsStatic(t *template.Template) bool {
	if t.Tree == nil || t.Root == nil {
		return true
	}

	for _, n := range t.Root.Nodes {
		if n.Type() != parse.NodeText {
			return false
		}
	}

	return true
}
