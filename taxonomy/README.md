# taxonomy

**The vocabulary the garm-ai/tools packages share.** Two compartments and two
tool sets, declared once, here, and imported by every tool proto in this
repository that names them.

Status: tagged `taxonomy/v0.2.0`. The four names and their descriptions are
unchanged from `v0.1.1`; the minor bump is the dependency changing identity.
The annotations this package compiles against are
`github.com/garm-ai/contracts` v0.2.0 rather than `github.com/garm-ai/garm`
v0.14.2 — the same file paths, published from a module of their own since the
CLI and the contract were split. **The two cannot be linked into one binary**:
both register `garm/tool/v1/*.proto`, so a service holding this package at
`v0.1.1` alongside anything on the new contract compiles and then dies in
`protoregistry` at startup. Move in one step.

## The declarations

| Kind | Name | Meaning |
|---|---|---|
| compartment | `internet` | Reaching hosts outside the tenant over the public internet. Held by principals that may read from the web; *what* they may read is the fetch service's allowlist per deployment and a CEL guard per agent |
| compartment | `generated-artefacts` | Files a tool produced during a run. Classified whole, at the creating run's level, because redaction cannot see inside a workbook |
| tool set | `research` | Reading the outside world: fetching, searching. Tools whose output was written by someone outside the tenant |
| tool set | `documents` | Producing and reading files |

Reaching the internet is a compartment and not a clearance because it is a
need-to-know question: a principal cleared to RESTRICTED still has no
business on the web unless it holds `internet`. Clearance says how sensitive;
a compartment says whose business it is.

## Adopting it

Copy `proto/tools/taxonomy/v1/taxonomy.proto` into your proto tree (from the
module cache, `$(go env GOMODCACHE)/github.com/garm-ai/tools/taxonomy@v0.2.0/proto/`;
the cache is read-only and `cp -R` keeps its modes, so create the target
directory first and `chmod -R u+w` it after — the repository README's
"Adopting a package" has the exact commands) and import it from any proto that names one of these compartments or sets,
even though no symbol is referenced: the import graph should say that a tool
depends on the file declaring its compartments.

If your tree already declares one of these names with a different
description, `garm catalogue build` fails naming both sources (L29). Adopt one
definition or rename yours.

The Go module — `go get github.com/garm-ai/tools/taxonomy@v0.2.0`; the git tag
is `taxonomy/v0.2.0`, and Go names a nested module's version without the
prefix — gives you the same four strings as constants (`taxonomy.CompartmentInternet`
and friends) so a claims file or a test never misspells one, and links the
file descriptor so the declarations can be read off it.

## What is deliberately not here

A `pii-*` or `financial` vocabulary. Those belong to the organisation that
adopts these tools, not to the tools.
