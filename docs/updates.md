# Updating Boxland

Boxland ships from a Git repo, not as a packaged binary, so an update
is "pull the new source, regenerate, migrate, rebuild." The TUI
notices when there's a new release and offers to do it for you.

## TL;DR

When the TUI shows the pink **Update available** banner, run:

```
boxland update
```

That's it. Boxland will:

1. Refuse to clobber uncommitted local changes.
2. Snapshot your database to `backups/pre-update-<from>-to-<to>-<ts>.tar.gz`.
3. `git pull --ff-only origin main`.
4. Re-exec the freshly pulled code to install dependencies, regenerate
   `templ` / `sqlc` / `flatc` / fonts, apply new migrations, rebuild
   the web client, and rebuild the `boxland` binary.
5. Print any version-specific notes from `MIGRATION_NOTES.md`.

Restart any running `boxland serve` / `boxland design` afterward to
pick up the new build.

## How updates surface

There are three places Boxland tells you about a new release:

- **TUI banner** — a one-line pink strip above the menu the moment
  the cached check returns. The "U" hotkey jumps straight to the
  update flow (or re-checks if no update is known yet).
- **TUI menu** — the **Update Boxland** row pins to position 0 with
  the `ready` badge while an update is available, then drops to the
  bottom of the menu (as **Check for updates**) once you're current.
- **Designer chrome bar** — a quiet pulsing pill in the in-app
  header when you're working in the design tools, linking to the
  release notes on GitHub.

Players are intentionally not notified — they don't operate the
server.

## Manual usage

```
boxland update            # Apply the latest release (interactive).
boxland update --check    # Print current vs latest, exit 0.
boxland update --force    # Skip clean-tree / on-main guards.
boxland update --no-backup  # Skip the pre-update DB snapshot (CI only).
```

## Disabling the check

Set `BOXLAND_DISABLE_UPDATE_CHECK=true` to skip every GitHub probe.
The cache is also bypassed, the TUI banner stays hidden, the
designer chrome pill never renders, and `/design/api/version` returns
a blank status. Useful for offline workshops, air-gapped servers,
and CI.

If the public `boxland` repository is shared by many users behind
the same NAT (workshop wifi, school lab) and you start hitting
GitHub's anonymous rate limit (60/hour/IP), set
`BOXLAND_GITHUB_TOKEN=<your-PAT>` to lift the limit to 5,000/hour.

The cache itself lives at:

| OS | Path |
|---|---|
| Linux | `~/.config/boxland/update-cache.json` |
| macOS | `~/Library/Application Support/boxland/update-cache.json` |
| Windows | `%LOCALAPPDATA%\boxland\update-cache.json` |

Delete it to force a fresh probe on the next launch.

## Releasing a new version

Boxland's `Version` constant is sourced from
[`server/internal/version/VERSION`](../server/internal/version/VERSION).
The release ritual is:

1. Bump `VERSION` to the new SemVer (no leading `v`, just `0.2.0`).
2. Add a `## v0.2.0` section to
   [`MIGRATION_NOTES.md`](../MIGRATION_NOTES.md) describing anything
   operators need to know.
3. Commit, tag (`git tag v0.2.0 && git push --tags`), and create a
   GitHub release pointing at the tag. The release name **must**
   parse as SemVer (with or without a leading `v`); pre-releases
   and drafts are ignored by the updater.
4. The next time anyone launches the TUI, the banner appears within
   a minute (cache TTL respected).

The updater also looks at the release `body` so the markdown there
can carry the customer-facing changelog.

## Recovery

Every `boxland update` writes a database snapshot before touching
anything. To roll back:

```
boxland backup import backups/pre-update-<from>-to-<to>-<ts>.tar.gz --yes
git checkout <previous-commit>
boxland install
```

The snapshot path is printed on the success/failure line so you can
copy/paste it without hunting through `backups/`.

## Across the long dev process

While Boxland is in alpha, expect frequent migrations and occasional
breaking changes. Two pieces of plumbing make those easier to
navigate:

- **`boxland_meta.last_started_version`** records which version last
  successfully booted the database. The `boxland serve` log emits a
  `boxland version changed since last boot` line on first launch
  after an update, so post-mortems have a clear before/after.
- **`MIGRATION_NOTES.md`** is read after every successful update and
  the section matching the new version is printed in the terminal.
  Use it for one-time data fixups, deprecated env vars, etc.

When v1.0 looms, expect this doc to grow a "stable upgrade channel"
section with signed binaries; until then, "git pull + re-bootstrap"
is the supported path.
