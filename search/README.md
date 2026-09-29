# search

**`search.v1.search_web`: one query in, a short list of links and excerpts
out, each excerpt wrapped as untrusted.** Rank 2 of the Hermes port, after
`fetch_page`, and cheap only because `fetch_page` already paid for the hard
parts: the sanitiser, the address floor, the host-policy shape.

Status: new, `search/v0.1.0`. The contract lints, builds a catalogue and
mounts on a bare deployment; the policy file, the egress and ingress halves
of the floor, the Brave client, the credential loader and the handler are
built and tested against a local https endpoint behind a fake resolver;
`searchd` serves `search_web` over NATS, and an end-to-end test drives it
over an embedded NATS server with the `Garm-Invocation` header garmd sends.
**An S in the port assessment is an estimate of effort, not a claim of
completeness** — what is deliberately not built is listed below and in
`../KNOWN-GAPS.md`.

```
proto/search/v1/search.proto   the declaration: what the tool is, who may see it, what it returns
gen/search/v1/                 the messages and the binding (`ServeSearchService`), committed
policy.example.yaml            the endpoint and the result host lists a deployment writes
cmd/searchd                    the service: connect, load the policy and the key, register, run, drain
```

## Why its own module, and not a second tool in `web`

One Go module per package is this repository's rule, and three things make
it the right one here rather than an accident of it. A deployment that
wants search and not fetching should not have to mount the fetcher's
allowlist. The two modules are on different contract versions today
(`web` on `github.com/garm-ai/contracts` v0.2.0, `search` on v0.3.0), and
one module cannot be on two. And a binary that linked both protos would
register two tools it serves one of.

It is a separate proto package (`search.v1`) as well as a separate module.
Before `garm` v0.19.0 that mattered for a second reason: the generated card
helpers were bare package-level names, so two single-tool services in one
proto package declared `DefaultInputCard` twice and the package did not
compile. v0.19.0 qualifies them by service, so the collision is gone — but
the packages stay apart because the deployments do.

## The annotation block, line by line

```proto
name: "search_web"                       the short name a manifest pins as search.v1.search_web
verb: VERB_READ                          searching changes nothing here
min_clearance: CLEARANCE_INTERNAL        a public caller has no business spending this tenant's search credential
compartments: ["internet"]               need-to-know, not seniority: RESTRICTED without `internet` sees no such tool
sets: ["research"]                       the same set as fetch_page; they are used together or not at all
effects: { idempotent: true              a retry runs the same query
           reversibility: REVERSIBILITY_FULL   nothing to undo
           external: true }              the query leaves the tenant, to a search company
audit: { level: LEVEL_LEDGER }           one ledger row per call; no query or results recorded
guidance: { when_to_use, when_not_to_use, on_error }   prompt surface, reviewed like code
```

And, unlike `web`, an owner:

```proto
option (garm.meta.v1.owner) = {
  team: "garm-tools"
  contact: "github.com/garm-ai/tools/issues"
};
```

`garm lint` warns `O1` on a service that names none, and that becomes an
error in a later release. `Owner.team` is the unit a ledger row is charged
to. This is a public package, so it names its maintainers rather than a
bank's internal team; an adopter who wants its own name on the row edits
that line in the copy of the proto it vendors, which it has to copy anyway
(see "Adopting it").

## Which search API, and why

**Brave Search.** One `GET https://api.search.brave.com/res/v1/web/search`
with `q` and `count`, the credential in an `X-Subscription-Token` header,
and `web.results[]` rows of `{title, url, description}` — which is exactly
the `Result{title, url, snippet}` the port assessment specifies, so nothing
is invented in the mapping.

The reason it is Brave and not Tavily, Perplexity or Exa is not price. A
row out of an index is text the page's own author wrote. With an LLM-backed
provider the model on the vendor's side chooses which URLs to return and
**writes the titles and descriptions itself** — Hermes's own documentation
warns about this — which points a second model's output straight at this
tenant's model with nothing in between. That is a strictly larger injection
surface for a tool whose entire output is untrusted text. Brave also takes
its credential in a header rather than a query parameter, so the key never
reaches a URL that a proxy, a referer or a redirect could carry.

`backend:` in the policy file names it, so a second adapter is a policy
change rather than a different binary. SearXNG (self-hosted, AGPL-3.0 over
HTTP, no vendor at all) is the obvious second one and is **not built**.

## The API key

`searchd --api-key-file /run/secrets/brave` or `SEARCHD_API_KEY_FILE`.
**A path, never a value**, and there is deliberately no flag or variable
that takes the key itself. This is the strictest pattern in this platform —
agentd takes its provider credential as `--provider-key-file` and its
signing key as `--sts-client-key-file` — and each half of it is load
bearing:

- a flag value is world-readable in `/proc/<pid>/cmdline` and in every `ps`
  on the host;
- an environment variable's value is inherited by every child process and
  dumped by most things that write a crash report;
- a file has an owner and a mode, which is the only part a deployment can
  actually enforce. `searchd` warns at boot when the file is readable by
  more than its owner. A warning and not a refusal, because a Kubernetes
  secret is mounted `0644` by default and refusing would make the strict
  thing the thing nobody deploys.

The key is read once at boot into the `Searcher` and goes nowhere else. It
is never put in a URL, never logged, never returned, and never named in an
error — every failure message says the *path*, which the operator typed.
An endpoint URL carrying credentials or a query string is refused at boot
for the same reason: a query string is where an API key gets pasted, and
from there into every proxy log in the path. `TestTheAPIKeyTravelsInAHeaderAndNowhereElse`
asserts the key is absent from the log, the query string and the response.

## The safety floor, and how it differs from the fetcher's

`web`'s floor guards one thing: a URL the caller chose, which the service
is about to connect to. This tool never connects to a URL a caller chose,
so its floor is a different shape — two halves, in opposite directions.

**Egress, to the one endpoint the operator configured — tighter than the
fetcher's.** https only, no credentials in the URL, no query string and no
fragment of the operator's own, the host floor by name before DNS, the
dialer resolving the name itself and refusing unless *every* address is
public (resolving twice is how a rebinding attack passes a check with one
answer and connects with another), and **no redirects at all**. `fetch_page`
follows up to five re-checked hops because a page legitimately moves. A
search API does not, and every request here carries the deployment's
credential, so a `Location` header is the endpoint asking for the key to be
sent somewhere else. It is a `502`, not a hop.

**Ingress, from the index — a surface the fetcher does not have at all.**
Nothing here connects to a result URL, so there is no SSRF in it. But every
result is a string somebody outside the tenant wrote, in a field a model
reads as an address, and a model shown a link tends to fetch it. So each
result URL must be https, carry no credentials, have a host DNS could
carry, and pass the same address floor and the same allow/block policy — so
an agent is never shown a link `fetch_page` would refuse to follow. Keep
the two allowlists the same, or make this one narrower.

A result that fails is **dropped, not refused**, and `dropped` in the
response says how many. One poisoned row in an index's answer is not a
reason to fail the call, and making it one would hand whoever put that row
there a way to deny search to everyone. A model told "three results" about
a query that found twelve can see the difference.

The address list itself is `web`'s, unchanged: RFC 1918, loopback,
link-local, CGNAT, multicast, reserved and documentation ranges, every IPv6
range that embeds or maps an IPv4 address, cloud metadata names and
addresses, `localhost`, `.local`, `.internal`, `.arpa`, Kubernetes service
names, and the WHATWG "ends in a number" rule so `2130706433`,
`0x7f000001`, `0177.0.0.1` and `127.1` are addresses rather than names. It
is not configurable and a deployment can only narrow what sits on top of
it. That it is a second copy of `web`'s is recorded in `../KNOWN-GAPS.md`.

## What is sanitised, and what is not

`tools/sanitize` does the work — the same package `fetch_page` uses, not a
second copy of it.

| Field | Treatment |
|---|---|
| `Result.snippet` | `Clean` then `Wrap`, with **this result's origin** as the wrapper's source |
| `Result.title` | `Clean`, capped at 200 runes, newlines folded to spaces, **not** wrapped |
| `Result.url` | origin and path only; `EscapedPath` first, then `Clean` |
| the query | **not** sanitised — it is the caller's own text, not the index's |
| the endpoint's JSON envelope | **not** sanitised — it is parsed, and a body that will not parse is a `502` whose message repeats none of it |

Each snippet is wrapped separately rather than the response as a whole,
because each result has a different author: one wrapper around the lot
would tell the model a single lie about where the text came from. The title
is a label beside a link rather than a passage, so it is cleaned and not
wrapped; anything the sanitiser noticed in it still joins the response's
`notices`.

The URL is cleaned too, because a zero-width character in a link is how two
addresses that render identically point at different places. But
`EscapedPath` runs *first*, so an invisible character inside a path is
percent-encoded rather than deleted: deleting one would hand the model an
address the index never returned, which is the worse failure, and
`%E2%80%8B` is visible, which was the point.

`notices` is advisory and never a refusal — `injection-phrase`,
`sentinels-neutralised`, `invisible-characters-removed`. A heuristic is not
a boundary. Hermes's own security statement is right that nothing inside
the agent process constitutes containment; garm's chain sits outside it for
that reason.

## Adopting it

`go get github.com/garm-ai/tools/search@v0.1.0` (the git tag is
`search/v0.1.0`), then copy `proto/` and the taxonomy's `proto/` into your
tree (the repository README has the commands: create the directory first
and `chmod -R u+w` after, because the module cache is read-only and `cp -R`
keeps its modes). Your `main` registers
`searchv1.ServeSearchService(svc, search.NewService(search.NewSearcher(policy, key)))`
on a `garmtool` service, or you run `cmd/searchd` as is.

A deployment needs **garmd v0.3.0 or later**: the generator synthesises an
input card and a result card beside the tool, and v0.2.x reads a card's own
message as an undeclared tool and refuses the whole mount.

## The policy file

`searchd --policy policy.yaml`. See `policy.example.yaml` for every key.
Four properties a reviewer should hold it to:

- **Fail closed.** No `backend`, no `allow` entries, an unknown key
  (`alow:`), malformed YAML, two documents, a cap out of range, an endpoint
  that is not https or carries a key: the service does not start. There is
  no blocklist-only mode. Hermes's `check_website_access` fails open on a
  malformed policy so a typo cannot break every web tool; here a typo stops
  the service at boot, where the operator is present.
- **Block wins.** `block` is checked before `allow`, and the rule that
  matched is named.
- **Rules that can never match are refused.** An `allow` entry the floor
  would drop anyway — an IP literal, `localhost`, a `.internal` name —
  fails at boot rather than sitting in a file an operator believes in.
- **Digest.** `policy_digest` on every response is sha256 over the backend,
  the endpoint and the sorted allow and block entries, so a ledger row joins
  to the exact configuration that permitted those links. Caps and timeouts
  do not enter it; the API key never goes near it.

A policy is loaded once. Reloading on a signal is a known gap.

## Running it

```bash
mise run gen-search          # messages and the binding
mise run lint-web            # garm lint over the assembled tree (taxonomy + search + web)
mise run catalogue-search    # build/search.binpb
mise run check-search        # garmd check: mounts on a bare deployment
# from the repository root, against a local NATS on a port of your own:
go run ./search/cmd/searchd --policy search/policy.example.yaml \
    --api-key-file /run/secrets/brave --nats nats://127.0.0.1:14222
go install github.com/garm-ai/tools/search/cmd/searchd@v0.1.0   # the deployment form
```

`searchd` connects, loads the policy and the key (and stops if it cannot
read either), registers `search_web` and its two card endpoints, serves
until SIGTERM, then drains. `--nats` (default `nats://127.0.0.1:4222`) names
the broker; the invocation's deadline, when garmd sends one, bounds each
call on top of the policy `timeout`. The version it advertises is the
module version the toolchain stamped: `v0.1.0` when installed from the tag,
and `0.0.0-dev` from any in-tree `go build` or `go run` — Go stamps only a
root module's tag, and `search` is a nested module.

**`--concurrency` (default 4) is service instances, not goroutines**, and
4 is chosen rather than inherited. Since tool-go v0.6.0 a handler runs
synchronously in the goroutine its subscription owns, so `WithConcurrency(n)`
registers `n` whole micro service instances in one queue group, each a
separate responder on `$SRV.INFO`, `$SRV.PING` and `$SRV.STATS`. garmd's
discovery collects INFO replies into a channel buffered at 64 and drops
silently past that — a budget shared with every service in the plane. That
is the same cost `webd` pays, and `webd` pays 8 of it because a page fetch
is a long wait: a handshake, up to five redirects, up to five megabytes,
a twenty-second cap. A search is one short JSON round trip with no
redirects and a ten-second cap, so each instance drains its queue several
times faster and four in flight is not the bottleneck four page loads was.
The real ceiling is the vendor's anyway: a Brave subscription is rated in
requests per second, and the entry plans sit at or below what four
instances can drive, so buying responders past that spends a shared budget
to produce `429`s. Beyond this, throughput is a deployment question — a
larger subscription first, then more `searchd` processes behind the same
queue group. A `--concurrency` of zero or less is refused at boot.

At startup the service logs what is actually in force, defaults included —
the policy line, then the runtime's own:

```
level=INFO msg="policy loaded" path=search/policy.example.yaml digest=… backend=brave
  endpoint=https://api.search.brave.com/res/v1/web/search allow=3 block=1
  max_results=20 max_snippet_chars=1000 timeout=10s user_agent=… api_key_file=/run/secrets/brave
level=INFO msg="serving tools" service=search version=0.1.0 tools=3 concurrency=4
  queue_groups=[search.v1.SearchService] identity=… contract_version=v0.3.0
```

## What one call looks like

1. `Garm-Invocation` is decoded by tool-go; a request without one is `400`
   and never reaches the handler, so an unattributed call spends none of
   the deployment's search quota.
2. The query is trimmed and bounded, and `limit` is clamped into the
   policy's `max_results` — the clamped number is what the endpoint is
   asked for, so nothing is paid for and thrown away.
3. One `GET` to the endpoint through the guarded dialer, with the
   credential in a header. No redirects; no proxy, ever.
4. The body is read up to `max_body_bytes` and parsed. Over the cap is a
   `502`, not a truncated parse: half a JSON document is not a document.
5. Every result URL goes through the address floor and the host policy;
   failures are dropped and counted.
6. Every surviving title and snippet goes through `sanitize`; the snippet is
   wrapped with its own origin; the results are hashed.
7. One log line: `tool=search_web tenant=… subject=… call_id=… limit=…
   returned=… dropped=… duration_ms=…`, or `search_web refused … code=502`.
   Neither the caller's query nor anything the index wrote is on it.

Refusals are coded errors, because garmd maps a coded refusal for the
caller and a bare one arrives as an unclassified 500: `400` for an empty or
oversized query and for a call with no invocation context, `403` for a
floor refusal on the endpoint, `429` when the vendor is rate limiting,
`502` for a DNS failure, a redirect, a rejected credential, an oversized or
unparseable answer, or any other upstream status, `504` for a timeout —
naming whether the clock that ran out was the policy's or the invocation's.
No message carries a byte the endpoint wrote.

## Tests

`go test ./...` runs everything against a local https endpoint behind a fake
resolver and a fake dial, and an embedded NATS server on an ephemeral port
for the end-to-end test (`e2e_test.go`). The three the brief names:
`floor_test.go` covers both halves of the floor including the endpoint
rules and the result rules; `service_test.go` covers the sanitising, the
per-result wrapping, the dropping and the credential; and every refusal
travels as a `toolbind.CodedError`, asserted over the wire in
`e2e_test.go`.

## Deliberately not built

- **One backend.** No SearXNG, no Tavily, no fallback between providers.
  `backend:` exists so the second one is a policy change.
- **No pagination.** `offset` is not exposed; one page, at most 20 results.
- **No rate limiting or spend cap of its own.** The vendor's `429` is
  surfaced as a coded refusal and nothing here counts calls or money. A
  bank that needs a budget needs it at the plane, not in one tool.
- **No caching or deduplication.** Every call is a call the vendor bills.
- **No policy reload.** Loaded once at boot, like `web`.
- **No news, image, video or local search**, and no `extra_snippets`: only
  the fields the contract has a home for are read out of the vendor's
  answer, because a field nobody looked at is a field nobody checked.
- **Results are not fetched or verified.** A URL that passes is a URL that
  *may* be fetched, not one that exists.
