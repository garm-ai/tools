# web

**`web.v1.fetch_page`: one public https page in, its readable text out,
wrapped as untrusted.** The first tool of the Hermes port, and the model for
every untrusted-content tool that follows it.

Status: the contract is declared, lints, builds a catalogue and mounts; the
service behind it is not built yet and the module is not tagged. `web/v0.1.0`
arrives with `webd`.

```
proto/web/v1/web.proto     the declaration: what the tool is, who may see it, what it returns
gen/web/v1/                 the messages and the binding (`ServeWebService`), committed
policy.example.yaml         the allow and block lists a deployment writes (Task 5)
cmd/webd                    the service: connect, load the policy, register, run, drain (Task 9)
```

## The annotation block, line by line

```proto
name: "fetch_page"                       the short name a manifest pins as web.v1.fetch_page
verb: VERB_READ                          reading changes nothing here
min_clearance: CLEARANCE_INTERNAL        a public caller has no business reading the web through this tenant's allowlist
compartments: ["internet"]               need-to-know, not seniority: RESTRICTED without `internet` sees no such tool
sets: ["research"]                       a session scoped to `research` sees it; sets narrow, never widen
effects: { idempotent: true              a retry fetches the same page
           reversibility: REVERSIBILITY_FULL   nothing to undo
           external: true }              the request leaves the tenant, to a host somebody else runs
audit: { level: LEVEL_LEDGER }           one ledger row per call; no request or response recorded
guidance: { when_to_use, when_not_to_use, on_error }   prompt surface, reviewed like code
```

No `approval`: nothing to undo, so nothing for a human to agree to. Lint rule
L16 does not fire because the tool is reversible. `garmd check` mounts it on a
deployment with no grant verifier and no audit sink, and `mise run check-web`
asserts that it does.

**Field policies.** The request is `PUBLIC` with `mask` on deny (requests are
checked for write clearance and never redacted on the way in; `mask` rather
than `omit` because `omit` on a presence-less scalar makes "redacted" and
"empty" the same value — L6). `char_limit` carries an explicit `omit` policy
because `mask` applies to strings only (L5). The response is `INTERNAL` with
`omit` on deny, every scalar `optional` (L6), and `final_url` shows its
**origin** to a caller who may not read the path (`url_origin`) — what a
redirect check needs and nothing more.

## Where the allow and block lists live, and where they do not

The lists are **service configuration** (`policy.example.yaml`), not field
policy and not a guard. Field policy governs who may read or write a field,
not what value it may carry. A guard in an agent's manifest sees only the
request the model wrote — never a `Location` header, never the resolved
address — so it can narrow (`args.url.startsWith('https://www.gov.uk/')`)
but cannot be the policy of record. The service holds the policy of record;
the guard narrows per agent; the two compose.

```proto
tools: [
  { fqn: "web.v1.fetch_page"
    guard: "args.url.startsWith('https://www.bankofengland.co.uk/') || "
           "args.url.startsWith('https://www.gov.uk/')" }
]
```

A blocklist in a guard (`!args.url.contains('admin.')`) is weak and should
not be attempted. `!args.url.contains('?')` is a reasonable default for a
research agent: the URL is the one channel through which a model can encode
what it was shown.

## What the response is

`content` is the page's text after extraction (`script`, `style`,
`template`, `noscript`, `iframe`, `object`, `svg`, comments and hidden
elements dropped) and after `sanitize.Clean` (NFC, invisible characters
removed, whitespace collapsed, sentinels neutralised, capped at
`char_limit`), between `<<<untrusted-content source="…">>>` and
`<<<end-untrusted-content>>>`. `notices` repeats the sanitiser's annotations
as data. `content_sha256` is over `content` as returned. `policy_digest`
identifies the lists in force, so a ledger row joins to the exact policy
that permitted the fetch.

## Adopting it

`go get github.com/garm-ai/tools/web@v0.1.0` (the git tag is `web/v0.1.0`),
then copy `proto/` and the taxonomy's `proto/` into your tree (see the
repository README). Your `main` registers
`webv1.ServeWebService(svc, web.NewService(web.NewFetcher(policy)))` on a
`garmtool` service, or you run `cmd/webd` as is.
