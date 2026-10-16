// The API of `stiggy design` (internal/design).

import type { Problem, Project } from "./project";

export interface Catalog {
  providers: string[];
  tool_types: string[];
  activities: string[];
  memory_types: string[];
  embedder_providers: string[];
  vectorstore_types: string[];
}

export const emptyCatalog: Catalog = {
  providers: [],
  tool_types: [],
  activities: [],
  memory_types: [],
  embedder_providers: [],
  vectorstore_types: [],
};

export interface Compiled {
  ok: boolean;
  yaml: string;
  problems?: Problem[];
}

export interface ProjectFile {
  path: string;
  output: string;
  project: Project | null;
}

async function ok(res: Response): Promise<Response> {
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
  return res;
}

export async function catalog(): Promise<Catalog> {
  return (await ok(await fetch("/api/catalog"))).json();
}

/** The project file given to `stiggy design`, or null when there is none. */
export async function readProject(): Promise<ProjectFile | null> {
  const res = await fetch("/api/project");
  if (res.status === 404) return null;
  return (await ok(res)).json();
}

export async function saveProject(p: Project): Promise<ProjectFile & Compiled> {
  return (await ok(await fetch("/api/project", { method: "PUT", body: JSON.stringify(p) }))).json();
}

export async function compile(p: Project, signal?: AbortSignal): Promise<Compiled> {
  return (await ok(await fetch("/api/compile", { method: "POST", body: JSON.stringify(p), signal }))).json();
}
