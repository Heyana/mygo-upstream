#!/usr/bin/env bash
# android-run.sh — adb 装机 + 启动 Activity + 抓 logcat。
#
# 用法：
#   scripts/android-run.sh [--apk <path>] [--component <pkg>/<activity>] [--log-seconds <n>]
#
# 环境变量（默认值为实测值，均可覆盖）：
#   ANDROID_HOME / ANDROID_SDK_ROOT   SDK 根（按平台取默认：macOS=~/Library/Android/sdk）
#   MYGO_ANDROID_APK                  APK 路径（默认 android/app/build/outputs/apk/debug/app-debug.apk）
#   MYGO_ANDROID_COMPONENT            组件名 <applicationId>/<Activity>（默认 org.mygo.host/org.mygo.MygoActivity）
#   MYGO_ANDROID_LOGTAG               logcat 过滤 tag（默认 mygo）
#
# 包名/Activity 名（已实读 T2B 的宿主工程，2026-10-12）：
#   android/app/build.gradle:27                    applicationId 'org.mygo.host'
#   android/app/build.gradle:17                    namespace 'org.mygo'
#   android/app/src/main/AndroidManifest.xml:36    <activity android:name=".MygoActivity">
#   → am start 用 applicationId 作包名；.MygoActivity 相对 namespace ⇒ org.mygo.MygoActivity
#   默认组件 = org.mygo.host/org.mygo.MygoActivity；T2B 若改名，用 --component 或
#   MYGO_ANDROID_COMPONENT 覆盖（或重读上面两个文件后改这一行）。
set -u

norm() { printf '%s' "${1//\\//}"; }

# ---- 平台自适应（与 android-doctor.sh 同源）----------------------------------
case "$(uname -s 2>/dev/null)" in
  MINGW*|MSYS*|CYGWIN*) EXE=".exe"; DEF_SDK="C:/Users/hzy/AppData/Local/Android/Sdk" ;;
  Darwin)               EXE="";     DEF_SDK="$HOME/Library/Android/sdk" ;;
  *)                    EXE="";     DEF_SDK="$HOME/Android/Sdk" ;;
esac

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SDK="$(norm "${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$DEF_SDK}}")"
ADB="$SDK/platform-tools/adb$EXE"

APK="${MYGO_ANDROID_APK:-$REPO/android/app/build/outputs/apk/debug/app-debug.apk}"
LOGTAG="${MYGO_ANDROID_LOGTAG:-mygo}"
LOG_SECONDS=5
COMPONENT_ARG=""

while [ $# -gt 0 ]; do
  case "$1" in
    --apk)        APK="${2:-}"; shift 2 ;;
    --component)  COMPONENT_ARG="${2:-}"; shift 2 ;;
    --log-seconds) LOG_SECONDS="${2:-5}"; shift 2 ;;
    -h|--help)    sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) printf '未知参数：%s\n' "$1" >&2; exit 2 ;;
  esac
done

# ---- 组件（默认已按 T2B 的 Manifest/gradle 写死，见文首注释）------------------
COMPONENT="${COMPONENT_ARG:-${MYGO_ANDROID_COMPONENT:-org.mygo.host/org.mygo.MygoActivity}}"

# ---- 1) 设备检查 ------------------------------------------------------------
echo "== [1/4] adb devices =="
if [ ! -f "$ADB" ]; then
  printf '\360\237\224\264 找不到 adb：%s\n' "$ADB"
  printf '   \342\206\263 \350\241\245\357\274\232sdkmanager "platform-tools"，或设 ANDROID_HOME\n'
  exit 2
fi
"$ADB" devices
ndev="$("$ADB" devices | sed '1d' | grep -c 'device$')"
if [ "$ndev" -eq 0 ]; then
  printf '\360\237\224\264 无设备/模拟器连接，未装机。\n'
  printf '   \342\206\263 \350\241\245\357\274\232启动模拟器或插真机开 USB 调试后重跑\n'
  exit 1
fi
printf '   \342\234\205 设备数：%d\n' "$ndev"

# ---- 2) 安装 ---------------------------------------------------------------
echo "== [2/4] 安装 APK =="
if [ ! -f "$APK" ]; then
  printf '\360\237\224\264 找不到 APK：%s\n' "$APK"
  printf '   \342\206\263 \350\241\245\357\274\232先跑 scripts/android-build.sh 出 .so，再 Gradle assembleDebug；或用 --apk 指定路径\n'
  exit 2
fi
"$ADB" install -r "$APK"
irc=$?
if [ "$irc" -ne 0 ]; then
  printf '\360\237\224\264 adb install 失败（exit=%d）\n' "$irc"
  exit "$irc"
fi
printf '   \342\234\205 安装完成：%s\n' "$APK"

# ---- 3) 启动 Activity -------------------------------------------------------
echo "== [3/4] 启动 Activity：$COMPONENT =="
"$ADB" logcat -c   # 清缓冲，只看本次启动
"$ADB" shell am start -n "$COMPONENT"
src=$?
if [ "$src" -ne 0 ]; then
  printf '\360\237\224\264 am start 失败（exit=%d）—— 核对包名/Activity 名是否与 Manifest 一致\n' "$src"
  exit "$src"
fi

# ---- 4) logcat --------------------------------------------------------------
echo "== [4/4] logcat（tag=$LOGTAG，采集 ${LOG_SECONDS}s）=="
sleep "$LOG_SECONDS"
"$ADB" logcat -d -s "$LOGTAG":V AndroidRuntime:E DEBUG:E
printf '   （如需实时跟踪：%s logcat -s %s:V）\n' "$ADB" "$LOGTAG"
