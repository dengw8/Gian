# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Layout

This is a single-context repo.

Use:

- `CONTEXT.md` at the repo root for domain language.
- `docs/adr/` at the repo root for architectural decisions.

## Before exploring, read these

- **`CONTEXT.md`** at the repo root.
- **`docs/adr/`** ADRs that touch the area you're about to work in.

If either path does not exist, proceed silently. The `/domain-modeling` skill creates these files lazily when terms or decisions are resolved.

## File structure

```text
/
├── CONTEXT.md
├── docs/adr/
└── src/
```

## Use the glossary's vocabulary

When output names a domain concept, use the term as defined in `CONTEXT.md`. Do not drift to synonyms the glossary explicitly avoids.

If the concept is missing from the glossary, either reconsider whether the language belongs in this project or note the gap for `/domain-modeling`.

## Flag ADR conflicts

If output contradicts an existing ADR, surface it explicitly rather than silently overriding it.
