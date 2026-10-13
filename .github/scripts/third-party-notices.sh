#!/usr/bin/env bash
# Writes the licence texts of everything compiled into a binary besides this
# repository's own code: Go's standard library and every module the main
# package imports, directly or not, on any of the operating systems named.
#
#   third-party-notices.sh <output file> <main package> <goos>...
#
# The MIT and BSD licences these modules use ask for their copyright notice to
# go with every binary copy, and Apache-2.0 for its licence and NOTICE file,
# so GoReleaser runs this before it builds and puts the file in each archive.
# The list is read from the import graph at release time, so it cannot fall
# behind go.mod.
set -euo pipefail

if (($# < 3)); then
	echo "usage: $0 <output file> <main package> <goos>..." >&2
	exit 2
fi
out=$1
main=$2
shift 2

# One line per module: path, version, directory. The dependencies differ by
# operating system (golang.org/x/sys/windows, say), so take the union.
modules=$(
	for goos in "$@"; do
		GOOS=$goos go list -deps \
			-f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' \
			"$main"
	done | sort -u
)

# Everything in a module's root that states its terms: LICENSE, COPYING,
# NOTICE (Apache-2.0 asks for it) and PATENTS (the Go project's grant).
legal_files() {
	find "$1" -maxdepth 1 -type f \
		\( -iname 'licen[cs]e*' -o -iname 'copying*' -o -iname 'notice*' -o -iname 'patents*' \) |
		sort
}

rule='================================================================================'

section() { # title, url, directory
	local files
	files=$(legal_files "$3")
	if [[ -z $files ]]; then
		echo "third-party-notices: no licence file in $3 ($1)" >&2
		exit 1
	fi
	printf '%s\n%s\n%s\n' "$rule" "$1" "$2"
	while IFS= read -r f; do
		printf -- '\n--- %s\n\n' "$(basename "$f")"
		cat "$f"
	done <<<"$files"
	printf '\n'
}

{
	cat <<EOF
This binary includes the third-party software listed below, each under the
licence that follows its name. The software of this repository itself is
covered by LICENSE.

EOF
	section "Go standard library $(go env GOVERSION)" https://go.dev "$(go env GOROOT)"
	while read -r path version dir; do
		[[ -n $path ]] || continue
		if [[ -z $dir ]]; then
			echo "third-party-notices: $path $version is not downloaded" >&2
			exit 1
		fi
		section "$path $version" "https://pkg.go.dev/$path@$version" "$dir"
	done <<<"$modules"
} >"$out"

echo "third-party-notices: wrote $(grep -c "^$rule\$" "$out") components to $out" >&2
