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
  literal. The rest of the service — redirect re-check, extraction, the
  wrapped response, `webd` — arrives in the next tasks.
- `web`: the guarded dialer connects to the first vetted address only. A host
  whose first address is unreachable is not retried on its second; the fetch
  fails and the caller retries.
- `web`: a policy is loaded once at boot. Reloading on a signal is not built;
  a change to the lists is a restart.
- `payments/`, `identity/`, `compliance/`: the packages this repository was
  created for. Intent recorded in `README.md`; nothing seeded.
