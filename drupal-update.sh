#!/bin/bash
# GNU General Public License v3.0
#
# Copyright (c) 2023 Valentino Međimorec
#
set -e

###############################################################################
# Predefined options                                                          #
#                                                                             #
# Type of update to perform                                                   #
# - semver-safe-update (only perform minor changes in versions)               #
# - all (perform all possible upgrades, includes minor)                       #
#                                                                             #
###############################################################################

###############################################################################
# Simplistic script to use with GitHub Actions or standalone                  #
# to perform composer updates.                                                #
#                                                                             #
# Standalone usage                                                            #
# Perform minor updates         -> bash drupal-update.sh semver-safe-update   #
###############################################################################

update_project() {
    PROJECT_NAME=$1
    CURRENT_VERSION=$2
    LATEST_VERSION=$3
    UPDATE_STATUS=$4
    if [ "$UPDATE_STATUS" == "update-possible" ]
    then
      composer require "$PROJECT_NAME":"$LATEST_VERSION" -W -q --ignore-platform-reqs
    else
      composer update "$PROJECT_NAME" -W -q --ignore-platform-reqs
    fi

    if [[ $LATEST_VERSION == dev-* ]]; then
      echo success
    elif grep -q "$LATEST_VERSION" composer.lock; then
      echo success
    else
      echo failed
    fi
}

# Determine if we're running inside GitHub actions.
GITHUB_RUNNING_ACTION=$GITHUB_ACTIONS

# For GitHub actions use inputs.
if [ "$GITHUB_RUNNING_ACTION" == true ]
then
  UPDATE_TYPE=${INPUT_UPDATE_TYPE}
else
  UPDATE_TYPE=$1
fi

# Fallback to minor updates.
if [ "$UPDATE_TYPE" != "semver-safe-update" ] && [ "$UPDATE_TYPE" != "all" ]
then
  UPDATE_TYPE="semver-safe-update"
fi

echo "| Project name | Old version | Proposed version | Update status | Patch review | Abandoned |"  >> "$GITHUB_STEP_SUMMARY"
echo "| ------ | ------ | ------ | ------ | ------ | ------ |" >> "$GITHUB_STEP_SUMMARY"
# Read composer output. Remove whitespaces - jq 1.5 can break while parsing.
UPDATES=$(composer outdated "drupal/*" -f json -D --locked --ignore-platform-reqs | sed -r 's/\s+//g');

for UPDATE in $(echo "${UPDATES}" | jq -c '.locked[]'); do
  PROJECT_NAME=$(echo "${UPDATE}" | jq '."name"' | sed "s/\"//g")
  PROJECT_URL=$(echo "${UPDATE}" | jq '."homepage"' | sed "s/\"//g")
  CURRENT_VERSION=$(echo "${UPDATE}" | jq '."version"' | sed "s/\"//g")
  LATEST_VERSION=$(echo "${UPDATE}" | jq '."latest"' | sed "s/\"//g")
  UPDATE_STATUS=$(echo "${UPDATE}" | jq '."latest-status"' | sed "s/\"//g")
  ABANDONED=$(echo "${UPDATE}" | jq '."abandoned"' | sed "s/\"//g")
  PATCHES=$(cat composer.json | jq '.extra.patches."'$PROJECT_NAME'" | length')

  RESULT="skipped"

  if [ "$UPDATE_TYPE" == 'all' ]
  then
    echo "Update $PROJECT_NAME from $CURRENT_VERSION to $LATEST_VERSION"
    RESULT=$(update_project "$PROJECT_NAME" "$CURRENT_VERSION" "$LATEST_VERSION" "$UPDATE_STATUS")
  else
    if [ "$UPDATE_STATUS" == "$UPDATE_TYPE" ]
    then
      echo "Update $PROJECT_NAME from $CURRENT_VERSION to $LATEST_VERSION"
      RESULT=$(update_project "$PROJECT_NAME" "$CURRENT_VERSION" "$LATEST_VERSION" "$UPDATE_STATUS")
    fi
  fi

  echo "| [${PROJECT_NAME}](${PROJECT_URL}) | ${CURRENT_VERSION} | ${LATEST_VERSION} | $RESULT | $PATCHES | $ABANDONED |" >> "$GITHUB_STEP_SUMMARY"

done

echo 'DRUPAL_UPDATES_TABLE<<EOF' >> "$GITHUB_ENV"
echo "$(cat $GITHUB_STEP_SUMMARY)" >> "$GITHUB_ENV"
echo 'EOF' >> "$GITHUB_ENV"