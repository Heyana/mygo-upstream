#!/bin/bash
# 构建 FreeType + HarfBuzz 静态库（macOS 本机 / Android arm64）
# 用法: bash build.sh [macos|android]
set -euo pipefail
cd "$(dirname "$0")"
TARGET="${1:-macos}"
ROOT="$(pwd)"
SRC=src
OUT=lib/$TARGET
mkdir -p "$OUT"
NDK=~/Library/Android/sdk/ndk/28.2.13676358/toolchains/llvm/prebuilt/darwin-x86_64/bin

case "$TARGET" in
  macos)
    CC="clang -arch arm64"
    FT_CONF="--host=aarch64-apple-darwin"
    HB_CONF="-Dbackend=disabled"
    ;;
  android)
    CC="$NDK/aarch64-linux-android29-clang"
    FT_CONF="--host=aarch64-linux-android"
    HB_CONF="-Dbackend=disabled"
    ;;
esac

# FreeType：最小依赖静态库
if [ ! -f "$OUT/libfreetype.a" ]; then
  (cd $SRC/freetype-2.13.3 && \
   CC="$CC" ./configure --prefix=$(pwd)/../$OUT --disable-shared \
     --without-brotli --without-bzip2 --without-png --without-zlib --without-harfbuzz \
     $FT_CONF > /dev/null 2>&1 && \
   make -j8 > /dev/null 2>&1 && make install > /dev/null 2>&1) && echo "freetype OK"
fi

# HarfBuzz：禁 glib/icu/cairo，freetype 可选（不用，shaping 自足）
if [ ! -f "$OUT/libharfbuzz.a" ]; then
  (cd $SRC/harfbuzz-10.2.0 && \
   meson setup build-$TARGET --cross-file="$ROOT/android-arm64.ini" --prefix="$(pwd)/../$OUT" \
     -Dglib=disabled -Dicu=disabled -Dcairo=disabled -Dfreetype=disabled \
     -Dtests=disabled -Ddocs=disabled -Dbenchmark=disabled -Dintrospection=disabled \
     --default-library=static --buildtype=release > /dev/null 2>&1 && \
   ninja -C build-$TARGET install > /dev/null 2>&1) && echo "harfbuzz OK"
fi
ls -la $OUT/*.a 2>/dev/null
