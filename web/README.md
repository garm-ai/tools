# web

**`web.v1.fetch_page`: one public https page in, its readable text out,
wrapped as untrusted.** The first tool of the Hermes port, and the model for
every untrusted-content tool that follows it.

Status: tagged `web/v0.1.0`. The contract lints, builds a catalogue and
mounts on a bare deployment; the policy file, the SSRF floor, the guarded
dialer, extraction, the fetcher and the handler are built and tested against
a local https server behind a fake resolver; `webd` serves `fetch_page` over
NATS, and an end-to-end test drives it over an embedded NATS server with the
`Garm-Invocation` header garmd sends. What is deliberately not built is in
`../KNOWN-GAPS.md`.

```
proto/web/v1/web.proto     the declaration: what the tool is, who may see it, what it returns
gen/web/v1/                 the messages and the binding (`ServeWebService`), committed
policy.example.yaml         the allow and block lists a deployment writes
cmd/webd                    the service: connect, load the policy, register, run, drain
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
`<<<end-untrusted-content>>>`.

Extraction is one pass over `golang.org/x/net/html`'s tokenizer, no DOM.
"Hidden" means the `hidden` attribute, `aria-hidden="true"`, or an inline
`style` that says `display:none`, `visibility:hidden`, `opacity:0` (or
`.0`), `font-size:0`, `color:transparent`, or `left`/`top`/`right`/
`bottom`/`text-indent` of three or more digits off the page. Property names
are anchored to the start of the style or the separator before them, so
`margin-left:-100px` and `background-color:transparent` hide nothing. A hidden element's whole subtree is dropped, by counting the nesting
of the tag that opened it; a self-closing non-void tag (`<div hidden/>`)
opens a subtree the way a browser opens one. Block elements start a line;
`<title>` is returned separately and never appears in the text. A
`text/plain` body (with or without parameters) passes through untouched.
What an external stylesheet or a class hides cannot be seen from the
markup and is not dropped (see `KNOWN-GAPS.md`). `notices` repeats the sanitiser's annotations
as data, over the content and the title together; the wrapper header
carries the content's notices only, because it describes the text it
encloses. `title` is cleaned like the content and cut at 200 characters. `content_sha256` is over `content` as returned. `policy_digest`
identifies the lists in force, so a ledger row joins to the exact policy
that permitted the fetch.

## What is refused, and how

Every refusal is a `toolbind.CodedError`; the code is what the chain
publishes and the message is what the model reads. The checks run in order
of cost, and nothing is resolved or dialled for a URL an earlier check
refused.

| Code | When | The message names |
|---|---|---|
| `400` | the URL does not parse or has no host; the call arrived with no invocation context | nothing |
| `403` | the scheme is not `https`; the URL carries credentials; the host is an IP literal, a local or metadata name, off the allowlist or on the blocklist; a host resolved to a non-public address; a redirect target failed any of these, had no host, could not be parsed, or was not `http`/`https`; more than `max_redirects` hops | the scheme, the host, or the matching rule; for a redirect, the target's **origin** only, quoted, capped at 256 characters, and only when its scheme is `http` or `https` |
| `404` | upstream answered 404 | nothing |
| `415` | the content type is not `text/html`, `application/xhtml+xml` or `text/plain` | the media type if it is well known (`application/pdf`, `image/png`, ...), its top level with a wildcard (`text/*`) if only that is, else `"unknown"`; never the header's own words |
| `502` | the host did not resolve, the connection failed, the body could not be read, or upstream answered any other 3xx/4xx/5xx (a 3xx without a `Location` is not a page) | the host, or the status code |
| `504` | the policy's `timeout` or the invocation's deadline passed, or the invocation was cancelled | which clock ran out |

A refusal message carries the host at most, never the path or the query,
and never a byte of what upstream sent. The log line carries the tenant,
subject and call id from `Garm-Invocation`, the host the caller asked for,
and on a refusal the code alone: a redirect target is the page's choice
and does not reach the log. A call that arrives with no invocation context
is refused `400` before anything is resolved; tool-go refuses it earlier,
and the service is safe without that.

## The client

The HTTP client is built once, around the policy, and is deliberately
plain. No proxy, ever: `Proxy` is nil, never `ProxyFromEnvironment`,
because a proxy carries the request past the floor. No cookie jar: a page
cannot set state that a later fetch carries back. HTTP/1.1 only: the
guarded dialer is the transport's `DialContext`, and one hop is one
connection, one dial, one address check. The `Referer` header Go sets on
every redirect hop is stripped before the hop is checked, so the caller's
URL (query included) is never sent to a host the page chose. TLS is
verified against the system roots. The `User-Agent` is the policy's
`user_agent`. Idle connections close after 30 seconds.

## Adopting it

`go get github.com/garm-ai/tools/web@v0.1.0` (the git tag is `web/v0.1.0`),
then copy `proto/` and the taxonomy's `proto/` into your tree (see the
repository README). Your `main` registers
`webv1.ServeWebService(svc, web.NewService(web.NewFetcher(policy)))` on a
`garmtool` service, or you run `cmd/webd` as is.

## The policy file

`webd --policy policy.yaml`. See `policy.example.yaml` for every key. Three
properties a reviewer should hold it to:

- **Fail closed.** No `allow` entries, an unknown key (`alow:`), malformed
  YAML, a cap out of range: the service does not start. There is no
  blocklist-only mode. Hermes's `check_website_access` fails open on a
  malformed policy so a typo cannot break every web tool; here a typo breaks
  every fetch, at boot, where the operator is present.
- **Block wins.** `block` is checked before `allow`, and a refusal names the
  rule that matched — which the tool's `on_error` guidance promises the
  model.
- **Digest.** `policy_digest` on every response is sha256 over the sorted
  allow and block entries, so a ledger row joins to the exact lists that
  permitted a fetch. Caps do not enter the digest.

A policy is loaded once. Reloading on a signal is a known gap.

## Running it

```bash
mise run gen-web          # messages and the binding
mise run lint-web         # garm lint over the assembled tree
mise run catalogue-web    # build/web.binpb
mise run check-web        # garmd check: mounts on a bare deployment
go run ./web/cmd/webd --policy web/policy.example.yaml   # against a local NATS
```

`webd` connects, loads the policy (and stops if it cannot), registers
`fetch_page`, serves until SIGTERM, then drains. `--nats` (default
`nats://127.0.0.1:4222`) names the broker; `--concurrency` (default 8)
bounds fetches in flight; the invocation's deadline, when garmd sends one,
bounds each call on top of the policy `timeout`. The version it advertises
is the module version the toolchain stamped (`v0.1.0` from a build of the
tag), or `0.0.0-dev` from an untagged tree.

## What one call looks like

1. `Garm-Invocation` is decoded by tool-go; a request without one is `400`
   and never reaches the handler.
2. The URL is parsed; scheme, credentials, the host floor, the block list
   and the allow list are checked, in that order, before DNS.
3. The dialer resolves the name itself, refuses unless every address is
   public, and connects to the vetted address. Every redirect hop is
   re-checked as in 2 and dials as in 3; more than `max_redirects` is
   refused.
4. The body is read up to `max_body_bytes` (`truncated` when more);
   `text/html`, `application/xhtml+xml` and `text/plain` only.
5. Text is extracted, cleaned, capped at `char_limit`, wrapped, hashed.
6. One log line: `tool=fetch_page tenant=... subject=... call_id=... host=...
   status=... bytes=... truncated=... duration_ms=...`, or `fetch_page refused
   ... code=403`. Nothing the page sent is on it.

Refusals are coded errors: `403` for every policy and floor refusal
(redirects included; the message names the rule or the reason), `400` for a
URL that cannot be parsed or a call with no invocation context, `404` for an
upstream 404, `415` for a content type this tool does not read, `502` for a
DNS or upstream failure, `504` for a timeout. The message never carries
upstream body text.

## Tests

`go test ./...` runs everything against a local https server behind a fake
resolver and a fake dial, and an embedded NATS server for the end-to-end
test (`e2e_test.go`). Every refusal class in the design has a test; see
`service_test.go`.
