# Known gaps

## Built

Phase 1 is tagged: `taxonomy/v0.2.0`, `sanitize/v0.1.1`, `web/v0.2.0`. The
first of phase 2 is `search/v0.1.0`.

- `taxonomy` - `internet`, `generated-artefacts`; `research`, `documents`, as a
  proto file a consumer copies and a Go module with the same four strings as
  constants. `garm lint` (over `taxonomy/catalogue.yaml`) is clean; a catalogue
  cannot be built from it alone because it declares no tool, and that is
  correct.
- `sanitize` - `Clean` (NFC, control and invisible characters removed and
  noticed, whitespace collapsed, sentinels neutralised, injection phrases
  annotated, rune cap with a note) and `Wrap` (the in-band untrusted-content
  markers).
- `web` - `web.v1.fetch_page`: the annotated contract; a fail-closed YAML
  policy with allow and block lists, caps and a digest; the SSRF floor before
  DNS, at the dial and on every redirect hop; extraction of the text a reader
  sees; the wrapped response with notices; one attributed log line; `webd`;
  tests for every refusal class and an end-to-end test over an embedded NATS
  server with `Garm-Invocation`. Lints, builds a catalogue, mounts on a bare
  deployment (`mise run check-web`), which since `web/v0.2.0` means garmd
  v0.3.0 or later. On tool-go v0.6.0 the handler runs synchronously and
  `--concurrency` (default 8) is that many micro service instances rather
  than goroutines; `webd` passes its logger to the runtime, so the
  concurrency actually in force is a line in the service's own log at
  startup whether it was passed or defaulted.
- `search` - `search.v1.search_web`: the annotated contract, with an
  `(garm.meta.v1.owner)`; a fail-closed YAML policy naming the backend, the
  endpoint, the result allow and block lists, caps and a digest; the two
  halves of the floor (egress to the configured endpoint, no redirects at
  all, every resolved address checked; ingress over every URL the index
  returned, dropped and counted rather than refused); a Brave client whose
  credential comes from a file and reaches no log, URL, error or response;
  per-result `sanitize.Wrap`; one attributed log line carrying neither the
  query nor anything the index wrote; `searchd`. Lints, builds a catalogue,
  mounts on a bare deployment (`mise run check-search`). `--concurrency`
  defaults to 4 — tool-go's own default, chosen rather than inherited: a
  search is one short JSON round trip, and the vendor's requests-per-second
  rating is the real ceiling. An **S** in the port assessment was an estimate
  of effort; "Not built, and why" below says what an S bought and what it did
  not.

## Around the contract dependency

- **`web.v1.WebService` names no `(garm.meta.v1.owner)`.** `garm lint` warns
  `O1` on every build since the pin moved to v0.18.1, and it becomes an error
  in a later release. It is not added here because `Owner.team` is "the unit
  a ledger row is charged to" — the adopting organisation's answer, not this
  repository's — and a placeholder charged to nobody is worse than a warning
  that says so. Adding it also means vendoring `garm/meta/v1/meta.proto`,
  which nothing here imports yet.
- **Nothing warns that a vendored annotation has drifted from the module it
  compiles against.** That hole is `garm`'s (`KNOWN-GAPS.md`, "Around the
  contract dependency": `garm init` writes the contract's bytes and never
  looks again), and it presented here as a green check over a stale tree:
  `mise run vendor-check` compared `third_party/proto` against a version
  *written in `mise.toml`*, so while `go.mod` sat five releases behind, the
  check went on passing because it was comparing the tree to its own
  constant. It now reads the version from every module's own `go.mod` and
  compares `third_party` against each one, which is how `taxonomy` and `web`
  on contracts v0.2.0 and `search` on v0.3.0 can share one vendored tree: the
  two releases publish byte-identical protos, and the day one does not, the
  check goes red. That closes it for this repository and for nobody else. A
  consumer who copies these protos gets no such check.
- **Nothing here asserts a garmd floor.** The `web/v0.2.0` and
  `search/v0.1.0` catalogues need garmd v0.3.0 or later, because the
  generator synthesises two card endpoints per tool and an older daemon
  refuses them. `mise run check-web` and `mise run check-search` run against
  the pinned garmd only, so they prove the floor is met, not where the floor
  is. The annotation schema version is still `v1`, so the
  catalogue's own compatibility window does not express this.
- **The card endpoints are registered but have no result store.** The
  generated `DefaultWebServiceResultCard` answers `result_unavailable`
  (`DefaultSearchServiceResultCard` likewise), because a card about an answer
  needs the answer and nothing here keeps a record of what `fetch_page` or
  `search_web` returned to somebody else. Overriding `ResultCard` and reading
  your own row is the documented route; neither `webd` nor `searchd` does.
  The identifiers are qualified by service since `garm` v0.19.0 — the bare
  `DefaultResultCard` and `ResultCardFrom` this file used to name no longer
  exist for any service, because two single-tool services in one proto
  package declared them twice and the package did not compile.

## Not built, and why

### `search_web`, specifically

An **S** in the port assessment is an estimate of effort, not a claim of
completeness. What `search/v0.1.0` does not do:

- **One backend.** Brave only. `backend:` in the policy file exists so a
  second adapter is a policy change and not a different binary, but there is
  no second adapter. SearXNG — self-hosted, AGPL-3.0 over HTTP, no vendor at
  all — is the obvious one for a deployment that cannot send queries to a
  search company, and it is not written.
- **No fallback between providers, no retry, no caching and no
  deduplication.** Every call is a call the vendor bills, and an upstream
  failure is a coded refusal rather than a second attempt somewhere else.
- **No rate limit or spend cap of its own.** The vendor's `429` is surfaced
  as a `429`; nothing here counts calls or money. A bank that needs a budget
  needs it at the plane, not in one tool.
- **No pagination.** `offset` is not exposed: one page, at most 20 results,
  which is the most one upstream page carries.
- **Only three fields are read** out of the vendor's answer — `url`, `title`,
  `description` — because a field nobody looked at is a field nobody checked.
  `extra_snippets`, freshness, language, country, safesearch and the news,
  image, video and local verticals are all unexposed.
- **The address floor is a second copy of `web`'s.** `floor.go` here and
  `floor.go`/`dial.go` there are the same prefix list, the same `localNames`,
  the same WHATWG "ends in a number" rule, and they can drift. They were not
  extracted into a shared module because doing so retags `web` and moves an
  adopter's dependency graph for no behaviour change; the right moment is the
  third tool that needs them. Until then the two test files are the guard,
  and they cover the same cases on purpose.
- **Results are not fetched or verified.** A URL that passes the floor and
  the policy is a URL that *may* be fetched, not one that exists, and the
  index's claim that a page says something is the index's claim.
- **Nothing correlates a search with the fetches that follow it.** A guard
  cannot say "only fetch a URL search_web returned"; that is the taint bit
  below, and it is not built either.
- **The allowlists are two files.** Nothing checks that `search`'s result
  allowlist is the same as, or narrower than, `web`'s fetch allowlist. An
  operator who widens one and not the other gets an agent shown links it
  cannot follow, or — worse — narrows search and not fetch and believes the
  narrowing covers both.
- **No adopter check.** `mise run adopt-check` walks the README's copy
  commands for `web` from the module cache; there is no equivalent for
  `search`, and there cannot be one until `search/v0.1.0` is on the proxy.

### Across the repository

- **`effects.untrusted_output` and taint escalation** (design 5.4, step 10).
  The wrapper marker is in-band because `garm.tool.v1` has no way yet to say
  a response was authored outside the tenant; agentd cannot turn "read a
  page" into "the next external write needs a grant" until that annotation
  exists. Its own short spec comes first.
- **Egress at the deployment** (design 5.2). The floor stops the service from
  reaching the tenant's network; a NetworkPolicy that permits port 443 to the
  internet and nothing inside the tenant is what makes a bug in the floor not
  a path to the ledger database. Deployment's job; not a package.
- **Policy reload.** A policy is read once at boot. The design wants a reload
  that keeps the previous policy on failure, as garmd's catalogue reload
  does. Restart the service to change the lists.
- **Shared block files** (`shared_files:` in the design's sketch). Every rule
  is in the one file; the key is unknown and refused at boot like any other.
- **Ports are not restricted.** Any port on an allowed host is dialled
  (`https://www.example.com:8443/` passes the same checks as `:443`); the
  floor is on the address, not the port. A policy knob (`ports:` or a
  443-only default) is a follow-up.
- **Only the first vetted address is dialled**; there is no fallback to the
  second when the first refuses the connection. The fetch fails and the
  caller retries.
- **HTTP/2 is off** (a custom dialer without `ForceAttemptHTTP2`). Nothing
  here needs it.
- **Internationalised host names** are matched as written (punycode as-is;
  no normalisation of Unicode labels).
- **Hidden text is judged from the markup alone**: the `hidden` attribute,
  `aria-hidden`, and inline `style`. Text hidden by an external stylesheet, a
  class (`sr-only`, `visually-hidden`), a `clip`/`clip-path` rule, or a
  zero-height overflow box is not dropped and reaches the sanitiser like any
  other text. A negative margin is judged by size alone: `-1000px` and
  beyond is off the page and dropped, anything smaller (`margin-left:-500px`)
  is a layout pull and kept, whether or not it hides in practice. Rendering
  CSS is out of scope for a text tool; the sanitiser's injection-phrase
  notice is the backstop.
- **A hidden element whose end tag is omitted drops the rest of the
  document.** HTML lets `p`, `li`, `td`, `tr`, `dt`, `dd` and `option` close
  implicitly when the next sibling opens; extraction counts explicit end tags
  only, so `<p hidden>x<p>visible` never sees the first `p` close and treats
  everything after it as hidden. A self-closing `<svg/>` or `<math/>` does
  the same. Both err in the safe direction (text a model should not have read
  is dropped, never the reverse) and `truncated` is not raised, so a caller
  sees a shorter page, not a marked one. Fixing it means a parser that knows
  the implicit-close rules, which is a DOM; recorded rather than built.
- **Markdown.** `content` is text with line breaks at block elements, not
  Markdown; links and headings are not marked. A converter (`html-to-markdown`,
  MIT) is a later decision.
- **Non-UTF-8 pages** arrive with replacement characters where the bytes were
  not UTF-8; no charset transcoding.
- **Query strings** are the one channel through which a model can encode what
  it was shown into a request the allowlist permits. The service cannot judge
  them; a manifest guard such as `!args.url.contains('?')` can. Documented in
  `web/README.md`.
- **`search_web`, the artefact store and the generators** (design steps
  4-9), and `payments/`, `identity/`, `compliance/`. Not seeded.
