# sanitize

**Text somebody outside the tenant wrote, made ready for a model to read.**
A fetched page, a search snippet, an extracted document.

It is not a boundary. Nothing inside the agent process constitutes
containment — Hermes Agent's security statement is right about that, and it
is why garm's chain sits outside the process. What this package does is make
the observation quieter and clearly marked:

- **Removes the cheap carriers** an injection rides on: control characters,
  zero-width and bidirectional characters, tag characters, variation
  selectors, private-use glyphs, soft hyphens.
- **Normalises** to NFC and collapses whitespace, so two fetches of one page
  compare equal and a hash over the text means something.
- **Neutralises sentinels**: runs of three or more `<` or `>` collapse to two,
  so the wrapper below cannot be opened or closed from inside; `<|…|>`
  chat-template tokens are broken with a space.
- **Caps** the text by rune count and says so with `[truncated]`.
- **Annotates** — never refuses — when it saw a phrase shaped like an
  instruction to a model (`injection-phrase`), when it removed invisible
  characters, when it rewrote a sentinel.
- **Wraps** the result between two markers the runner and the model can see:

```
<<<untrusted-content source="https://example.com" notices="injection-phrase" truncated="true">>>
…the text…
<<<end-untrusted-content>>>
```

## API

`go get github.com/garm-ai/tools/sanitize@v0.1.0`; the git tag is
`sanitize/v0.1.0`, and Go names a nested module's version without the
prefix. The module depends on `golang.org/x/text` and nothing else.

```go
c := sanitize.Clean(text, 15000)   // 0 means DefaultMaxRunes
c.Text, c.Truncated, c.Notices
s := sanitize.Wrap(c, "https://example.com")   // an origin, never a full URL
```

Notices are the constants `NoticeInvisibleCharacters`, `NoticeSentinels`,
`NoticeInjectionPhrase`, in that order when several apply.

## What is deliberately not here

- HTML parsing. That is the fetcher's job (`web/extract.go`); this package
  takes text.
- Markdown. Phase 1 returns text; a Markdown converter is a later decision.
- Any refusal. A heuristic that blocks is a heuristic someone will route
  around; the taint escalation the design proposes (§5.4) is the runner's,
  not this package's.
