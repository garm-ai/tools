# tools — governed tool packages

Tool packages a consumer adopts by tag. One Go module per package; the
design record is in `../spec` (`docs/superpowers/plans/2026-09-29-tools-phase-1-plan.md`
and `docs/research/20260929_hermes-agent-tools-port-assessment.md` in `../docs`).

## The invariants

- **Nothing here may depend on `garmd`.** A tool service must not be able to
  reach the enforcing code. `mise run acceptance` asserts it per module.
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
