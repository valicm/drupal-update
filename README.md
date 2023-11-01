## Description
Bash script for updating Drupal core and/or contributed modules with Composer. 
It can be used as a GitHub action or as a standalone script/integrated into other CI tools.

## Features
* perform minor or major updates to Drupal core / contributed modules
* options to exclude modules from check and/or enable Drupal core checks
* outputs Markdown table of changes as a file or environment variable
* highlight failed patches
* can be used as a GitHub action
* can be used as a standalone script.


## GitHub Action Usage
![](https://vallic.com/sites/default/files/2023-11/github_example.png "GitHub Drupal Upgrades")

See [action.yml](action.yml)

```yaml
    steps:
      - uses: actions/checkout@v2
      - name: Check updates
        id: updates
        uses: valicm/drupal-update@v3

```

### GitHub action example to create PR with updates
* Runs each day once at midnight. 
* Perform minor/security updates
* Creates automated PR with branch `drupal-automated-updates`
_(you need to set secret variable named MY_PERSONAL_TOKEN in your repo, so that PR can be created)_

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
        uses: valicm/drupal-update@v3

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

## Standalone script usage
![](https://vallic.com/sites/default/files/2023-11/local_example.png "GitHub Drupal Upgrades")

| Example                                       | Command                                      |
|-----------------------------------------------|----------------------------------------------|
| Run all minor and security updates            | `bash drupal-update.sh`                      |
| Run any update (minor, security, major)       | `bash drupal-update.sh -t all`               |
| Run any update, except for Drupal core        | `bash drupal-update.sh -t all -c false`      |
| Run minor update, excluding some modules      | `bash drupal-update.sh -e pathauto,redirect` |
| Run all updates, saving summary in upgrade.md | `bash drupal-update.sh -t all -o upgrade.md` |


Get all minor updates and output results in summary.md file.
```bash
curl -fsSL https://raw.githubusercontent.com/valicm/drupal-update/main/drupal-update.sh | bash -s -- -o summary.md
```
