#!/usr/bin/env bash
# gcc's -static-libstdc++ affects its implicit C++ runtime, not the explicit
# -lstdc++ emitted by duckdb-go-bindings. Replace every explicit request with
# the selected compiler's exact static archive, without making libc static.
set -euo pipefail
compiler=${LOOSTER_REAL_CC:-gcc}
args=()
for arg in "$@"; do
  case "$arg" in
    -lstdc++)
      archive=$("$compiler" -print-file-name=libstdc++.a)
      if [[ "$archive" == libstdc++.a || ! -f "$archive" ]]; then
        echo "Static C++ runtime archive is unavailable for $compiler: $archive" >&2
        exit 1
      fi
      args+=("$archive")
      ;;
    @*)
      # Do not silently bypass the replacement if a future Go linker starts
      # putting library flags in a response file. CI must then add a parser.
      echo 'Compiler response files require static runtime review' >&2
      exit 1
      ;;
    *) args+=("$arg") ;;
  esac
done
exec "$compiler" "${args[@]}"
