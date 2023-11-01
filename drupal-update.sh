#!/bin/bash
###############################################################################
# GNU General Public License v3.0                                             #
# Copyright (c) 2023 Valentino Međimorec                                      #
#                                                                             #
# Simplistic script to use with GitHub Actions or standalone                  #
# to perform composer updates of Drupal projects.                             #
#                                                                             #
# Standalone usage                                                            #
# Run minor updates                  -> bash drupal-update.sh                 #
# Run all updates                    -> bash drupal-update.sh -t all          #
# Run updates, except core           -> bash drupal-update.sh -t all -c false #                                                                          #
###############################################################################

set -e

RESULT_SUCCESS="success"
RESULT_PATCH_FAILURE="patch failure"
RESULT_GENERIC_ERROR="generic error"
RESULT_DEPENDENCY_ERROR="failed dependency"
RESULT_UNKNOWN="unknown"
RESULT_SKIP="skipped"

# Function to display script usage.
usage() {
 echo "Usage: $0 [OPTIONS]"
 echo "Options:"
 echo " -h, --help      Display this help message"
 echo " -t, --type      Options: semver-safe-update or all. Default is semver-safe-update: minor and security upgrades."
 echo " -o, --output    Specify an output file to save summary. Default is none."
 echo " -c, --core      Flag to enable or disable Drupal core upgrades check. Default is true."
 echo " -e, --exclude   Exclude certain modules from updates check. Use comma separated list: token,redirect,pathauto"
}

# Exit with error.
exit_error() {
  usage
  exit 1
}

# Validate options passed by GitHub or by standalone usage with flags.
validate_options() {
    UPDATE_TYPE=$1
    UPDATE_CORE=$2
    UPDATE_EXCLUDE=$3
    SUMMARY_FILE=$4

    if  [ -n "$UPDATE_TYPE" ] && [ "$UPDATE_TYPE" != "semver-safe-update" ] && [ "$UPDATE_TYPE" != "all" ]; then
      echo "Error: Update type can be either semver-safe-update or all"
      exit_error
    fi

    if [ -n "$UPDATE_CORE" ] && [ "$UPDATE_CORE" != true ] && [ "$UPDATE_CORE" != false ]; then
      echo "Error: Core flag must be either true or false. Default if empty is false"
      exit_error
    fi

    if [ -n "$SUMMARY_FILE" ] && [[ "$SUMMARY_FILE" != *.md ]]; then
      echo "Error: Summary output file needs to end with .md extension"
      exit_error
    fi
}

# Validate if all requirements are present.
# Check existence of composer.json/lock file, composer, sed and jq binaries.
validate_requirements() {
  if [ ! -f composer.json ] || [ ! -f composer.lock ]; then
    echo "Error: composer.json or composer.lock are missing"
    exit 1
  fi

  local BINARIES="php composer sed grep jq";
  for BINARY in $BINARIES
  do
    if ! [ -x "$(command -v "$BINARY")" ]; then
      echo "Error: $BINARY is not installed."
      exit 1
    fi
  done

}

# Update project based on composer update status.
# Mark dev versions as success, because we don't have specific version.
update_project() {
    PROJECT_NAME=$1
    CURRENT_VERSION=$2
    LATEST_VERSION=$3
    UPDATE_STATUS=$4
    PATCH_LIST=$5
    local COMPOSER_OUTPUT=
    if [ "$UPDATE_STATUS" == "update-possible" ]; then
      COMPOSER_OUTPUT=$(composer require "$PROJECT_NAME":"$LATEST_VERSION" -W -q -n --ignore-platform-reqs)
    else
      COMPOSER_OUTPUT=$(composer update "$PROJECT_NAME" -W -q -n --ignore-platform-reqs)
    fi

    local EXIT_CODE=$?;

    case "$EXIT_CODE" in
      0)
        echo $RESULT_SUCCESS
      ;;
      1)
        # Do sanity check before comparing patches.
        if [[ $LATEST_VERSION == dev-* ]]; then
          RESULT=$RESULT_SUCCESS
        elif grep -q "$LATEST_VERSION" composer.lock; then
          RESULT=$RESULT_SUCCESS
        else
          RESULT=$RESULT_GENERIC_ERROR
        fi

        # Compare list of patches. We need to compare them, because if previous
        # one failed, composer install is going to throw error.
        if [ -n "$PATCH_LIST" ]; then
          local PATCH=
          for PATCH in $(echo "${PATCH_LIST}" | jq -c '.[]'); do
             if [ "$(grep -s -c -ic "$PATCH" "$COMPOSER_OUTPUT")" != 0 ]; then
               RESULT="$PATCH"
             else
               continue
             fi
          done
        fi
        echo "$RESULT"
      ;;
      2)
        echo "$RESULT_DEPENDENCY_ERROR"
      ;;
      *)
        echo "$RESULT_UNKNOWN"
    esac
}

# Set default values.
SUMMARY_FILE=
UPDATE_TYPE="semver-safe-update"
UPDATE_EXCLUDE=
UPDATE_CORE=true

# Determine if we're running inside GitHub actions.
GITHUB_RUNNING_ACTION=$GITHUB_ACTIONS

# For GitHub actions use inputs.
if [ "$GITHUB_RUNNING_ACTION" == true ]
then
  UPDATE_TYPE=${INPUT_UPDATE_TYPE}
  UPDATE_CORE=${INPUT_UPDATE_CORE}
  UPDATE_EXCLUDE=${INPUT_UPDATE_EXCLUDE}
fi

# Go through any flags available.
while getopts "h:t:c:e:o:" options; do
  case "${options}" in
  h)
    echo usage
    exit
    ;;
  t)
    UPDATE_TYPE=${OPTARG}
    ;;
  c)
    UPDATE_CORE=${OPTARG}
    ;;
  e)
    UPDATE_EXCLUDE=${OPTARG}
    ;;
  o)
    SUMMARY_FILE=${OPTARG}
    ;;
  :)
    echo "Error: -${OPTARG} requires an argument."
    ;;
  *)
    exit_error
    ;;
  esac
done

# Perform validations of shell scripts arguments and requirements to run script.
validate_options "$UPDATE_TYPE" "$UPDATE_CORE" "$UPDATE_EXCLUDE" "$SUMMARY_FILE"
validate_requirements

# If we have list of exclude modules, convert it to loop list.
if [ -n "$UPDATE_EXCLUDE" ]; then
  UPDATE_EXCLUDE="${UPDATE_EXCLUDE//,/ }"
fi

# Get full composer content for later usage.
COMPOSER_CONTENTS=$(< composer.json);

SUMMARY_INSTRUCTIONS="### Automated Drupal update summary\n"

# Define variable for writing summary table.
SUMMARY_OUTPUT_TABLE="| Project name | Old version | Proposed version | Status | Patches | Abandoned |\n"
SUMMARY_OUTPUT_TABLE+="| ------ | ------ | ------ | ------ | ------ | ------ |\n"
# Read composer output. Remove whitespaces - jq 1.5 can break while parsing.
UPDATES=$(composer outdated "drupal/*" -f json -D --locked --ignore-platform-reqs | sed -r 's/\s+//g');

for UPDATE in $(echo "${UPDATES}" | jq -c '.locked[]'); do
  PROJECT_NAME=$(echo "${UPDATE}" | jq '."name"' | sed "s/\"//g")
  PROJECT_URL=$(echo "${UPDATE}" | jq '."homepage"' | sed "s/\"//g")
  if [ -z "$PROJECT_URL" ] || [ "$PROJECT_URL" == null ]; then
    PROJECT_URL="https://www.drupal.org/project/drupal"
  fi
  CURRENT_VERSION=$(echo "${UPDATE}" | jq '."version"' | sed "s/\"//g")
  LATEST_VERSION=$(echo "${UPDATE}" | jq '."latest"' | sed "s/\"//g")
  UPDATE_STATUS=$(echo "${UPDATE}" | jq '."latest-status"' | sed "s/\"//g")
  ABANDONED=$(echo "${UPDATE}" | jq '."abandoned"' | sed "s/\"//g")
  PATCHES=$(echo "$COMPOSER_CONTENTS" | jq '.extra.patches."'"$PROJECT_NAME"'" | length')

  PATCH_LIST=
  if [ "$PATCHES" -gt 0 ]; then
    PATCH_LIST=$(echo "$COMPOSER_CONTENTS" | jq '.extra.patches."'"$PROJECT_NAME"'"')
  fi

  PROJECT_RELEASE_URL=$PROJECT_URL
  if [[ $LATEST_VERSION != dev-* ]]; then
     PROJECT_RELEASE_URL=$PROJECT_URL"/releases/"$LATEST_VERSION
  fi

  RESULT=$RESULT_SKIP

  # Go through excluded packages and skip them.
  if [ -n "$UPDATE_EXCLUDE" ]; then
    for EXCLUDE in $UPDATE_EXCLUDE
    do
      if [ "$PROJECT_NAME" == "drupal/$EXCLUDE" ]; then
       SUMMARY_OUTPUT_TABLE+="| [${PROJECT_NAME}](${PROJECT_URL}) | ${CURRENT_VERSION} | [${LATEST_VERSION}]($PROJECT_RELEASE_URL) | $RESULT | $PATCHES | $ABANDONED |\n"
       continue
      fi
    done
  fi

  # If we need to skip Drupal core updates.
  # Still write latest version for summary table.
  if [ "$UPDATE_CORE" == false ]; then
    if [[ "$PROJECT_NAME" =~ drupal/core-* ]] || [ "$PROJECT_NAME" = "drupal/core" ]; then
      echo "Skipping upgrades for $PROJECT_NAME"
      SUMMARY_OUTPUT_TABLE+="| [${PROJECT_NAME}](${PROJECT_URL}) | ${CURRENT_VERSION} | [${LATEST_VERSION}]($PROJECT_RELEASE_URL) | $RESULT | $PATCHES | $ABANDONED |\n"
      continue
    fi
  fi

  if [ "$UPDATE_TYPE" == 'major' ]; then
    echo "Update $PROJECT_NAME from $CURRENT_VERSION to $LATEST_VERSION"
    RESULT=$(update_project "$PROJECT_NAME" "$CURRENT_VERSION" "$LATEST_VERSION" "$UPDATE_STATUS", "$PATCH_LIST")
  else
    if [ "$UPDATE_STATUS" == "$UPDATE_TYPE" ]; then
      echo "Update $PROJECT_NAME from $CURRENT_VERSION to $LATEST_VERSION"
      RESULT=$(update_project "$PROJECT_NAME" "$CURRENT_VERSION" "$LATEST_VERSION" "$UPDATE_STATUS", "$PATCH_LIST")
    else
      echo "Skipping upgrades for $PROJECT_NAME"
    fi
  fi

  # Write specific cases in highlights summary report.
  if [ ${#RESULT} -gt 20 ]; then
    SUMMARY_INSTRUCTIONS+="- **$PROJECT_NAME** had failure during applying of patch: *$RESULT*.\n"
    RESULT=$RESULT_PATCH_FAILURE
  fi

  if [ "$RESULT" == "$RESULT_DEPENDENCY_ERROR" ]; then
    SUMMARY_INSTRUCTIONS+="- **$PROJECT_NAME** had unresolved dependency.\n"
  fi

  SUMMARY_OUTPUT_TABLE+="| [${PROJECT_NAME}](${PROJECT_URL}) | ${CURRENT_VERSION} | [${LATEST_VERSION}]($PROJECT_RELEASE_URL) | $RESULT | $PATCHES | $ABANDONED |\n"
done

SUMMARY_INSTRUCTIONS+="\n$SUMMARY_OUTPUT_TABLE"

# For GitHub actions use GitHub step summary and environment variable DRUPAL_UPDATES_TABLE.
if [ "$GITHUB_RUNNING_ACTION" == true ]; then
  echo -e "$SUMMARY_INSTRUCTIONS" >> "$GITHUB_STEP_SUMMARY"
  {
    echo 'DRUPAL_UPDATES_TABLE<<EOF'
    cat "$GITHUB_STEP_SUMMARY"
    echo 'EOF'
  } >>"$GITHUB_ENV"
else
  echo -e "$SUMMARY_INSTRUCTIONS"
fi

# If we have summary file.
if [ -n "$SUMMARY_FILE" ]; then
  if [ ! -f "$SUMMARY_FILE" ]; then
    touch "$SUMMARY_FILE"
  fi
  echo -e "$SUMMARY_INSTRUCTIONS" > "$SUMMARY_FILE"
fi