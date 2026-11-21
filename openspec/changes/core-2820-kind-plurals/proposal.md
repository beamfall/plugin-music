# Plural entity kind names

CORE-2820: clients head a group of related entities with the kind's singular display name, so a group of several entities is headed in the singular. plugin-sdk v0.4.7 adds `EntityKind.PluralDisplayName`. Declare English and French plurals for every entity kind: artist "Artists", album "Albums", genre "Genres".

Non-goals: change kind names, singular labels, relations or search. Evidence: package tests, gofmt, lint and strict specs against plugin-sdk v0.4.7. Rollback: remove the plurals; clients fall back to the singular display name.
