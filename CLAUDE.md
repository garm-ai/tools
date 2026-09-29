# tools — governed tool packages

Tool packages a consumer adopts by tag. One Go module per package; the
design record is in `../spec` (`docs/superpowers/plans/2026-09-29-tools-phase-1-plan.md`
and `docs/research/20260929_hermes-agent-tools-port-assessment.md` in `../docs`).

## The invariants

- **Nothing here may depend on `garmd`.** A tool service must not be able to
  reach the enforcing code. `mise run acceptance` asserts it per module.
- **One contract module per binary.** The annotations and wire types are
  `github.com/garm-ai/contracts`; `github.com/garm-ai/garm` is the CLI and the
  generator, and no module here may require it. The two register the same
  descriptor file paths, so a binary linking both compiles and then dies in
  `protoregistry` at init. That failure is invisible to a test suite whose
  binary links only one of them — `mise run check-web` and running `webd` are
  what see it. When a module moves, its contract imports, its `tool-go` and
  its committed generated code move in the same commit.
- **The vendored annotations are checked against the version go.mod requires**,
  not against a version written down in `mise.toml`. `mise run vendor-check`
  reads it from every module that requires the contract and compares
  `third_party` against each. A constant there only checks the tree against
  itself, which is how the vendored copies once sat five releases behind the
  module they compiled against without a red run. Modules may be on different
  contract releases (`search` is on v0.3.0, `taxonomy` and `web` on v0.2.0)
  only for as long as those releases publish byte-identical protos; the check
  is what makes that a fact rather than a hope.
- **No policy code in a tool.** Every limit a service enforces is a safety
  limit (hosts, addresses, bytes, seconds), never an authorisation one. The
  daemon decided who may call before the request arrived; the service returns
  everything it knows and the daemon redacts.
- **A credential is a file path, never a value.** Anything needing one takes
  `--api-key-file` (with a `*_KEY_FILE` environment variable holding a path
  too), following agentd's `--provider-key-file`, which is the strictest
  pattern in this platform. A flag value is in `/proc` and in every `ps`; an
  environment variable's value is inherited by every child. The value reaches
  no log line, no URL, no error and no response, and a boot-time check refuses
  a file holding anything but a key.
- **Nothing the page sent reaches a log line or an error message.** A refused
  redirect target's origin is the one exception, and it is the same
  disclosure `url_origin` permits; it is echoed only when its host is a DNS
  name or an IP literal. Of a response, `final_url` is the field a page can
  choose after a redirect: origin and path only, cleaned and capped like the
  title, never the query.
- **Untrusted text is `tools/sanitize`'s job, once.** A page's text, a search
  snippet, an extracted document: one package cleans and wraps all of them. A
  second sanitiser is a second set of bugs. Each passage is wrapped with its
  own origin, never a batch with one.
- **A result a tool hands back is not refusable content.** Where a service
  repeats links or rows somebody outside the tenant chose, a bad one is
  dropped and counted, not turned into a refusal — otherwise poisoning one row
  denies the tool to everyone. Where a service acts on the caller's own input,
  a bad one is a coded refusal, because garmd maps a code and a bare error
  arrives as an unclassified 500.
- **Fail closed.** A policy that cannot be read stops the service at boot.

## Working here

```
mise install     the toolchain
mise run ci      everything CI runs
```

The toolchain is pinned in `mise.toml`: `garm` and `protoc-gen-garm-go` from
the same release (v0.19.0 — its card helpers are qualified by service), and
`garmd` as a floor rather than a preference.

Documentation is updated in the same commit as the fact it states: this
file, `README.md`, each package's `README.md`, `KNOWN-GAPS.md`. An effort
estimate in the port assessment is not a completeness claim; what a package
does not do belongs in `KNOWN-GAPS.md` in the commit that ships it.
