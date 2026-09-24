# tools

**Tool packages you can adopt instead of writing.** Governed tools that garm
maintains, packaged so that you take the ones you want and nothing else.

## What is here

```
payments/     the tools it declares, and the compartments they require
identity/
compliance/
```

One repository, **per-package adoption**. You depend on
`garm-ai/tools/payments@v1.2`, not on this repository. One CI and one release
train here; no inherited tools there.

Separate repositories per domain were considered and rejected: these domains
change together far more often than they change apart, and N repositories buys
an independence nobody asked for at the cost of N pipelines.

## A package brings its own taxonomy

`payments` declares the compartments its tools require. Adopting the package
means adopting that vocabulary — and that coupling is **opt-in, by the act of
adoption**. Nothing here is imposed on a catalogue that does not ask for it.

If a package and your own protos both declare a compartment, identical
declarations merge and any difference fails your catalogue build, naming both
sources. Adopt one definition or rename yours; nothing is silently resolved.

## How this differs from `examples`

An [example](../examples) is a template you fork and then own outright.
A package here is a product you consume and we maintain. Forking an example
means never hearing from us again; adopting a package means a version bump
when we fix something.

## Status

Not yet seeded. Intent recorded; code arrives at Phase 6 of the split.
