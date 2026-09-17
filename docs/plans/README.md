# Plans

- [Model router](active/model-router.md): controlling implementation plan for Frost, including the implemented CLI, catalog producer and capacity-aware selection plus the remaining evaluation work.
- [Catalog and performance evidence](active/catalog-and-profile-evidence.md): supporting contract for public-data refresh, model/output types, latency, context, and optional personal preferences. Read after the controlling plan.
- [Slice 1a: public catalog producer](implemented/slice-1a-catalog-producer.md): implemented `tools/catalog-build`, source connectors, identity overlay, and provisional measured adequacy rules (td-452c07; planning task td-b850c7).
- [Slice 2: capacity-aware selection](implemented/slice-2-capacity.md): implemented neutral usage snapshot, availability and expiry preference in the router, and optional CodexBar wrapper (td-3af594; planning task td-d398e8).
- [Public-source research](../research/model-data-sources.md): verified sources, access conditions, refresh semantics, and candidate inputs for external producers.

Plans in `planning/` await approval, plans in `active/` still control ongoing work, and completed slices live in `implemented/`. The executable under `experiments/` is the original feasibility probe.
