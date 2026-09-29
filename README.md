# garm tools

**Governed tool packages you adopt instead of writing.** Each package is a set
of tools declared in proto, annotated with the `garm.tool.v1` contract, served
as NATS micro services behind [garmd](https://github.com/garm-ai/garmd), and
maintained here. You take the packages you want and nothing else.

Status, 29 September 2026: the first three packages are planned and in
implementation. Nothing here is released yet. The roadmap below says what
arrives when.

## Contents

- [What a package is](#what-a-package-is)
- [Packages](#packages)
- [Roadmap](#roadmap)
- [Adopting a package](#adopting-a-package)
- [What a governed tool looks like](#what-a-governed-tool-looks-like)
- [Safety model](#safety-model)
- [Building, testing, releasing](#building-testing-releasing)
- [How this differs from examples](#how-this-differs-from-examples)
- [Contributing](#contributing)
- [Licence](#licence)

## What a package is

One directory, one Go module, one release train. A package holds:

- the proto files that declare its tools and the annotations garmd enforces;
- the service that serves them, on [tool-go](https://github.com/garm-ai/tool-go);
- the compartments and tool sets it needs, declared once in its taxonomy;
- tests over a real broker, sending the invocation context garmd sends;
- a README that explains every annotation choice in plain words.

You depend on `github.com/garm-ai/tools/web@v0.1.0`, not on this repository.
One repository keeps one CI and one review standard; per-package modules keep
your dependency to what you asked for.

Separate repositories per package were considered and rejected: these packages
change together far more than apart, and N repositories buys an independence
nobody asked for at the cost of N pipelines.

## Packages

| Package | What it gives an agent | Risk class | State |
|---|---|---|---|
| `taxonomy` | The shared vocabulary: `internet` and `generated-artefacts` compartments; `research` and `documents` tool sets | none | planned, phase 1 |
| `sanitize` | Normalises untrusted content before it reaches a model: control characters, injection sentinels, length caps, an untrusted marker | none | planned, phase 1 |
| `web` | `fetch_page`: a governed page fetcher with a host allowlist and blocklist, an SSRF floor, redirect re-checks, text extraction | prompt injection, exfiltration | planned, phase 1 |
| `web` | `search_web`: a search client whose results are filtered by the same host policy | prompt injection | planned, phase 2 |
| `artefacts` | An object store for generated files with `get`, `list` and `delete`, owner-only until the authorization graph lands | data exposure | planned, phase 2 |
| `documents` | `create_html_document`, `create_workbook` (Excel) | data exposure | planned, phase 2 |
| `documents` | `create_presentation` (PowerPoint), `create_document` (Word), `extract_document` | data exposure, sandbox | planned, phase 3 |
| `payments`, `identity`, `compliance` | Bank-domain packages | approval-gated | intended, not scheduled |

The first three were chosen from an assessment of what
[Hermes Agent](https://github.com/NousResearch/hermes-agent) ships and what a
regulated business can safely borrow. The assessment is in the design record
(`docs/research/20260929_hermes-agent-tools-port-assessment.md` in the
`garm-ai/docs` repository).

## Roadmap

Effort: S is up to two days, M up to two weeks, L longer. Each step is a
release of one package.

### Phase 1: read the web safely

| Step | Package | Delivers | Effort | Depends on |
|---|---|---|---|---|
| 1 | `taxonomy` | The two compartments and two tool sets every later proto lints against | S | garm v0.14.2 |
| 2 | `sanitize` | The package every fetcher and generator reuses | S | none |
| 3 | `web` | `fetch_page` with allow and block lists, SSRF floor, redirect re-checks | M | 1, 2, an egress policy at the deployment |

### Phase 2: research and produce

| Step | Package | Delivers | Effort | Depends on |
|---|---|---|---|---|
| 4 | `web` | `search_web`, results filtered by the host policy | S | 1, a search vendor decision |
| 5 | `artefacts` | The store and its three tools; a file is an `ArtifactRef`, never bytes | M | none |
| 6 | `documents` | `create_html_document`, the first generator, proving the output contract | S | 2, 5 |
| 7 | `documents` | `create_workbook` on excelize | M | 5 |

### Phase 3: office formats and the loop closed

| Step | Package | Delivers | Effort | Depends on |
|---|---|---|---|---|
| 8 | `documents` | `create_presentation`, `create_document`: Hermes's MIT Python scripts in a no-network container behind a Go service | M + M | 5, a sandbox |
| 9 | `documents` | `extract_document` for what was produced or uploaded | M | 8 |
| 10 | garm contract | `effects.untrusted_output`: after a run reads untrusted content, an external write escalates to a human grant | M | 3 |
| 11 | agentd | `ask_human`: a clarifying question with a ledger row | S | the inbox |
| 12 | [`agents`](https://github.com/garm-ai/agents) | A researcher agent manifest as the first end-to-end use | S | 3, 4, 6 or 7, 11 |

### Later

Delivery tools with grants (`send_message`), image analysis, a classification
stamp and graph-backed sharing of artefacts, a governed memory store, a
browser, terminal and code execution. Each only with a named use case and the
sandbox and egress controls in place.

Phases 1 and 2 are about three engineer-weeks. Phase 3 adds two more. Step 10
is the one change to the `garm.tool.v1` contract and gets its own short design
first.

## Adopting a package

1. Vendor the annotations once with `garm init` (garm v0.14.2 or later).
2. Add the package's proto directory to your `buf.yaml` as a dependency and
   the module to your `go.mod`, for example `github.com/garm-ai/tools/web@v0.1.0`.
3. Run `garm lint` and `garm catalogue build`. The package's taxonomy merges
   with yours; an identical declaration merges silently, a different one fails
   the build naming both sources. Adopt one definition or rename yours.
4. Publish the catalogue with `garm catalogue publish`, run the package's
   service next to your own, and let garmd mount it.
5. In an agent manifest, allowlist the tools you want and add guards, for
   example `args.url.startsWith("https://docs.example.com/")`.

A package brings its own taxonomy, and that coupling is opt-in by the act of
adoption. Nothing here is imposed on a catalogue that does not ask for it.

## What a governed tool looks like

Every tool is a proto method with the full annotation block. Sketch of
`fetch_page`, from the assessment; the shipped proto is the source of truth.

```proto
rpc FetchPage(FetchPageRequest) returns (FetchPageResponse) {
  option (garm.tool.v1.tool) = {
    name: "fetch_page"
    title: "Fetch a web page"
    description: "Fetch one page from an allowed host and return its text."
    verb: VERB_READ
    min_clearance: CLEARANCE_INTERNAL
    compartments: ["internet"]
    sets: ["research"]
    effects: { idempotent: true }
    guidance: {
      when_to_use: "The page is on an allowed host and you need its text."
      when_not_to_use: "The answer is already in the conversation."
      on_error: "Say the page could not be fetched and stop; do not guess."
    }
  };
}
```

The allow and block lists are the service's own configuration, fail closed: no
allowlist means nothing is fetched. An agent narrows further with a guard on
the URL. Field policy is not the instrument for this, since it governs who may
read a field, not what a value may be.

## Safety model

- **Untrusted in, marked out.** Everything fetched from the internet or read
  from a document is normalised by `sanitize` and returned as an observation
  the runner treats as data, not instructions. The next tool call still passes
  every guard.
- **No raw bytes across the hop.** Generators write to the artefact store and
  return a reference. Reading a file back is its own governed tool with its own
  policy.
- **Egress is a deployment decision.** The fetcher's allowlist, the SSRF floor
  and the container's network policy all say no by default.
- **Approval where money or messages move.** Anything that leaves the
  perimeter is approval-gated with material fields, the same way a payment is.

## Building, testing, releasing

- Go 1.26, `mise` for the toolchain, `buf` for protos.
- `mise run ci` in a package runs lint, vet, the unit tests, `garm lint`,
  `garm catalogue build` and a mount check against a pinned garmd.
- Tests run over an embedded NATS server and send the `Garm-Invocation` header
  the way garmd does; no test talks to the internet.
- Releases are per-package tags, for example `web/v0.1.0`. A package's README
  states what changed and what its annotations mean.

## How this differs from examples

An [example](https://github.com/garm-ai/examples) is a template you fork and
then own. A package here is a product you consume and we maintain. Forking an
example means never hearing from us again; adopting a package means a version
bump when we fix something.

## Contributing

Open an issue naming the tool, the use case and the risk class before writing
code. A tool is accepted when its annotations are complete, its refusals are
tested, its README explains every choice, and it needs no licence we cannot
carry (permissive only; see the assessment on PDF libraries).

## Licence

MIT. See [LICENSE](LICENSE).
