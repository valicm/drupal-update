# Drupal Update

Go CLI port of [valicm/drupal-update](https://github.com/valicm/drupal-update).
Requires composer.json and composer.lock in the working directory, plus a working Composer launcher. The default uses host Composer; a container launcher can provide PHP and Composer.

Build with `go build -o drupal-update .` (use `-buildvcs=false` in a source directory without usable Git metadata), then run the binary from your Drupal project:

```sh
drupal-update
drupal-update -t all
drupal-update -c false -e token,drupal/redirect -o updates.md
```

The default `semver-safe-update` uses Composer's classification and preserves existing constraints. `all` also changes constraints for packages Composer reports as `update-possible`. Core packages are updated together after contributed packages. Existing development requirements remain development requirements. The legacy `all` mode cannot coordinate core-dev in require-dev; use `major` for coordinated core changes.

Composer can update transitive dependencies with `--with-all-dependencies`. Proposed versions in the summary are discovery results, not confirmation of the installed version. Platform requirements remain enforced. Failed package updates produce a nonzero exit code after writing the report; Composer scripts and plugins can modify the project, so use a working branch and review the resulting diff.

The composite action builds the same CLI and runs it in the caller's workspace. It accepts `update_type`, `update_core`, `update_exclude`, `composer_command`, `update_scope`, and `update_packages`, writes the step summary, and exports `DRUPAL_UPDATES_TABLE`. The selected Composer launcher must already be available and its container or runtime must be ready. CLI flags override action input environment variables.

## Upgrade modes

```sh
drupal-update -t security
drupal-update -t minor
drupal-update -t major
drupal-update -t nuclear -o recovery.md
```

- `security`: audit locked packages, update affected Drupal packages (including transitive Drupal packages) with minimal dependency changes, then verify selected packages no longer have advisories. Existing constraints remain; if they prevent a fix, the command fails. Unrelated dependency changes may be necessary. This mode does not fix non-Drupal advisories.
- `minor`: select the latest stable release within each direct Drupal package's current major. Patch releases are included. Constraints can change, so pinned requirements do not prevent selection.
- `major`: select the latest stable release in a newer major for direct Drupal packages. Existing core release packages are coordinated across require and require-dev. Packages without a newer major stay unchanged, subject to Composer's dependency resolution.
- `nuclear`: back up composer.json, composer.lock and patches.lock.json, remove composer.lock, then run composer install up to eight times. A dependency solving failure can relax direct Drupal requirements to `*`. A patch failure can remove an inline patch only when Composer output identifies exactly one configured patch URL. Dependency and patch locks are regenerated after patch changes. External patch definitions and unidentified failures stop recovery. Core=false, exclusions, scope restrictions and package selection are rejected because a fresh resolution affects the whole graph.

Explicit modes keep backups in `.drupal-update-backup-*` in the project. On failure, manifests and lock files are restored; installed files are not restored. Review changes and run composer install to reconcile installed files. PHP/platform requirements and Composer policy remain enforced. Nuclear mode does not remove packages or bypass platform requirements, and cannot guarantee a resolvable project.

Minor and major selection accepts stable numeric versions (including a leading v), excludes development/prerelease versions, and skips development branches or unsupported locked version formats with an explanation in the report. A numeric prerelease can move to a stable release at or above its base version; minor mode still stays within its major. Security mode needs Composer audit; explicit modes need Composer's `--minimal-changes` support (Composer 2.7 or newer). JSON formatting of composer.json is normalized when constraints or patches change. Core and exclusion options filter selected update targets; Composer can still move dependencies needed by those targets. Nuclear retains relaxed `*` constraints on success for review.

## Validation

Run `go test ./...` and `go vet ./...`. The tests include offline integration checks against real Composer when it is installed, using local metapackages for all four modes. Security advisory fixes and patch failures are covered with controlled Composer responses; full Drupal-site upgrades still need project-specific verification.

Composer command behavior follows the [official CLI documentation](https://getcomposer.org/doc/03-cli.md).

## Constraint preservation (#13)

Upgrades keep simple constraint styles: `^1.0` becomes `^2.0.0`, `~1.0` becomes `~2.0.0`, `1.*` becomes `2.*`, and exact `1.0.0` becomes exact `2.0.0`. Stability suffixes such as `@beta` are retained. Minor and major modes use temporary exact Composer update targets to install the chosen release while leaving the broader constraint in composer.json. Minor and major modes retain compound alternatives such as `^2 || ^3`; Composer rejects a temporary target outside those alternatives and the manifests and locks are restored. Other complex constraints and aliases are not automatically rewritten. The legacy `all` mode rejects unsupported complex constraints. Security and semver-safe updates do not rewrite requirements; nuclear may deliberately relax them to `*`.

## Container Composer (#12)

```sh
drupal-update --composer-command 'ddev composer' -t minor
drupal-update --composer-command 'docker compose exec -T php composer' -t security
```

The launcher supports quoted arguments and executes without a shell: variables, pipes, substitutions and redirects are not expanded. Only its first executable is checked on the host. Commands inherit the current project directory; configure the container to use that same mounted project and working directory. The launcher must preserve Composer's JSON stdout and exit codes. `docker compose exec` needs `-T` so JSON commands do not allocate a terminal.

## Separate update targets and PRs (#11)

```sh
drupal-update -t minor --scope core
drupal-update -t minor --scope contrib
drupal-update -t minor --scope contrib --packages token
drupal-update -t major --packages token,drupal/redirect
```

Scopes are `all` (default), `core`, and `contrib`. Package names can be full names or Drupal module names. Selecting any core package selects the core group so its required release packages stay coordinated. Exclusions and `--core false` still apply; `--scope core --core false` is rejected. Selecting an unknown locked package is an error. These filters choose update targets; necessary transitive dependency changes can still appear in a PR.

The [split update workflow](examples/split-updates.yml) checks core every four hours and grouped contrib daily. The [per-module workflow](examples/per-module-updates.yml) creates separate branches for a configurable module matrix. Both use a stable branch per group or module, preventing a new PR on every run. Copy an example to the Drupal project's .github/workflows directory, replace `UPDATE_ACTION_REF` with a tag or commit containing this Go version, and set the PHP version/extensions and validation steps for that project. The current published v4 action does not include these new inputs. Adapt `add-paths` to include any scaffolded or generated project files that must be committed; backups are intentionally excluded.

The examples use GITHUB_TOKEN. Enable permission for Actions to create PRs in the repository; use an appropriate GitHub App token or repository secret if the PR must trigger further workflows, since events generated with GITHUB_TOKEN generally do not trigger other workflows. PR creation and automerge run in the caller's workflow, not in the CLI.

For optional automerge, give the create-pull-request step `id: pr`, enable repository automerge and required checks, then add:

```yaml
- name: Enable automerge after required checks pass
  if: steps.pr.outputs.pull-request-number && steps.pr.outputs.pull-request-operation == 'created'
  env:
    GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
    PR_NUMBER: ${{ steps.pr.outputs.pull-request-number }}
  run: gh pr merge "$PR_NUMBER" --auto --squash
```

This is opt-in and is not enabled by the example workflows. Required branch checks must reflect your site's validation needs.

## Real project regression fixtures

`fixtures/project_x`, `project_y`, and `project_z` contain supplied manifests and historical locks. Tests copy them into temporary directories and verify that the originals remain unchanged. Coverage includes mixed stable/prerelease/development dependencies, constraint styles and compound alternatives, coordinated core release packages including core-recipe-unpack and require-dev, transitive security targets, full lock rollback, and removal of one identified inline patch.

Most upgrade scenarios use controlled Composer responses against the complete project files so they run without private repositories, old PHP versions, downloads, scripts or plugins. Real Composer additionally inspects all fixture locks and validates their manifests offline. Project X intentionally retains its stale lock baseline. A fixture-derived compound-constraint test also exercises the real solver on local metapackages, including rollback when a major target lies outside the retained alternatives. These tests do not establish that the complete sites can currently install or upgrade against their live repositories.
