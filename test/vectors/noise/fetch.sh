#!/bin/sh
# Re-download the vendored Noise test vectors and verify them against the
# checked-in SHA256SUMS. Run from anywhere; operates on its own directory.
#
# SPIKE-08: the vectors are checked in so tests run offline; this script exists
# so their provenance is verifiable and refreshable.
set -eu

dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$dir"

# name  ->  URL
set -- \
  "cacophony.txt|https://raw.githubusercontent.com/centromere/cacophony/master/vectors/cacophony.txt"

for entry in "$@"; do
	name=${entry%%|*}
	url=${entry#*|}
	echo "fetching $name"
	curl -fsSL -o "$name.tmp" "$url"
	mv "$name.tmp" "$name"
done

echo "verifying against SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
	sha256sum -c SHA256SUMS
else
	shasum -a 256 -c SHA256SUMS
fi
echo "ok"
