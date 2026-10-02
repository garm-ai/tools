# garm tools

**Tool packages you can adopt instead of writing.** Each package is a set of
governed tools declared in proto, annotated with the `garm.tool.v1` contract,
served as NATS micro services behind [garmd](https://github.com/garm-ai/garmd),
and maintained here. You take the packages you want and nothing else.

Status, 29 September 2026: phase 1 (`taxonomy`, `sanitize`, `fetch_page`) is
tagged: `taxonomy/v0.2.0`, `sanitize/v0.1.1`, `web/v0.2.0`. The first tool of
phase 2 is tagged too: `search/v0.1.0` (`search_web`). `KNOWN-GAPS.md` says
what is built and what is deliberately not — an **S** in the port assessment
is an estimate of effort, not a claim of completeness — and the roadmap below
says what arrives when.

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
tagged `<package>/vX.Y.Z`. You depend on `github.com/garm-ai/tools/web@v0.2.0` (the git tag is `web/v0.2.0`),
not on this repository, and you inherit only what that package needs: `web`
brings `taxonomy` (the vocabulary its tool names) and `sanitize` (the wrapper
its response uses), both tagged packages of this repository, and no other
package of this repository. Its `go.mod` also names `nats-server`, which only
its end-to-end test uses: the entry joins your module graph (`go mod tidy`
keeps every build tag's imports), and your binary never links it. One CI and
one release train here; no inherited tools there.

Separate repositories per package were considered and rejected: these packages
change together far more often than they change apart, and N repositories
buys an independence nobody asked for at the cost of N pipelines.

## What is here

| Package | Module | Tag | What it declares |
|---|---|---|---|
| `taxonomy/` | `github.com/garm-ai/tools/taxonomy` | `taxonomy/v0.2.0` | The compartments and tool sets the packages here share: `internet`, `generated-artefacts`; `research`, `documents` |
| `sanitize/` | `github.com/garm-ai/tools/sanitize` | `sanitize/v0.1.1` | Cleaning and wrapping text an attacker may have written, before a model reads it |
| `search/` | `github.com/garm-ai/tools/search` | `search/v0.1.0` | `search.v1.search_web`: one query in, links and excerpts out, behind the same host policy and a two-sided floor |
| `web/` | `github.com/garm-ai/tools/web` | `web/v0.2.0` | `web.v1.fetch_page`: one public https page in, its readable text out, behind an allowlist and an SSRF floor |

`payments/`, `identity/` and `compliance/` are the packages this repository
was created for and are not yet seeded; the four above are the first
release train because a research agent needs them first.

`search` is its own module rather than a second tool inside `web`, which the
roadmap originally assumed. A deployment that wants search and not fetching
should not mount the fetcher's allowlist; the two are on different contract
versions today (`web` on contracts v0.2.0, `search` on v0.3.0) and one module
cannot be on two; and a binary linking both protos would register a tool it
does not serve.

The full set, planned and intended:

| Package | What it gives an agent | Risk class | State |
|---|---|---|---|
| `taxonomy` | The shared vocabulary: `internet` and `generated-artefacts` compartments; `research` and `documents` tool sets | none | phase 1, tagged `taxonomy/v0.2.0` |
| `sanitize` | Normalises untrusted content before it reaches a model: control characters, injection sentinels, length caps, an untrusted marker | none | phase 1, tagged `sanitize/v0.1.1` |
| `web` | `fetch_page`: a governed page fetcher with a host allowlist and blocklist, an SSRF floor, redirect re-checks, text extraction | prompt injection, exfiltration | phase 1, tagged `web/v0.2.0` |
| `search` | `search_web`: a search client whose results are filtered by the same host policy | prompt injection | phase 2, tagged `search/v0.1.0` |
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
| 1 | `taxonomy` | The two compartments and two tool sets every later proto lints against | S | contracts v0.2.0 |
| 2 | `sanitize` | The package every fetcher and generator reuses | S | none |
| 3 | `web` | `fetch_page` with allow and block lists, SSRF floor, redirect re-checks | M | 1, 2, an egress policy at the deployment |

### Phase 2: research and produce

| Step | Package | Delivers | Effort | Depends on |
|---|---|---|---|---|
| 4 | `search` | `search_web`, results filtered by the host policy | S | 1, 2, a search vendor decision (Brave) |
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

Two steps, because garm builds a catalogue from a `catalogue.yaml` manifest
and a `module:` entry is how that manifest names a package it does not own:

1. `go get github.com/garm-ai/tools/web@v0.2.0` — the generated messages,
   the `ServeWebService` binding your `main` registers on a `garmtool`
   service, and the `require` your own `go.mod` now carries. That `require`
   is not incidental: `garm catalogue build` resolves a module entry's
   version with `go list -m`, run in the directory your `catalogue.yaml`
   lives in, and refuses outright when that directory has no go.mod — a
   module entry names WHAT to include and go.mod says WHICH VERSION, so a
   tree with no module graph has nothing to pin the descriptors to.
2. Add a `module:` entry for `web` and one for its own `taxonomy`
   requirement to your `catalogue.yaml`, beside that go.mod:

   ```yaml
   include:
     - path: proto                       # your own declarations, if any
     - module: github.com/garm-ai/tools/web
       packages: [web.v1]
     - module: github.com/garm-ai/tools/taxonomy
       packages: [tools.taxonomy.v1]
   ```

   No proto tree to copy and nothing to keep in sync: the version comes from
   your own `go.mod`, the bytes come from the module cache your build
   already links, and `garm lint` and `garm catalogue build` see the
   declarations the moment the entry is there. `garm catalogue init` writes
   a first `catalogue.yaml` for a tree that does not have one yet.

For `search` the same two steps with `github.com/garm-ai/tools/search@v0.1.0`
and `packages: [search.v1]` in place of `web`'s, plus one file it does not
share: a search API needs a credential, and `searchd` takes it as
`--api-key-file` (or `SEARCHD_API_KEY_FILE`) — a path, never a value, so the
key is not in a process list. `search/README.md` says why.

This repository's own CI does both: `catalogue.yaml` at the repository root
lists `taxonomy/proto`, `search/proto` and `web/proto` as three `path:`
entries, and `garm lint`, `garm catalogue build` and `garmd check` run over
it (`lint-web` over the whole manifest, `catalogue-search`/`check-search`,
`catalogue-web`/`check-web`); `mise run adopt-check` runs the two steps above
exactly as written, for `web`, in a temporary directory with its own go.mod,
so the commands above cannot regress. There is no `adopt-check` for `search`
yet, and there cannot be one until `search/v0.1.0` is on the proxy;
`KNOWN-GAPS.md` records it.

Then, as with any tool of your own: vendor the annotations once with
`garm init`, publish the catalogue with `garm catalogue publish`, run the
package's service next to your own, let garmd mount it, and in an agent
manifest allowlist the tools you want and add guards, for example
`args.url.startsWith("https://docs.example.com/")`.

Three version floors go with that, and all three come from the split of the
CLI from the contract:

- **garm v0.19.0 or later**, CLI and `protoc-gen-garm-go` from the same
  release. v0.19.0 qualifies every generated card helper by its service
  (`Default<Service><Card>`, `<Service><Card>From`); the bare names it emitted
  before collide when one proto package holds two single-tool services, and a
  tree that regenerates against it renames those identifiers. v0.18.0 renamed the Go import paths — the annotations and wire
  types are `github.com/garm-ai/contracts` now, and the `contracts/` segment
  that used to sit inside `github.com/garm-ai/garm` is gone — so a tree that
  upgrades one and not the other generates imports of packages that no longer
  exist. `garm init` writes four annotation files now (`tool`, `agent`,
  `card`, `meta`); this repository vendors the two its protos import.
- **garm with `catalogue.yaml` support** — a `module:` entry is how the
  manifest names an adopted package, and the version it resolves to comes
  from your own `go.mod` rather than from a copied proto tree. This
  repository pins v0.27.0, the newest tag published when it migrated off
  `--proto`.
- **garmd v0.3.0 or later.** `protoc-gen-garm-go` v0.18.1 synthesises an
  input card and a result card beside every tool, so `web`'s catalogue
  declares three tools where it declared one. A daemon that predates
  `garm.card.v1` reads a card's own message as an undeclared tool and refuses
  the whole mount. The annotation schema version is unchanged at `v1`, so
  nothing in the catalogue's compatibility check warns about this; `mise run
  check-web` is what catches it here.
- **One contract module per binary.** `github.com/garm-ai/garm/contracts/...`
  and `github.com/garm-ai/contracts/...` register the same descriptor file
  paths. A service that links both compiles and then dies at startup in
  `protoregistry`. Move your own imports, this repository's packages and
  tool-go in one change; `tools` packages before `taxonomy/v0.2.0` and
  `web/v0.2.0` are on the old path.

`garm lint` still warns `O1` that `web.v1.WebService` names no
`(garm.meta.v1.owner)` — a warning today and an error in a later release.
`KNOWN-GAPS.md` records it. `search.v1.SearchService` does name one
(`team: "garm-tools"`, `contact: "github.com/garm-ai/tools/issues"`), because
a public package naming its maintainers is a better answer than no answer:
`Owner.team` is the unit a ledger row is charged to, and an adopter who wants
its own team on the row edits that line in the proto it vendors, which it has
to copy anyway. `web` is expected to follow.

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
  and the container's network policy all say no by default. The searcher
  applies the same host policy to the links it *returns*, so an agent is never
  shown an address it could not fetch — and drops what fails rather than
  refusing the call, because one poisoned row in an index's answer must not
  become a way to deny the tool to everyone.
- **A credential is a file.** Anything here that needs one takes a path
  (`--api-key-file`), never a value: a flag is in every `ps` on the host and
  an environment variable's value is inherited by every child. It is never
  logged, never returned and never named in an error.
- **Approval where money or messages move.** Anything that leaves the
  perimeter is approval-gated with material fields, the same way a payment is.

## Working here

Go 1.26, `mise` for the toolchain, `buf` for protos.

```
mise install          the toolchain
mise run ci           what CI runs: vendor-check, buf-lint, lint-taxonomy, lint-web,
                      check-search and check-web (each builds its catalogue first),
                      adopt-check, lint, build, test, tidy-check, acceptance, gen-check
mise run gen          regenerate the committed Go from the protos
```

Each module is tidied on its own: `(cd web && GOWORK=off go mod tidy)`. A
`go.work` at the root joins the modules for `mise run test` (which runs
`go test github.com/garm-ai/tools/...`); `mise run acceptance` builds and
tests each one with `GOWORK=off`, which is the README's promise made testable.
No `replace` directive is ever committed.

Tests run over an embedded NATS server and send the `Garm-Invocation` header
the way garmd does; no test talks to the internet — the fetcher's fake
internet and the searcher's fake index are local https servers behind a fake
resolver, on ephemeral ports. Releases are per-package tags, for example
`web/v0.2.0` and `search/v0.1.0`. A package's README states what changed and
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
