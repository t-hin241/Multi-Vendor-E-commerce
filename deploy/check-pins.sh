#!/usr/bin/env bash
# Checks that every container image is pinned and that the Go toolchain is
# the same everywhere it is named.
#
#   - every FROM (Dockerfiles) and image: (compose files) is name:version@sha256:digest,
#     never a bare tag or "latest";
#   - one image name has one pinned reference across all files;
#   - workflows name no image themselves: CI takes them from the compose files
#     (job "pins"), so a bump there is what the integration tests run;
#   - every go.mod and go.work has the same go and toolchain lines, and the
#     golang builder image is that toolchain's version.
#
# Usage (repository root, Linux shell or Git Bash): bash deploy/check-pins.sh
set -euo pipefail

cd "$(dirname "$0")/.."

fail=0
problem() {
  echo "check-pins: $*" >&2
  fail=1
}

dockerfiles=(backend/gateway/Dockerfile backend/services/*/Dockerfile frontend/Dockerfile)
composefiles=(docker-compose.yml deploy/*.yml deploy/*/*.yml)

# "file<TAB>image" for every image reference.
refs=$(
  {
    awk 'toupper($1) == "FROM" { print FILENAME "\t" $2 }' "${dockerfiles[@]}"
    awk '$1 == "image:" { print FILENAME "\t" $2 }' "${composefiles[@]}"
  } | tr -d '\r'
)

digest_re='^[a-z0-9./_-]+:[A-Za-z0-9._-]+@sha256:[0-9a-f]{64}$'
while IFS=$'\t' read -r file ref; do
  if [[ ! "$ref" =~ $digest_re ]]; then
    problem "$file: $ref is not pinned as name:version@sha256:digest"
  elif [[ "$ref" == *:latest@* ]]; then
    problem "$file: $ref uses the latest tag; pin a version"
  fi
done <<<"$refs"

# One name, one reference.
while read -r name; do
  count=$(cut -f2 <<<"$refs" | awk -F'[:@]' -v n="$name" '$1 == n' | sort -u | wc -l)
  if [ "$count" -gt 1 ]; then
    problem "$name is pinned to $count different references:"
    awk -F'\t' -v n="$name" 'index($2, n ":") == 1' <<<"$refs" >&2
  fi
done < <(cut -f2 <<<"$refs" | awk -F'[:@]' '{ print $1 }' | sort -u)

# Workflows take their images from the compose files.
if grep -nE '^[[:space:]]*image:[[:space:]]*[^$[:space:]]' .github/workflows/*.yml; then
  problem "a workflow names an image directly; read it from the compose files (job pins)"
fi

# Go: the same go/toolchain lines in every module and the workspace.
gomods=(backend/go.work backend/gateway/go.mod backend/pkg/go.mod backend/services/*/go.mod)
go_lines=$(for f in "${gomods[@]}"; do tr -d '\r' <"$f" | awk '$1 == "go" { print $2 }'; done | sort -u)
toolchains=$(for f in "${gomods[@]}"; do tr -d '\r' <"$f" | awk '$1 == "toolchain" { print $2 }'; done | sort -u)
missing=$(for f in "${gomods[@]}"; do tr -d '\r' <"$f" | grep -q '^toolchain ' || echo "$f"; done)

if [ "$(wc -l <<<"$go_lines")" -ne 1 ]; then
  problem "go directives differ: $(echo $go_lines)"
fi
if [ -n "$missing" ]; then
  problem "no toolchain line in: $(echo $missing)"
fi
if [ "$(wc -l <<<"$toolchains")" -ne 1 ]; then
  problem "toolchain lines differ: $(echo $toolchains)"
fi

toolchain=${toolchains#go}
builder_versions=$(cut -f2 <<<"$refs" | awk -F'[:@]' '$1 == "golang" { split($2, v, "-"); print v[1] }' | sort -u)
for v in $builder_versions; do
  if [ "$v" != "$toolchain" ]; then
    problem "golang builder image is $v but go.mod says toolchain go$toolchain;" \
      "update both together (go mod edit -toolchain / go work edit -toolchain)"
  fi
done

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "check-pins: $(wc -l <<<"$refs") image references pinned; Go toolchain go$toolchain everywhere"
