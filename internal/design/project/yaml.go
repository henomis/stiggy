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

package project

import (
	"bytes"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"

	"gopkg.in/yaml.v3"
)

const (
	tagInt = "!!int"
	// indent is the YAML indentation, as in the fleets people write.
	indent = 2
)

// kv is one key of an ordered mapping.
type kv struct {
	k string
	v any
}

// omap is a YAML mapping that keeps its key order, so the generated fleet
// reads like one written by hand.
type omap []kv

// set appends k unless v is empty; it reports the mapping for chaining.
func (m omap) set(k string, v any) omap {
	if empty(v) {
		return m
	}

	return append(m, kv{k, v})
}

func empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case int:
		return x == 0
	case bool:
		return !x
	case omap:
		return len(x) == 0
	case []any:
		return len(x) == 0
	case []string:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	default:
		return false
	}
}

// node turns a value into a YAML node.
func node(v any) *yaml.Node {
	switch x := v.(type) {
	case omap:
		n := &yaml.Node{Kind: yaml.MappingNode}
		for _, e := range x {
			n.Content = append(n.Content, scalar(e.k), node(e.v))
		}

		return n
	case map[string]any:
		n := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range slices.Sorted(maps.Keys(x)) {
			n.Content = append(n.Content, scalar(k), node(x[k]))
		}

		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode}
		for _, e := range x {
			n.Content = append(n.Content, node(e))
		}

		return n
	case []string:
		n := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, e := range x {
			n.Content = append(n.Content, scalar(e))
		}

		return n
	case string:
		return scalar(x)
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(x)}
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagInt, Value: strconv.Itoa(x)}
	case float64:
		// JSON numbers arrive as floats: write whole ones as integers.
		if x == math.Trunc(x) && math.Abs(x) < 1<<53 {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagInt, Value: strconv.FormatInt(int64(x), 10)}
		}

		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: strconv.FormatFloat(x, 'g', -1, 64)}
	default:
		// Store options and the like: let the encoder pick.
		var n yaml.Node
		if err := n.Encode(x); err != nil {
			return scalar(fmt.Sprint(x))
		}

		return &n
	}
}

// scalar is a string node; multi-line text is written as a block.
func scalar(s string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	if bytes.ContainsRune([]byte(s), '\n') {
		n.Style = yaml.LiteralStyle
	}

	return n
}

// encode writes a document.
func encode(v any) ([]byte, error) {
	var b bytes.Buffer

	enc := yaml.NewEncoder(&b)
	enc.SetIndent(indent)

	if err := enc.Encode(node(v)); err != nil {
		return nil, fmt.Errorf("project: %w", err)
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("project: %w", err)
	}

	return b.Bytes(), nil
}

// key is a value's identity for merging: two equal values encode the same.
func key(v any) string {
	b, err := encode(v)
	if err != nil {
		return fmt.Sprint(v)
	}

	return string(b)
}
