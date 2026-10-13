#!/usr/bin/env bash
# Prints the tag the release workflow should publish for HEAD, or nothing when
# no release is due.
#
# The bump comes from the conventional-commit subjects on main's first-parent
# line since the last tag. Pull requests land as merge commits titled after the
# pull request, so that line holds one subject per pull request and none of the
# commits made on the branch along the way.
#
#   feat           minor
#   fix, perf      patch
#   type!: or a    major, or minor while the version is still 0.x
#   BREAKING CHANGE: line in the body
#
# Anything else (docs, chore, ci, test, refactor, style, build) releases
# nothing. An argument of patch, minor or major forces that bump instead.
#
# When HEAD already carries a version tag, that tag is printed, so a rerun of a
# release that failed part way publishes the same version rather than skipping.
set -euo pipefail

force=${1:-}
case $force in
'' | auto) force= ;;
patch | minor | major) ;;
*)
	echo "next-version: unknown bump '$force'" >&2
	exit 2
	;;
esac

semver='v[0-9]*.[0-9]*.[0-9]*'

if here=$(git describe --tags --exact-match --match "$semver" HEAD 2>/dev/null); then
	echo "$here"
	exit 0
fi

if last=$(git describe --tags --abbrev=0 --match "$semver" HEAD 2>/dev/null); then
	range="$last..HEAD"
else
	last=v0.0.0
	range=HEAD
fi

# 0 nothing, 1 patch, 2 minor, 3 major.
rank=0
raise() { ((rank < $1)) && rank=$1 || true; }

while IFS= read -r subject; do
	if [[ $subject =~ ^[a-z]+(\([^\)]*\))?!: ]]; then
		raise 3
	elif [[ $subject =~ ^feat(\([^\)]*\))?: ]]; then
		raise 2
	elif [[ $subject =~ ^(fix|perf)(\([^\)]*\))?: ]]; then
		raise 1
	fi
done < <(git log --first-parent --format=%s "$range")

if git log --first-parent --format=%b "$range" | grep -qE '^BREAKING[ -]CHANGE:'; then
	raise 3
fi

case $force in
patch) rank=1 ;;
minor) rank=2 ;;
major) rank=3 ;;
esac

((rank > 0)) || exit 0

IFS=. read -r major minor patch <<<"${last#v}"

# Before 1.0 a breaking change moves the minor version; 1.0 is a decision for
# a person, so only a forced major bump makes it.
if ((rank == 3 && major == 0)) && [[ $force != major ]]; then
	rank=2
fi

case $rank in
3) echo "v$((major + 1)).0.0" ;;
2) echo "v$major.$((minor + 1)).0" ;;
1) echo "v$major.$minor.$((patch + 1))" ;;
esac
