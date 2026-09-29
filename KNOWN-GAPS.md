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
  catalogue and mounts; the service behind it — policy file, SSRF floor,
  redirect re-check, extraction, the wrapped response, `webd` — arrives in
  the next tasks.
- `payments/`, `identity/`, `compliance/`: the packages this repository was
  created for. Intent recorded in `README.md`; nothing seeded.
