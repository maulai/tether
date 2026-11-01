#!/usr/bin/env bash
# Prints the version to release HEAD as, or nothing when there is none.
#
# The first release is v0.1.0. After that, each commit since the last tag
# may carry a trailer line "Release: none|patch|minor|major"; a commit
# without one counts as a patch, and the largest step wins. Nothing is
# released when every commit says none.
set -euo pipefail

if git describe --exact-match --tags --match 'v[0-9]*' HEAD >/dev/null 2>&1; then
	exit 0 # already released
fi
last=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null || true)
if [ -z "$last" ]; then
	echo v0.1.0
	exit 0
fi

level=0
while IFS= read -r -d '' msg; do
	step=1
	case "$(printf '%s\n' "$msg" | sed -n 's/^Release: *//p' | tail -n 1 | tr -d '[:space:]')" in
	none) step=0 ;;
	minor) step=2 ;;
	major) step=3 ;;
	esac
	if [ "$step" -gt "$level" ]; then level=$step; fi
done < <(git log --format='%B%x00' "$last..HEAD")

IFS=. read -r major minor patch <<<"${last#v}"
patch=${patch%%-*}
case $level in
3) echo "v$((major + 1)).0.0" ;;
2) echo "v$major.$((minor + 1)).0" ;;
1) echo "v$major.$minor.$((patch + 1))" ;;
esac
