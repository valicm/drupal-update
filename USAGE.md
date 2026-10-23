# Drupal Update usage scenarios

Examples target **v6.0.0**. See [installation and migration](README.md) for binary downloads, the Bash wrapper, and migration from v4 or v5.

Run standalone commands from the Drupal project directory containing composer.json and composer.lock. Use PHP/extensions that satisfy the project and Composer 2.7+. DDEV or another Composer launcher can provide the runtime instead of host PHP. Review upgrade diffs before deployment.

## Choose an upgrade mode

| Goal | CLI | Behavior |
| --- | --- | --- |
| Default legacy behavior | `drupal-update` | Composer-classified semver-safe updates under current requirements; not security-only |
| Security fixes | `drupal-update -t security` | Update advisory-affected Drupal packages and verify selected advisories are cleared |
| Security fixes with exact pins | `drupal-update -t security --security-update-pinned` | Permit selected exact pins to advance to a stable patch within their current minor |
| Minor and patch updates | `drupal-update -t minor` | Select stable releases within each direct Drupal package's current major |
| Major upgrades | `drupal-update -t major` | Select stable releases in newer majors; coordinate core release requirements |
| Legacy general upgrades | `drupal-update -t all` | Retain the original all-update behavior; prefer major mode for coordinated core-dev changes |
| Dependency recovery | `drupal-update -t nuclear -o recovery.md` | Fresh resolution with bounded retries and explicit reports of relaxed constraints/removed patches |

Scope and package filters choose requested update targets. Composer may move their dependencies too. By default this tool updates project files and dependencies. Database updates and configuration export are opt-in (see scenario 15); it does not deploy the site.

## 1. Run locally with a downloaded binary

Install the matching binary archive for your operating system and architecture using the [README instructions](README.md#standalone-use-outside-github-actions), then:

```sh
cd /path/to/drupal-project
drupal-update --version
drupal-update -t minor -o updates.md
```

A downloaded binary needs no Go installation. On Windows, use `drupal-update.exe`.

## 2. Keep an existing Bash command

```sh
bash /path/to/drupal-update.sh -t all -c false -e token,redirect -o summary.md
```

The wrapper forwards the original flags and keeps the caller's project directory. It uses an explicit/adjacent/PATH binary, builds a local source checkout if Go is available and `DRUPAL_UPDATE_VERSION` is unset, or downloads and verifies a release binary.

```sh
DRUPAL_UPDATE_VERSION=v6.0.0 bash drupal-update.sh -t security
DRUPAL_UPDATE_BINARY=/opt/tools/drupal-update bash drupal-update.sh -t minor
```

`DRUPAL_UPDATE_VERSION=latest` resolves the latest stable GitHub release for downloads. An installed or adjacent binary takes precedence; use `DRUPAL_UPDATE_BINARY` to choose one explicitly.

## 3. Use DDEV or Docker Compose

```sh
drupal-update --composer-command 'ddev composer' -t security
drupal-update --composer-command 'docker compose exec -T php composer' -t minor
```

The container must be running and use the same mounted project and working directory. Only the launcher's first executable needs to exist on the host. Quoted arguments are supported; shell substitutions, variables, pipes, and redirects are not expanded.

## 4. Apply security fixes under existing requirements

```sh
drupal-update -t security -o security-updates.md
```

This includes advisory-affected transitive Drupal packages. When core is affected, the installed core release group is updated together so core-recommended can move its exact core dependency. Unrelated contributed packages are not explicitly requested.

By default, composer.json requirements remain unchanged. A requirement such as `^11.4` can permit a fixed release, while an exact `11.4.7` pin blocks any different version. Composer's platform checks and security blocking stay enabled. Non-Drupal advisories are outside this mode's remediation scope.

## 5. Fix security advisories when core is exactly pinned

For a project with core-recommended/scaffold/project-message pinned to `11.4.7`:

```sh
drupal-update -t security --security-update-pinned -o security-updates.md
```

The tool can advance those exact pins together to a stable patch such as `11.4.10`. They remain exact requirements, and core-dev remains in require-dev if present. It does not advance those pins to `11.5` or `12`. Existing ranges and unrelated requirements remain unchanged.

The summary reports pin changes, and the post-update audit must clear the selected advisories. If no compatible fix is available in the current minor line, the run fails and restores manifests/locks. Installed files are not restored by rollback.

GitHub Action equivalent:

```yaml
- uses: valicm/drupal-update@v6.0.0
  with:
    update_type: security
    security_update_pinned: 'true'
```

This opt-in is supported only for security mode and defaults to false. General security mode can still move packages within their existing range constraints; the same-minor restriction applies to pins advanced by this option.

## 6. Update only core or only contributed packages

```sh
drupal-update -t minor --scope core
drupal-update -t minor --scope contrib
drupal-update -t security --scope core --security-update-pinned
```

Selecting core includes the core release group already used by the project; missing core packages are not added. The legacy `-c false` / `--core false` also disables requested core updates. Combining scope core with core false is an error.

## 7. Update selected modules or exclude fragile ones

```sh
drupal-update -t minor --packages token
drupal-update -t major --scope contrib --packages token,drupal/redirect
drupal-update -t minor --exclude webform,drupal/commerce
```

Short names and full Drupal package names are accepted. Selecting any core package selects its core group. Exclusions still apply. Unknown locked package names are rejected by the CLI. In security mode, only advisory-affected packages within the selection are requested; selecting a nonaffected package does not force an update.

## 8. Leave patched packages alone

```sh
drupal-update -t minor --skip-patched
```

Targets with patches are skipped. Patches are read from inline `extra.patches`, the composer-patches 1.x `extra.patches-file`, the 2.x `extra.composer-patches.patches-file` (default `patches.json`), and `patches.lock.json`, which also lists dependency patches. A patch on core skips the core group. Patch definitions remain unchanged. These are target filters, not a dependency freeze: a necessary transitive change can still affect a patched package and fail.

```yaml
- uses: valicm/drupal-update@v6.0.0
  with:
    update_type: minor
    skip_patched: 'true'
```

Explicitly skipping a patched security target can leave its advisories unresolved; the summary identifies skipped targets.

## 9. Try patched upgrades, then skip an identified failure

```sh
drupal-update -t minor --patch-failure skip -o updates.md
```

For minor/major mode, a uniquely identified selected package with a patch failure can be removed from the retry set after restoring the original manifests and locks. A failed core patch skips the core group. Remaining targets are retried with a shrinking target list. Patch definitions are retained. Failure messages from composer-patches 1.x and 2.x are recognised.

```yaml
- uses: valicm/drupal-update@v6.0.0
  with:
    update_type: minor
    patch_failure: skip
```

The default is fail. Ambiguous/unidentified patch failures and dependency/platform errors still fail. Skip-on-failure is not supported for security mode. If all targets are skipped, the report says so; this does not mean all requested upgrades succeeded. Reconcile installed files with composer install when needed.

## 10. Coordinate a major upgrade

```sh
drupal-update -t major --scope core -o core-major.md
drupal-update -t major -o project-major.md
```

Use core scope when contributed modules already support the target core. Use the full-project command when contributed and core requirements need to move together. The tool selects stable newer-major targets; the resolver can still reject incompatible modules, custom requirements, or platforms. It does not guarantee a one-command major upgrade.

Simple caret/tilde/wildcard/pinned styles are retained. Supported compound alternatives remain unchanged; a target outside those alternatives fails for review. Minor/major selection skips development branches and can move a numeric prerelease to a stable release at or above its base version within the requested version boundary.

## 11. Attempt nuclear recovery

```sh
drupal-update -t nuclear -o recovery.md
```

The tool backs up composer.json, composer.lock, and patches.lock.json where present, removes composer.lock, and runs composer install up to eight times. It can relax direct Drupal requirements to `*` after a solver failure and remove an identified failing inline patch before regenerating locks. The patch entry is removed from composer.json in place, and the remaining patches keep their order. Unknown/external patch failures stop recovery.

Nuclear mode resolves the full graph. Core=false, exclusions, scope/package restrictions, and patch-skipping options are rejected. Successful recovery can leave wildcard constraints and removed patches: review the report and diff. Failed recovery restores manifests/locks, not installed files. It cannot guarantee a resolvable or functioning Drupal site.

## 12. Use the GitHub Action

```yaml
steps:
  - uses: actions/checkout@v7
  - uses: shivammathur/setup-php@v2
    with:
      php-version: '8.4' # Match the project and required extensions.
      tools: composer:v2
  - name: Apply Drupal security fixes
    uses: valicm/drupal-update@v6.0.0
    with:
      update_type: security
      security_update_pinned: 'true'
```

With a release tag the Action runs that release's checksum-verified binary; other references build from source. It writes the step summary, exports `DRUPAL_UPDATES_TABLE`, and sets the outputs listed in the [option reference](#cli-and-action-option-reference). PHP/Composer must already be available, or set `composer_command` to a working container launcher. For PR creation, use one of the complete workflows below and follow the [automation-token setup](README.md#automation-token-setup).

Pin an exact Action tag such as `@v6.0.0` for predictable behavior. `@latest` is a Git reference, not an automatic alias for GitHub's newest release; this repository does not maintain it. The Bash wrapper's `latest` download setting is separate.

## 13. Choose a scheduled PR workflow

Copy an example into the Drupal project's `.github/workflows/` and merge it into the default branch. Configure its PHP/extensions, `DRUPAL_UPDATE_TOKEN`, and project validation.

| Scenario | Complete example | Schedule in UTC |
| --- | --- | --- |
| Security PRs with automerge | [security-updates.yml](examples/security-updates.yml) | Daily at 20:00 |
| Minor/patch PRs for review | [minor-updates.yml](examples/minor-updates.yml) | Friday at 04:31 |
| Core-major PRs around release windows | [core-major-updates.yml](examples/core-major-updates.yml) | Fridays at 04:47 in June/August/December, even years |
| Manual general upgrades | [general-updates.yml](examples/general-updates.yml) | Manual choices for mode/scope/packages |
| Separate core/contrib PRs | [split-updates.yml](examples/split-updates.yml) | Core every four hours; contrib daily |
| One PR per module | [per-module-updates.yml](examples/per-module-updates.yml) | Daily module matrix |

Only the security template enables automerge. Enable repository automerge/squash merging and require the site's checks/reviews on the base branch. Without required checks, a mergeable PR can merge immediately. The automation token must cover the repository's resource owner, repository selection, and Contents/Pull requests write access; pending organization approval can cause checkout HTTP 403. See the [setup screenshot and instructions](README.md#automation-token-setup).

The core-major cadence follows the release policy described in the [README schedule notes](README.md#scheduled-examples). Polling does not guarantee a release exists. Releases after the daily security check are picked up by the next run, and GitHub can delay scheduled jobs.

## 14. Run from another CI platform and save a report

```sh
cd /path/to/checked-out-drupal-project
drupal-update -t security --security-update-pinned -o security-updates.md
```

Archive the Markdown report even when the command fails. The report is the only thing printed to stdout; progress and diagnostics go to stderr, and failures return a nonzero exit status. Do not set GitHub's summary/environment variables for another CI platform; standalone use has no GitHub API dependency once the binary is installed.

## 15. Install from configuration, upgrade, and export configuration

These options are available since v5.0.3.

Prepare a **disposable database**, database credentials in the site's settings, and `$settings['config_sync_directory']` pointing to the committed configuration. Install the current locked dependencies (including Drush) first:

```sh
composer install --no-interaction
drupal-update -t minor --install-existing-config --updatedb --export-config
```

The order is `drush site:install --existing-config --yes` using the old code, Composer upgrades, `drush updatedb --yes`, then `drush config:export --yes`. Installing before the upgrade gives update hooks the old schema and configuration to migrate. A fresh install does not reproduce production content or data-dependent update problems; use a database copy when testing those.

`site:install` can replace an existing database. Only enable installation for a disposable CI/local site, never a production database. The tool does not provision a database, generate settings, install Drush, or back up/restore databases. Exported YAML is written to the site's configured sync directory.

For an already installed site or database copy, omit installation:

```sh
drupal-update -t security --updatedb --export-config
```

For DDEV, provide both launchers:

```sh
ddev composer install --no-interaction
drupal-update -t minor --composer-command 'ddev composer' --drush-command 'ddev drush' --install-existing-config --updatedb --export-config
```

A complete [GitHub Actions example](examples/existing-config-updates.yml) provisions MySQL, prepares disposable settings, installs from configuration, upgrades, and opens a PR containing the exported YAML. It assumes a `web/` docroot and `config/sync/`; adjust its two environment variables and PHP extensions for your project. It runs manually and does not enable automerge.

On GitHub Actions, prepare the database and settings, then:

```yaml
- run: composer install --no-interaction
- uses: valicm/drupal-update@v6.0.0
  with:
    update_type: minor
    install_existing_config: 'true'
    updatedb: 'true'
    export_config: 'true'
```

Include your sync directory (for example `config/sync/**`) along with Composer files in the pull request's `add-paths` if you restrict committed files. Review exported YAML before merging. Disable automatic merging until this workflow is validated for your project.

Post-upgrade commands run on every successful invocation, including when no package updates are eligible. Installation failure stops before upgrading. Composer failure skips post-upgrade Drush commands. A Drush failure fails the invocation and stops subsequent commands; successful Composer changes remain, and database/configuration changes are not rolled back. The Markdown report and GitHub summary include the failed step. `--export-config` requires `--updatedb`.

See Drush's [existing-config installation](https://www.drush.org/13.x/commands/site_install/), [database updates](https://www.drush.org/13.x/commands/updatedb/), and [configuration export](https://www.drush.org/13.x/commands/config_export/) references.

## CLI and Action option reference

| CLI | Action input | Default |
| --- | --- | --- |
| `--type`, `-t` | `update_type` | semver-safe-update |
| `--core`, `-c` | `update_core` | true |
| `--exclude`, `-e` | `update_exclude` | empty |
| `--scope` | `update_scope` | all |
| `--packages` | `update_packages` | empty |
| `--install-existing-config` | `install_existing_config` | false |
| `--updatedb` | `updatedb` | false |
| `--export-config` | `export_config` | false |
| `--drush-command` | `drush_command` | vendor/bin/drush |
| `--composer-command` | `composer_command` | composer |
| `--security-update-pinned` | `security_update_pinned` | false |
| `--skip-patched` | `skip_patched` | false |
| `--patch-failure` | `patch_failure` | fail |
| `--output`, `-o` | `output_file` | empty |

Boolean Action inputs should be quoted, for example `'true'`. CLI boolean switches such as --skip-patched take no value; --core is a string option and uses `--core false`. Output files must have a `.md` extension.

| Action output | Value |
| --- | --- |
| `changed` | `'true'` when composer.json, composer.lock or patches.lock.json changed, otherwise `'false'` |
| `report` | The Markdown report; later steps can also read it from the `DRUPAL_UPDATES_TABLE` environment variable |
