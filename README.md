# Description

GitHub Action for updating Drupal core and/or contributed modules
with Composer.

It outputs table of changes, which than can be used further in GitHub workflows.
It can be used as GitHub action, but bash script can be used as standalone outside of GitHub.

# GitHub Action Usage

See [action.yml](action.yml)

```yaml
    steps:
      - uses: actions/checkout@v2
      - name: Check updates
        id: updates
        uses: valicm/drupal-update@v1
        with:
          update_type: 'semver-safe-update'

```

- update_type -> can be either `semver-safe-update` or `all`. 

semver-safe-update - means it will perform minor updates

all - means it would try to perform all updates

# Example to auto-create PR with minor updates
Runs each day once at midnight. Perform all minor updates, and creates automated PR.
(you need to set secret variable named MY_PERSONAL_TOKEN in your repo, so that PR can be created)

```yaml
name: Automated Drupal updates

on:
  workflow_dispatch:
  schedule:
    - cron: '0 0 * * *'

jobs:
  check-available-updates:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v2
        
      - name: Check updates
        id: updates
        uses: valicm/drupal-update@v1
        with:
          update_type: 'semver-safe-update'

      - name: create pull-request
        uses: peter-evans/create-pull-request@v5
        with:
          token: ${{ secrets.MY_PERSONAL_TOKEN }}
          commit-message: Automated Drupal updates
          title: Automated Drupal updates
          body: ${{ env.DRUPAL_UPDATES_TABLE }}
          branch: drupal-automated-updates
          delete-branch: true

```

