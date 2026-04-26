# Boxland migration notes

This file collects per-version notes that the `boxland update` command
prints right after a successful upgrade. Entries are keyed by the
*target* version (the version the user is moving to) so a user
crossing several versions in one update sees every relevant note.

The format is one `## vX.Y.Z` heading per release, with free-form
markdown beneath. Empty sections are fine — the updater just skips
them. Add a new section whenever a release introduces something a
user needs to know (a manual data migration, a config change, a new
required dependency, a behaviour change worth flagging).

Keep entries short and action-oriented. A user reading this is mid-
upgrade and wants to know: did anything change for me, and do I need
to do anything?

---

## v0.1.0

First versioned release. Earlier checkouts (anything pinned to
`0.0.0-dev`) should run `boxland update` once to land on a real
version number, after which future updates will be tracked
incrementally.
