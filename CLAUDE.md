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
  reads it from `taxonomy/go.mod`. A constant there only checks the tree
  against itself, which is how the vendored copies once sat five releases
  behind the module they compiled against without a red run.
- **No policy code in a tool.** Every limit a service enforces is a safety
  limit (hosts, addresses, bytes, seconds), never an authorisation one. The
  daemon decided who may call before the request arrived; the service returns
  everything it knows and the daemon redacts.
- **Nothing the page sent reaches a log line or an error message.** A refused
  redirect target's origin is the one exception, and it is the same
  disclosure `url_origin` permits; it is echoed only when its host is a DNS
  name or an IP literal. Of a response, `final_url` is the field a page can
  choose after a redirect: origin and path only, cleaned and capped like the
  title, never the query.
- **Fail closed.** A policy that cannot be read stops the service at boot.

## Working here

```
mise install     the toolchain
mise run ci      everything CI runs
```

Documentation is updated in the same commit as the fact it states: this
file, `README.md`, each package's `README.md`, `KNOWN-GAPS.md`.
