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

import "maps"

// attachments compiles an AI block's tools and knowledge bases and returns
// their names.
func (w *wfc) attachments(n *Node) (tools, knowledge []string) {
	for _, a := range n.Attachments {
		loc := Loc{Workflow: w.wf.ID, Node: n.ID, Attachment: a.ID}

		if a.Type == AttachKnowledge {
			if k := w.knowledgeBase(loc, a); k != "" {
				knowledge = append(knowledge, k)
			}

			continue
		}

		tools = append(tools, w.tool(loc, a)...)
	}

	return tools, knowledge
}

// decode reads an attachment's settings.
func (w *wfc) decode(loc Loc, a Attachment, v any) bool {
	if len(a.Params) == 0 {
		return true
	}

	if err := decodeStrict(a.Params, v); err != nil {
		w.c.problem(loc, "invalid settings: %v", err)

		return false
	}

	return true
}

// addTool adds one tool entry and returns its name.
func (w *wfc) addTool(loc Loc, base string, v omap) string {
	name, added := w.c.tools.add(base, v)
	if added {
		w.c.source("tools."+name, loc)
	}

	return name
}

func (w *wfc) tool(loc Loc, a Attachment) []string {
	base := slug(a.Name, a.Type)

	switch a.Type {
	case AttachFiles:
		return w.fileTools(loc, a, base)
	case AttachShell:
		var p ShellParams
		if !w.decode(loc, a, &p) {
			return nil
		}

		return []string{w.addTool(loc, base, omap{}.
			set("type", "bash").
			set("working_dir", p.WorkingDir).
			set("allowlist", p.Allowlist).
			set("safe_mode", p.SafeMode).
			set("timeout", p.Timeout))}
	case AttachKV:
		var p KVParams
		if !w.decode(loc, a, &p) {
			return nil
		}

		if p.Bucket == "" {
			loc.Field = "bucket"
			w.c.problem(loc, "name the bucket")
		}

		return []string{w.addTool(loc, base, omap{}.set("type", "kv").set("bucket", p.Bucket))}
	case AttachMCP:
		var p MCPParams
		if !w.decode(loc, a, &p) {
			return nil
		}

		if (p.Command == "") == (p.URL == "") {
			w.c.problem(loc, "give either a command to start the MCP server or its URL")
		}

		return []string{w.addTool(loc, base, omap{}.
			set("type", "mcp").
			set("command", p.Command).
			set("args", p.Args).
			set("url", p.URL).
			set("allow", p.Allow).
			set("deny", p.Deny))}
	case AttachSkills:
		var p SkillsParams
		if !w.decode(loc, a, &p) {
			return nil
		}

		return []string{w.addTool(loc, base, omap{}.set("type", "skill").set("root", p.Root))}
	default:
		w.c.problem(loc, "unknown attachment type %q", a.Type)

		return nil
	}
}

// knowledgeBase adds an embedder, a vector store and a knowledge entry, and
// returns the knowledge name.
func (w *wfc) knowledgeBase(loc Loc, a Attachment) string {
	var p KnowledgeParams
	if !w.decode(loc, a, &p) {
		return ""
	}

	cr, ok := w.c.creds[p.EmbeddingCredential]
	if !ok {
		l := loc
		l.Field = "embedding_credential"
		w.c.problem(l, "choose the provider that embeds the documents")
	}

	if p.Store == "" {
		l := loc
		l.Field = "store"
		w.c.problem(l, "choose where the documents are stored")
	}

	emb := omap{}.set("provider", cr.Provider).set("model", p.EmbeddingModel)
	if cr.Env != "" {
		emb = emb.set("api_key", "${"+cr.Env+"}")
	}

	emb = emb.set("base_url", cr.BaseURL)

	embName, added := w.c.embedders.add(slug(p.EmbeddingModel, "embedder"), emb)
	if added {
		w.c.source("embedders."+embName, loc)
	}

	store := omap{}.set("type", p.Store)

	opts := maps.Clone(p.StoreOptions)
	for _, k := range sortedKeys(opts) {
		store = store.set(k, opts[k])
	}

	storeName, added := w.c.stores.add(slug(a.Name, "store")+"_store", store)
	if added {
		w.c.source("vectorstores."+storeName, loc)
	}

	k := omap{}.
		set("embedder", embName).
		set("vectorstore", storeName).
		set("description", p.Description).
		set("top_k", p.TopK)

	if p.Documents != "" {
		k = k.set("sources", []any{omap{}.
			set("path", p.Documents).
			set("chunk_size", p.ChunkSize).
			set("chunk_overlap", p.ChunkOverlap)})
	}

	name, added := w.c.knowledge.add(slug(a.Name, "knowledge"), k)
	if added {
		w.c.source("knowledge."+name, loc)
	}

	return name
}

// fileTools adds one tool per file permission the attachment grants.
func (w *wfc) fileTools(loc Loc, a Attachment, base string) []string {
	var p FilesParams
	if !w.decode(loc, a, &p) {
		return nil
	}

	kinds := []struct {
		on    bool
		types []string
	}{
		{p.Read, []string{"file_read"}},
		{p.Write, []string{"file_write"}},
		{p.Edit, []string{"file_edit"}},
		{p.Search, []string{"glob", "grep"}},
	}

	var out []string

	for _, k := range kinds {
		if !k.on {
			continue
		}

		for _, t := range k.types {
			out = append(out, w.addTool(loc, base+"_"+t, omap{}.set("type", t).set("working_dir", p.WorkingDir)))
		}
	}

	if len(out) == 0 {
		w.c.problem(loc, "choose what the agent may do with files")
	}

	return out
}
