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

package compile

import (
	"fmt"

	"github.com/henomis/stiggy/spec"
)

// Splitters a knowledge source may name.
const (
	SplitterRecursive = "recursive"
	SplitterMarkdown  = "markdown"
)

func (c *compiler) knowledge() {
	for _, name := range sortedKeys(c.fleet.Embedders) {
		e := c.fleet.Embedders[name]
		path := "embedders." + name

		c.checkName(path, "embedder", name)

		if e.Instance != nil {
			continue
		}

		switch {
		case e.Provider == "":
			c.errorf(path+".provider", "provider is required")
		case !c.cat.HasEmbedderProvider(e.Provider):
			c.errorf(path+".provider", "unknown embedding provider %q", e.Provider)
		}
	}

	for _, name := range sortedKeys(c.fleet.VectorStores) {
		v := c.fleet.VectorStores[name]
		path := "vectorstores." + name

		c.checkName(path, "vector store", name)

		if v.Instance != nil {
			continue
		}

		switch {
		case v.Type == "":
			c.errorf(path+".type", "type is required")
		case !c.cat.HasVectorStoreType(v.Type):
			c.errorf(path+".type", "unknown vector store type %q", v.Type)
		default:
			if val, ok := c.cat.(VectorStoreValidator); ok {
				if err := val.ValidateVectorStore(v); err != nil {
					c.errorf(path, "%v", err)
				}
			}
		}
	}

	for _, name := range sortedKeys(c.fleet.Knowledge) {
		c.knowledgeEntry("knowledge."+name, name, c.fleet.Knowledge[name])
	}
}

func (c *compiler) knowledgeEntry(path, name string, k spec.Knowledge) {
	c.checkName(path, "knowledge", name)

	if _, ok := c.fleet.Embedders[k.Embedder]; !ok {
		c.errorf(path+".embedder", "unknown embedder %q", k.Embedder)
	}

	if _, ok := c.fleet.VectorStores[k.VectorStore]; !ok {
		c.errorf(path+".vectorstore", "unknown vector store %q", k.VectorStore)
	}

	for i, s := range k.Sources {
		spath := fmt.Sprintf("%s.sources.%d", path, i)

		if s.Path == "" {
			c.errorf(spath+".path", "path is required")
		}

		switch s.Splitter {
		case "", SplitterRecursive, SplitterMarkdown:
		default:
			c.errorf(spath+".splitter", "unknown splitter %q (want %s or %s)", s.Splitter,
				SplitterRecursive, SplitterMarkdown)
		}

		if s.ChunkSize < 0 || s.ChunkOverlap < 0 || (s.ChunkSize > 0 && s.ChunkOverlap >= s.ChunkSize) {
			c.errorf(spath, "chunk_size and chunk_overlap must not be negative, with overlap below size")
		}
	}
}
