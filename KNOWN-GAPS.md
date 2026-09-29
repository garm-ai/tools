# Known gaps

## Built

Phase 1 is tagged: `taxonomy/v0.1.1`, `sanitize/v0.1.1`, `web/v0.1.1`.

- `taxonomy` - `internet`, `generated-artefacts`; `research`, `documents`, as a
  proto file a consumer copies and a Go module with the same four strings as
  constants. `garm lint --proto taxonomy/proto` is clean; a catalogue cannot be
  built from it alone because it declares no tool, and that is correct.
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
  deployment (`mise run check-web`).

## Not built, and why

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
