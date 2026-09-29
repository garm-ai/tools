# Known gaps

## Built

Phase 1 (taxonomy, sanitize, fetch_page) is in progress; each task moves its
item from the list below to this one.

- `taxonomy` — `internet`, `generated-artefacts`; `research`, `documents`, as a
  proto file a consumer copies and a Go module with the same four strings as
  constants. `garm lint --proto taxonomy/proto` is clean; a catalogue cannot be
  built from it alone because it declares no tool, and that is correct.

## Not built

- `sanitize`: `Clean` and `Wrap`.
- `web`: `fetch_page`, its policy file, the SSRF floor, the redirect re-check,
  extraction, the wrapped response, `webd`.
- `payments/`, `identity/`, `compliance/`: the packages this repository was
  created for. Intent recorded in `README.md`; nothing seeded.
