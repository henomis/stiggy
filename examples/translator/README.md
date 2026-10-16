# translator

One agent used by several programs. The translator program serves a
translator agent on its own, without a workflow, exposed on the NATS Agent
Protocol as `translation/translator`. Two other programs, `support` and
`newsletter`, each run a workflow whose last step sends text to that agent
(a `remote` step), found on the bus by its owner and name.

| Program | What it does |
|---|---|
| [translator](translator) | Serves the translator until Ctrl+C |
| [support](support) | Answers a customer question, then has it translated into Italian |
| [newsletter](newsletter) | Drafts a newsletter blurb, then has it translated into Spanish |

## What you need

- NATS with JetStream, at `$NATS_URL` (default `nats://127.0.0.1:4222`):
  `nats-server -js`, or `docker run -p 4222:4222 nats -js`.
- Ollama on localhost, with access to `gemma4:cloud` (cloud models need
  `ollama signin`).

## Run

From the repository root, start the translator in one terminal and leave it
running:

```bash
nats-server -js &
go run ./examples/translator/translator
```

Then, in another terminal, run either workflow program:

```bash
go run ./examples/translator/support
go run ./examples/translator/newsletter
```

Each prints its execution's status and the translated text. Without the
translator running, the translation step fails after its retries.

## Test

```bash
go test ./examples/translator/...                                # scripted models
STIGGY_EXAMPLE_LIVE=1 go test -v -p 1 ./examples/translator/...  # against gemma4:cloud
```

The tests do not need the translator program: the `support` and
`newsletter` tests put a stand-in agent on the bus under the same owner and
name. They do need the NATS server; without one they are skipped.
