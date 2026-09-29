# Known gaps

## Built

Nothing yet. Phase 1 (taxonomy, sanitize, fetch_page) is in progress; each
task moves its item from the list below to this one.

## Not built

- `taxonomy`: the four declarations, as a proto package and Go constants.
- `sanitize`: `Clean` and `Wrap`.
- `web`: `fetch_page`, its policy file, the SSRF floor, the redirect re-check,
  extraction, the wrapped response, `webd`.
- `payments/`, `identity/`, `compliance/`: the packages this repository was
  created for. Intent recorded in `README.md`; nothing seeded.
