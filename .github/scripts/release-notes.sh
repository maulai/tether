#!/usr/bin/env bash
# Prints the release notes for the commits since the last tag, as Markdown.
#
# A commit says what a release brings in trailer lines at the end of its
# message, next to Release: and Co-Authored-By:
#
#   Breaking: Handlers take path parameters as arguments; PathValue is gone.
#   Added: Problem gives a client-side test per code.
#   Fixed: A panic in a handler answers 500 instead of breaking off.
#
# Each key may appear several times; a long value continues on lines that
# start with a space. The notes list them by kind, Breaking first, oldest
# commit first. A release whose commits carry none of them lists the
# subjects of its commits instead, so the notes are never empty.
#
# Usage: release-notes.sh <new tag> [repository URL]
set -euo pipefail

tag=${1:?usage: release-notes.sh <new tag> [repository URL]}
repo=${2:-}
last=$(git describe --tags --abbrev=0 --match 'v[0-9]*' HEAD 2>/dev/null || true)
range=HEAD
if [ -n "$last" ]; then range="$last..HEAD"; fi

values() {
	git log --reverse --no-merges --format="%(trailers:key=$1,valueonly,unfold)" "$range" | sed '/^[[:space:]]*$/d'
}

section() {
	local title=$1 key=$2 lines
	lines=$(values "$key")
	if [ -n "$lines" ]; then
		printf '## %s\n\n' "$title"
		printf '%s\n' "$lines" | sed 's/^/- /'
		printf '\n'
		found=1
	fi
}

found=0
section "Breaking changes" Breaking
section "Added" Added
section "Fixed" Fixed
if [ "$found" = 0 ]; then
	printf '## Changes\n\n'
	git log --reverse --no-merges --format='- %s' "$range"
	printf '\n'
fi
if [ -n "$repo" ] && [ -n "$last" ]; then
	printf '**Full changelog:** %s/compare/%s...%s\n' "$repo" "$last" "$tag"
fi
