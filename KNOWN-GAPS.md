# Known gaps

## Built

Phase 1 (taxonomy, sanitize, fetch_page) is in progress; each task moves its
item from the list below to this one.

- `taxonomy` — `internet`, `generated-artefacts`; `research`, `documents`, as a
  proto file a consumer copies and a Go module with the same four strings as
  constants. `garm lint --proto taxonomy/proto` is clean; a catalogue cannot be
  built from it alone because it declares no tool, and that is correct.
- `sanitize` — `Clean` (NFC, control and invisible characters removed and
  noticed, whitespace collapsed, sentinels neutralised, injection phrases
  annotated, rune cap with a note) and `Wrap` (the in-band untrusted-content
  markers). The marker is in-band because nothing in `garm.tool.v1` yet says
  a response is untrusted; the design's `effects.untrusted_output` (§5.4,
  step 10) replaces "carry the marker" with a flag agentd records.

## Not built

- `web`: the contract (`web.v1.fetch_page`) is declared, lints, builds a
  catalogue and mounts; the policy file loads (allow and block lists, caps,
  digest; fail-closed on no allow entries, an unknown key, malformed YAML or
  a cap out of range); the SSRF floor refuses non-public addresses and local
  or metadata names before DNS, and the guarded dialer resolves the host
  itself, refuses unless every address is public, and dials the vetted
  literal; extraction reduces HTML to the text a reader sees (`script`,
  `style`, `template`, `noscript`, `iframe`, `object`, `svg`, comments and
  hidden subtrees dropped, the title kept apart); the fetcher re-checks
  every redirect hop and the resolved address, caps the body and the text,
  gates the content type, and the handler returns the wrapped response and
  writes the one log line. `webd`, the process that registers it on NATS,
  arrives in the next task; until then the module is not tagged.
- `web`: extraction judges "hidden" from the markup alone: the `hidden`
  attribute, `aria-hidden`, and inline `style`. Text hidden by an external
  stylesheet, a class (`sr-only`, `visually-hidden`), a `clip`/`clip-path`
  rule, or a zero-height overflow box is not dropped and reaches the
  sanitiser like any other text. Rendering CSS is out of scope for a text
  tool; the sanitiser's injection-phrase notice is the backstop.
- `web`: a hidden element whose end tag is omitted drops the rest of the
  document. HTML lets `p`, `li`, `td`, `tr`, `dt`, `dd` and `option` close
  implicitly when the next sibling opens; extraction counts explicit end
  tags only, so `<p hidden>x<p>visible` never sees the first `p` close and
  treats everything after it as hidden. A self-closing `<svg/>` or `<math/>`
  does the same: a browser opens a foreign element there, and so does
  extraction, and nothing closes it. Both err in the safe direction (text a
  model should not have read is dropped, never the reverse) and the
  `truncated` flag is not raised, so a caller sees a shorter page, not a
  marked one. Fixing it means a parser that knows the implicit-close rules,
  which is a DOM; recorded rather than built.
- `web`: the guarded dialer connects to the first vetted address only. A host
  whose first address is unreachable is not retried on its second; the fetch
  fails and the caller retries.
- `web`: ports are not restricted. Any port on an allowed host is dialled
  (`https://www.example.com:8443/` passes the same checks as `:443`); the
  floor is on the address, not the port. A policy knob (`ports:` or a
  443-only default) is a follow-up.
- `web`: a policy is loaded once at boot. Reloading on a signal is not built;
  a change to the lists is a restart.
- `payments/`, `identity/`, `compliance/`: the packages this repository was
  created for. Intent recorded in `README.md`; nothing seeded.
