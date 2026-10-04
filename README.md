# stiggy

stiggy deploys multi-agent architectures on NATS. It glues
[phero](https://github.com/henomis/phero), which provides the agents, to
[packtrail](https://github.com/henomis/packtrail), which provides durable,
event-sourced orchestration on JetStream. It adds crews, supervisors, swarms,
human review and replay on a substrate that survives crashes and scales out.

You describe a fleet once, either as a YAML file run by the `stiggy` binary
or in Go through the `github.com/henomis/stiggy` library. Both build the same
model:

- **models, tools, knowledge**: any LLM provider, tool, memory backend or
  vector store phero offers, plus MCP servers;
- **agents**: a model, a role/goal/backstory, tools, memory,
  knowledge and delegates;
- **flows**: packtrail flows whose steps run agents. Every packtrail feature
  is available: choices, fan-out/join, maps, subflows, waits, retries,
  budgets, fork, rerun and time travel;
- **crews and patterns**: task crews and the supervisor, swarm, evaluator-optimizer, debate and plan-execute, expanded into flows.

## Quick start

```bash
nats-server -js &
go install github.com/henomis/stiggy/cmd/stiggy@latest
```

```yaml
# fleet.yaml
namespace: newsroom
models:
  fast: {provider: ollama, model: gemma4:cloud}
agents:
  researcher: {model: fast, role: Researcher, goal: "Find accurate, recent facts."}
  writer: {model: fast, role: Writer, goal: "Write short, clear articles."}
crews:
  article:
    tasks:
      - {id: research, agent: researcher, description: "Research {{.input.topic}}."}
      - {id: write, agent: writer, description: Write a 200-word article from the research.}
```

```bash
stiggy validate fleet.yaml        # every problem, with its line
stiggy compile fleet.yaml         # the packtrail flows it becomes
stiggy run fleet.yaml &           # serve it
stiggy start -ns newsroom article -input '{"topic": "NATS"}' -wait
```

The same in Go:

```go
nc, _ := nats.Connect(nats.DefaultURL)

app, _ := stiggy.New(nc,
    stiggy.WithNamespace("newsroom"),
    stiggy.WithModel("fast", openai.New("ollama",
        openai.WithBaseURL(openai.OllamaBaseURL), openai.WithModel("gemma4:cloud"))),
    stiggy.WithAgent("researcher", spec.Agent{Model: "fast", Role: "Researcher"}),
    stiggy.WithAgent("writer", spec.Agent{Model: "fast", Role: "Writer"}),
    stiggy.WithCrew("article", spec.Crew{Tasks: []spec.Task{
        {ID: "research", Agent: "researcher", Description: "Research {{.input.topic}}."},
        {ID: "write", Agent: "writer", Description: "Write a 200-word article from the research."},
    }}),
)

go app.Run(ctx)
<-app.Ready()

id, _ := app.Client().Start(ctx, "article", map[string]any{"topic": "NATS"})
st, _ := app.Client().Wait(ctx, id)
```

`app.Client()` is a packtrail client: signal, resume, update, fork, rerun,
watch and stream progress from it.

## How it works

```
 fleet.yaml ─┐                         ┌─ packtrail engine (event log, timers, budgets)
             ├─► spec.Fleet ─► compile ─┤
 Go options ─┘   (validate)            └─ workers: one per agent / activity / remote
                                            └─ fresh phero agent per job
```

- A flow step that runs an agent is a packtrail task whose `meta` holds the
  step (prompt, writes, routes, ...). It is versioned with the flow, so a
  running execution keeps the configuration it started with.
- Each agent is a packtrail worker kind. Every job builds a fresh phero agent,
  runs it, and returns its output, channel writes and usage.
- Agent-chosen routing, `ask_human`, structured output and execution memory
  live in packtrail state, so they are durable and correct under fork, rerun
  and time travel.
- stiggy never touches NATS directly. It opens one connection and hands it to
  phero and packtrail, and `internal/archguard` enforces this in the test
  suite.

## Documentation

- [examples/](examples): runnable programs (below).

## Examples

| Example | Shows |
|---|---|
| [hello](examples/hello) | The smallest YAML fleet |
| [router](examples/router) | Agent-chosen routing, built with the Go API |
| [mapreduce](examples/mapreduce) | Structured output, a parallel map, an append channel |
| [hitl](examples/hitl) | `ask_human` and human review, resumed with `Client.Resume` |
| [crew](examples/crew) | A crew with a guardrail, also served as a protocol agent |
| [remote](examples/remote) | Another team's phero agent as a step and as a delegate |

Each runs against `$NATS_URL`, with a real model when `OPENAI_API_KEY`,
`ANTHROPIC_API_KEY` or `OLLAMA_MODEL` is set, and a scripted one otherwise:

```bash
go run ./examples/router
OLLAMA_MODEL=gemma4:cloud go run ./examples/mapreduce
```

The examples are tests too. `make examples` runs them with the scripted
models on an embedded server. `make examples-live` runs them against a real
model, by default `OLLAMA_MODEL=gemma4:cloud`.

## Deployment

Every process of a deployment runs the same fleet file and picks its part:

```bash
stiggy run fleet.yaml -role engine -http :8080                 # one or more engines
stiggy run fleet.yaml -role workers -http :8080                # scale out
stiggy run fleet.yaml -role workers -only researcher,writer    # dedicated hosts
```

`/healthz` and `/readyz` serve health checks. [deploy/](deploy) has a
Dockerfile and a docker-compose setup. To operate executions, use
`stiggy get | history | progress | signal | resume | update | cancel | fork | rerun`,
or packtrail's CLI and `packtrail-ui` on the fleet's namespace.

## Development

```bash
make test           # tests with -race
make check          # test + lint + vet
make examples       # the examples, scripted
make examples-live  # the examples, against a real model
```

Requires Go 1.26.4. Depends on the released `phero/v2` v2.0.0 and `packtrail` v1.0.0.

## License

Apache 2.0. See [LICENSE](LICENSE).
