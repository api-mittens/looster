#!/usr/bin/env bash
# Run in the CLANGARM64 MSYS2 environment on Windows ARM64.
# Official recipe: duckdb-go/v2 v2.10502.0 README, "Linking a Static Library".
set -euo pipefail
root=$(pwd)
source_dir=$(cygpath -u "${RUNNER_TEMP:?}")/duckdb-source
[[ $(go list -m -f '{{.Version}}' github.com/duckdb/duckdb-go/v2) == v2.10502.0 ]]
[[ $(go list -m -f '{{.Version}}' github.com/duckdb/duckdb-go-bindings) == v0.10502.0 ]]
git clone --filter=blob:none --no-checkout https://github.com/duckdb/duckdb.git "$source_dir"
git -C "$source_dir" checkout 8a5851971fae891f292c2714d86046ee018e9737
# Mirror the five official archives' built-in extensions. No runtime DuckDB DLL.
cmake -S "$source_dir" -B "$source_dir/build/release" -G Ninja \
  -DCMAKE_BUILD_TYPE=Release -DCMAKE_C_COMPILER=clang -DCMAKE_CXX_COMPILER=clang++ \
  -DBUILD_SHELL=OFF -DBUILD_UNITTESTS=OFF -DENABLE_JEMALLOC=OFF \
  '-DBUILD_EXTENSIONS=icu;json;parquet;autocomplete;tpch;tpcds' -DDUCKDB_EXPLICIT_PLATFORM=windows_arm64
cmake --build "$source_dir/build/release" --parallel 2
# Combine complete archives, preserving duplicate object basenames. This is the
# llvm-ar MRI equivalent of upstream's bundle-library target.
bundle="$source_dir/build/release/libduckdb_bundle.a"
{
  printf 'CREATE %s\n' "$(cygpath -m "$bundle")"
  find "$source_dir/build/release/src" "$source_dir/build/release/third_party" "$source_dir/build/release/extension" \
    -name '*.a' -type f -print | sort | while IFS= read -r archive; do
      printf 'ADDLIB %s\n' "$(cygpath -m "$archive")"
    done
  printf 'SAVE\nEND\n'
} | llvm-ar -M
test -s "$bundle"
# Export Windows paths so native go.exe passes usable paths to the cgo linker.
libdir=$(cygpath -m "$source_dir/build/release")
{
  echo 'CC=clang'
  echo 'CXX=clang++'
  echo 'CGO_CPPFLAGS=-DDUCKDB_STATIC_BUILD'
  echo "CGO_LDFLAGS=-L$libdir -lduckdb_bundle -lc++ -lunwind -static -lws2_32 -lwsock32 -lrstrtmgr -lbcrypt -lcrypt32 -ladvapi32 -lshell32 -lole32"
  echo 'DUCKDB_BUILD_TAGS=duckdb_use_static_lib'
  echo 'OBJDUMP=llvm-objdump'
} >> "$GITHUB_ENV"
cd "$root"
