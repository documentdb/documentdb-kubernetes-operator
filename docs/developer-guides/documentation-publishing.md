# Documentation publishing

Stable release `1.1.2` publishes to `/1.1/`, titled `1.1` in the version
selector. A later patch updates that same minor entry. The full release tag is
still substituted for `<release-version>` in installation commands and CRD URLs,
and must match the release's `Chart.yaml` version.

The deployment helper records the full tag as the minor entry's `release`
property in `gh-pages:versions.json`. Under the shared Pages deployment lock,
it fetches published metadata before deciding whether to publish:

- A lower patch cannot replace newer docs in the same minor. An equal version
  can be redeployed to repair documentation.
- A patch for an older minor may update that minor, but cannot take `latest`
  from a higher stable release. Comparisons are numeric, not lexicographic.
- Existing full patch entries count toward both ordering guards. A minor entry
  with missing or inconsistent provenance fails rather than risking a downgrade.
- Prereleases require a `major.minor.patch-prerelease` tag and publish only to
  their full-tag path. They never update a stable minor, `latest`, or the root
  redirect. Marking a prerelease tag as stable does not make it a stable chart
  version: publish a matching `major.minor.patch` chart/tag instead.
- Main publishes only `/dev/`, stamped with main's full chart version.

The helper pushes the release content, provenance, aliases and root redirect
together. Existing patch snapshot directories are not rebuilt, renamed, deleted,
or converted to aliases. They remain in the selector until a separate migration.
This policy applies to releases containing the updated workflow and helper;
older release branches must receive the publishing changes before tagging.
Rerunning a historical workflow does not adopt the new policy.

## Proposed one-time migration (requires approval)

Do not run a cleanup automatically during deployment. Before any migration,
inventory `gh-pages` (including `versions.json`, aliases, root redirect and
snapshot directory tree IDs) and retain its commit SHA for rollback. Pause docs
deployments while preparing and reviewing a single migration commit.

1. Select the highest stable patch snapshot for each existing minor. Confirm
   that its installation commands and CRD URLs use that snapshot's full release.
   Seed the missing minor path from that snapshot without rebuilding historical
   source; add a minor selector entry titled `major.minor` and a matching full
   `release` property. Never replace a newer minor entry already deployed.
2. Move `latest` to the minor with the highest stable release and keep the root
   redirect targeting `latest`. Confirm that all aliases still resolve.
3. After every stable patch is represented in minor provenance, propose removing
   the legacy patch entries **only from `versions.json`** to simplify the
   selector. Leave every old patch directory byte-for-byte intact, so bookmarked
   deep links and exact-version snapshots retain their original meaning. Do not
   use `mike delete`, or turn frozen patch URLs into moving minor aliases.
4. Preview the migration, compare old snapshot tree IDs, and check minor/latest,
   old patch deep links, `preview/` redirects and installation pins. Review and
   approve the diff before pushing. If needed, revert only the migration commit,
   coordinating with any subsequent deployments.

Merging the workflow change does not backfill minors, remove old selector
entries, or migrate existing snapshots. The next eligible stable release
creates/updates its own minor and advances `latest` only if ordering allows it.
