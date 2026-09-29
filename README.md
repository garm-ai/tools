# garm tools

**Tool packages you can adopt instead of writing.** Each package is a set of
governed tools declared in proto, annotated with the `garm.tool.v1` contract,
served as NATS micro services behind [garmd](https://github.com/garm-ai/garmd),
and maintained here. You take the packages you want and nothing else.

Status, 29 September 2026: phase 1 (`taxonomy`, `sanitize`, `fetch_page`) is
in implementation. `taxonomy/v0.1.1` and `sanitize/v0.1.1` are tagged; `web` is not released yet.
`KNOWN-GAPS.md` says what is built; the roadmap below says what arrives when.

## Contents

- [What a package is](#what-a-package-is)
- [What is here](#what-is-here)
- [Roadmap](#roadmap)
- [Adopting a package](#adopting-a-package)
- [A package brings its own taxonomy](#a-package-brings-its-own-taxonomy)
- [What a governed tool looks like](#what-a-governed-tool-looks-like)
- [Safety model](#safety-model)
- [Working here](#working-here)
- [How this differs from examples](#how-this-differs-from-examples)
- [Contributing](#contributing)
- [Licence](#licence)

## What a package is

One directory, one Go module, one release train. A package holds:

- the proto files that declare its tools and the annotations garmd enforces;
- the service that serves them, on [tool-go](https://github.com/garm-ai/tool-go);
- the compartments and tool sets it needs, declared once in `taxonomy`;
- tests over a real broker, sending the invocation context garmd sends;
- a README that explains every annotation choice in plain words.

One repository, **per-package adoption**. Each package is its own Go module,
tagged `<package>/vX.Y.Z`. You depend on `github.com/garm-ai/tools/web@v0.1.0` (the git tag is `web/v0.1.0`),
not on this repository, and you inherit only what that package needs: `web`
brings `taxonomy` (the vocabulary its tool names) and `sanitize` (the wrapper
its response uses), both tagged packages of this repository, and nothing
else. One CI and one release train here; no inherited tools there.

Separate repositories per package were considered and rejected: these packages
change together far more often than they change apart, and N repositories
buys an independence nobody asked for at the cost of N pipelines.

## What is here

| Package | Module | Tag | What it declares |
|---|---|---|---|
| `taxonomy/` | `github.com/garm-ai/tools/taxonomy` | `taxonomy/v0.1.1` | The compartments and tool sets the packages here share: `internet`, `generated-artefacts`; `research`, `documents` |
| `sanitize/` | `github.com/garm-ai/tools/sanitize` | `sanitize/v0.1.1` | Cleaning and wrapping text an attacker may have written, before a model reads it |
| `web/` | `github.com/garm-ai/tools/web` | in progress | `web.v1.fetch_page`: one public https page in, its readable text out, behind an allowlist and an SSRF floor |

`payments/`, `identity/` and `compliance/` are the packages this repository
was created for and are not yet seeded; the three above are the first
release train because a research agent needs them first.

The full set, planned and intended:

| Package | What it gives an agent | Risk class | State |
|---|---|---|---|
| `taxonomy` | The shared vocabulary: `internet` and `generated-artefacts` compartments; `research` and `documents` tool sets | none | phase 1, tagged `taxonomy/v0.1.1` |
| `sanitize` | Normalises untrusted content before it reaches a model: control characters, injection sentinels, length caps, an untrusted marker | none | phase 1, tagged `sanitize/v0.1.1` |
| `web` | `fetch_page`: a governed page fetcher with a host allowlist and blocklist, an SSRF floor, redirect re-checks, text extraction | prompt injection, exfiltration | phase 1, in progress |
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

Two steps, because garm builds a catalogue from one proto tree and has no
proto-dependency mechanism beyond buf:

1. `go get github.com/garm-ai/tools/web@v0.1.0` — the generated messages
   and the `ServeWebService` binding your `main` registers on a `garmtool`
   service.
2. Copy the package's proto tree, and the taxonomy's, into your own:
   `cp -R "$(go env GOMODCACHE)/github.com/garm-ai/tools/web@v0.1.0/proto/." proto/`
   and the same for `taxonomy@v0.1.0`. `garm lint` and `garm catalogue build`
   then see the declarations. (The module cache is the same bytes your build
   links, so the proto and the binding cannot disagree.)

This repository's own CI does exactly that once a package exists: `mise run
assemble` copies the packages' trees into `build/proto`, and lint, catalogue
and mount check run over it.

Then, as with any tool of your own: vendor the annotations once with
`garm init` (garm v0.14.2 or later), publish the catalogue with
`garm catalogue publish`, run the package's service next to your own, let
garmd mount it, and in an agent manifest allowlist the tools you want and add
guards, for example `args.url.startsWith("https://docs.example.com/")`.

## A package brings its own taxonomy

`taxonomy/` declares the compartments and tool sets the tools here require.
Adopting a package means adopting that vocabulary, and that coupling is
**opt-in, by the act of adoption**. Nothing here is imposed on a catalogue
that does not ask for it.

If a package and your own protos both declare a compartment, identical
declarations merge and any difference fails your catalogue build, naming both
sources (lint rule L29). Adopt one definition or rename yours; nothing is
silently resolved.

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

## Working here

Go 1.26, `mise` for the toolchain, `buf` for protos.

```
mise install          the toolchain
mise run ci           what CI runs: vendor-check, buf-lint, lint-taxonomy, lint, build, test,
                      tidy-check, acceptance, gen-check
                      plus lint-web, catalogue-web, check-web once those exist
mise run gen          regenerate the committed Go from the protos
```

Each module is tidied on its own: `(cd web && GOWORK=off go mod tidy)`. A
`go.work` at the root joins the modules for `mise run test` (which runs
`go test github.com/garm-ai/tools/...`); `mise run acceptance` builds and
tests each one with `GOWORK=off`, which is the README's promise made testable.
No `replace` directive is ever committed.

Tests run over an embedded NATS server and send the `Garm-Invocation` header
the way garmd does; no test talks to the internet. Releases are per-package
tags, for example `web/v0.1.0`. A package's README states what changed and
what its annotations mean.

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
