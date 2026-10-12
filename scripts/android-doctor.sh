#!/usr/bin/env bash
# android-doctor.sh — 安卓构建/装机/体检工具链自检。
#
# 逐项打印 ✅ / 🔴 / ⚠️，并在缺项后给出「怎么补」。不使用 set -e：必须把全部检查
# 跑完再下结论，而不是碰到第一个失败就退出。
#
# 环境变量覆盖（默认值是本机实测值，可覆盖，不要写死到不可改）：
#   ANDROID_HOME / ANDROID_SDK_ROOT   SDK 根目录（默认 C:/Users/hzy/AppData/Local/Android/Sdk）
#   ANDROID_NDK_VERSION               NDK 版本目录名（默认 28.2.13676358，即 r28）
#   JAVA_HOME                         JDK 根目录（默认本机 Microsoft OpenJDK 17）
#   MYGO_ANDROID_CC                   C 交叉编译器（默认由 NDK + aarch64-linux-android29 推导）
#
# 退出码：0 = 关键项齐全；1 = 有 🔴 关键项缺失。
set -u

fail=0
warn=0
ok()   { printf '  \342\234\205 %s\n' "$*"; }
bad()  { printf '  \360\237\224\264 %s\n' "$*"; fail=$((fail + 1)); }
note() { printf '  \342\232\240\357\270\217  %s\n' "$*"; warn=$((warn + 1)); }
hint() { printf '     \342\206\263 \350\241\245\357\274\232%s\n' "$*"; }

norm() { printf '%s' "${1//\\//}"; }

SDK="$(norm "${ANDROID_HOME:-${ANDROID_SDK_ROOT:-C:/Users/hzy/AppData/Local/Android/Sdk}}")"
NDK_VER="${ANDROID_NDK_VERSION:-28.2.13676358}"
NDK_DIR="$SDK/ndk/$NDK_VER"
JDK="$(norm "${JAVA_HOME:-C:/Users/hzy/AppData/Local/Programs/Microsoft/jdk-17.0.10.7-hotspot}")"
HOST_TAG="windows-x86_64"
TOOLDIR="$NDK_DIR/toolchains/llvm/prebuilt/$HOST_TAG/bin"
CC="${MYGO_ANDROID_CC:-$TOOLDIR/aarch64-linux-android29-clang.cmd}"
OBJDUMP="$TOOLDIR/llvm-objdump.exe"
ADB="$SDK/platform-tools/adb.exe"
EMU="$SDK/emulator/emulator.exe"

echo "=== android-doctor ==="
echo "SDK   : $SDK"
echo "NDK   : $NDK_DIR"
echo "JDK   : $JDK"
echo "CC    : $CC"
echo

echo "[1] Android SDK 根目录"
if [ -d "$SDK" ]; then ok "存在：$SDK"; else
  bad "不存在：$SDK"
  hint "设置 ANDROID_HOME 指向 SDK，或用 Android Studio SDK Manager 安装"
fi

echo "[2] NDK（要求 r28，16KB 对齐默认 align 2**14 依赖它）"
if [ -d "$NDK_DIR" ]; then
  ok "存在：$NDK_DIR"
  if [ -f "$NDK_DIR/source.properties" ]; then
    rev="$(sed -n 's/^Pkg\.Revision *= *//p' "$NDK_DIR/source.properties" | head -1)"
    printf '     版本（source.properties Pkg.Revision）= %s\n' "$rev"
  fi
  case "$NDK_VER" in
    28.*) ok "版本目录名 $NDK_VER 属于 r28" ;;
    *)    note "版本目录名 $NDK_VER 不是 r28 —— 非 r28 不保证默认 16KB 对齐"
          hint "安装 NDK r28（如 28.2.13676358）并设 ANDROID_NDK_VERSION" ;;
  esac
else
  bad "不存在：$NDK_DIR"
  hint "sdkmanager \"ndk;28.2.13676358\"，或设 ANDROID_NDK_VERSION 指向已有 NDK"
fi

echo "[3] CC 交叉编译器"
if [ -f "$CC" ]; then ok "存在：$CC"; else
  bad "不存在：$CC"
  hint "确认 NDK 已装、HOST_TAG=windows-x86_64，或设 MYGO_ANDROID_CC 指向 aarch64-linux-android<API>-clang.cmd"
fi

echo "[4] JDK（要求 17）"
if [ -f "$JDK/bin/java" ]; then
  jver="$("$JDK/bin/java" -version 2>&1 | head -1)"
  printf '     %s\n' "$jver"
  jmaj="$(printf '%s' "$jver" | sed -n 's/.*version "\([0-9][0-9]*\).*/\1/p')"
  case "$jmaj" in
    17) ok "JDK 17" ;;
    '')  note "无法解析 JDK 主版本（原文见上）" ;;
    *)   note "JDK 主版本=$jmaj，不是 17" ; hint "设 JAVA_HOME 指向 JDK 17（Gradle/AGP 需要）" ;;
  esac
else
  bad "找不到 $JDK/bin/java"
  hint "设 JAVA_HOME 指向 JDK 17 安装目录"
fi

echo "[5] platform-tools/adb"
if [ -f "$ADB" ]; then ok "存在：$ADB"; else
  bad "不存在：$ADB"
  hint 'sdkmanager "platform-tools"'
fi

echo "[6] emulator"
if [ -f "$EMU" ]; then ok "存在：$EMU"; else
  note "不存在：$EMU（无真机时需模拟器）"
  hint 'sdkmanager "emulator" 并创建 AVD'
fi

echo "[7] platforms（要求 ≥ 31）"
if [ -d "$SDK/platforms" ]; then
  best=0
  for d in "$SDK"/platforms/android-*; do
    [ -d "$d" ] || continue
    v="${d##*/android-}"; v="${v%%.*}"
    case "$v" in ''|*[!0-9]*) continue ;; esac
    [ "$v" -gt "$best" ] && best="$v"
  done
  if [ "$best" -ge 31 ]; then ok "最高平台 android-$best（≥31）"; else
    bad "最高平台 android-$best（<31）"; hint 'sdkmanager "platforms;android-36"'
  fi
else
  bad "无 $SDK/platforms 目录"; hint 'sdkmanager "platforms;android-36"'
fi

echo "[8] build-tools"
if [ -d "$SDK/build-tools" ] && [ -n "$(ls -A "$SDK/build-tools" 2>/dev/null)" ]; then
  btl="$(ls "$SDK/build-tools" 2>/dev/null | tr '\n' ' ')"
  ok "存在：$btl"
else
  note "无 build-tools（纯 --so-only 编译不需要，但出 APK 需要）"
  hint 'sdkmanager "build-tools;36.1.0"'
fi

echo "[9] cmdline-tools（sdkmanager/avdmanager 的来源）"
if [ -d "$SDK/cmdline-tools" ] && [ -n "$(ls -A "$SDK/cmdline-tools" 2>/dev/null)" ]; then
  ok "存在且非空：$SDK/cmdline-tools"
else
  note "cmdline-tools 为空：$SDK/cmdline-tools 不存在（任务书描述为「为空」，实测目录根本不存在，效果相同：无 sdkmanager/avdmanager）"
  hint 'Android Studio → SDK Manager → SDK Tools → 勾选 Android SDK Command-line Tools'
fi

echo "[10] 连接的设备（信息项，非必需）"
if [ -f "$ADB" ]; then
  devs="$("$ADB" devices 2>/dev/null | sed '1d' | grep -v '^[[:space:]]*$')"
  if [ -z "$devs" ]; then
    note "无设备连接（装机/EGL 压测需要真机或模拟器）"
    hint "启动模拟器或插真机开 USB 调试，然后 adb devices"
  else
    ok "已连接："
    printf '%s\n' "$devs" | sed 's/^/       /'
  fi
else
  note "跳过（adb 缺失）"
fi

echo
echo "=== 结论 ==="
if [ "$fail" -eq 0 ]; then
  printf '  ✅ 关键项齐全（🔴=0, ⚠️=%d）\n' "$warn"
else
  printf '  🔴 缺 %d 个关键项（⚠️=%d），见上面 ↳ 补法\n' "$fail" "$warn"
fi
exit "$([ "$fail" -eq 0 ] && echo 0 || echo 1)"
