# Search music entities by name

CORE-2321: artist kinds do not opt into title search. Declare artists and albums FullTextSearchable so Core returns the entity pages themselves, not only matching tracks.

Non-goals: change ranking, search authorization, genres or metadata. Existing Core policy registration resynchronizes opted-in entity indexes. Evidence: a failing bundle contract test followed by package tests and lint. Rollback: remove the opt-ins. Integration needs a plugin release and Core repin, reserved for the orchestrator because this builder may not release.
