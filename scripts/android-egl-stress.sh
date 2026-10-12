#!/usr/bin/env bash
# android-egl-stress.sh — 前后台切换 20 次 + 旋转 10 次，抓「是否出现空白帧」。
#
# 用法：
#   scripts/android-egl-stress.sh [--fg-bg <n>] [--rotations <n>] [--component <pkg>/<activity>]
#
# 环境变量（默认值为本机实测值，均可覆盖）：
#   ANDROID_HOME / ANDROID_SDK_ROOT   SDK 根（默认 C:/Users/hzy/AppData/Local/Android/Sdk）
#   MYGO_ANDROID_COMPONENT            组件名（默认 org.mygo.host/org.mygo.MygoActivity）
#   MYGO_ANDROID_SHOTS                截图输出目录（默认 <repo>/.mygo/android/frames，已 gitignore）
#   MYGO_BLANK_BYTES                  空白帧判定的 PNG 字节阈值（默认 30000，启发式）
#   MYGO_ANDROID_LOGTAG               logcat tag（默认 mygo）
#
# 无设备时：打印「无设备，未跑」并以 0 退出（不是脚本错误）。
set -u

norm() { printf '%s' "${1//\\//}"; }

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SDK="$(norm "${ANDROID_HOME:-${ANDROID_SDK_ROOT:-C:/Users/hzy/AppData/Local/Android/Sdk}}")"
ADB="$SDK/platform-tools/adb.exe"

OUTDIR="${MYGO_ANDROID_SHOTS:-$REPO/.mygo/android/frames}"
BLANK_BYTES="${MYGO_BLANK_BYTES:-30000}"
LOGTAG="${MYGO_ANDROID_LOGTAG:-mygo}"

FG_BG=20
ROT=10
COMPONENT_ARG=""

while [ $# -gt 0 ]; do
  case "$1" in
    --fg-bg)      FG_BG="${2:-20}"; shift 2 ;;
    --rotations)  ROT="${2:-10}"; shift 2 ;;
    --component)  COMPONENT_ARG="${2:-}"; shift 2 ;;
    -h|--help)    sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) printf '未知参数：%s\n' "$1" >&2; exit 2 ;;
  esac
done

# ---- 设备检查（先做：无设备就明确报「未跑」）-------------------------------
echo "== [0] adb devices =="
if [ ! -f "$ADB" ]; then
  printf '\360\237\224\264 找不到 adb：%s → 无设备，未跑\n' "$ADB"
  printf '   \342\206\263 \350\241\245\357\274\232sdkmanager "platform-tools"，或设 ANDROID_HOME\n'
  exit 0
fi
"$ADB" devices
ndev="$("$ADB" devices | sed '1d' | grep -c 'device$')"
if [ "$ndev" -eq 0 ]; then
  printf '\360\237\224\264 无设备，未跑（EGL 压测需要真机或模拟器）\n'
  printf '   \342\206\263 \350\241\245\357\274\232启动模拟器或插真机开 USB 调试后重跑\n'
  exit 0
fi
printf '   \342\234\205 设备数：%d\n' "$ndev"

# ---- 组件（默认已按 T2B 的 Manifest/gradle 写死）----------------------------
#   android/app/build.gradle:27 applicationId 'org.mygo.host'；:17 namespace 'org.mygo'
#   android/app/src/main/AndroidManifest.xml:36 <activity android:name=".MygoActivity">
#   → org.mygo.host/org.mygo.MygoActivity
COMPONENT="${COMPONENT_ARG:-${MYGO_ANDROID_COMPONENT:-org.mygo.host/org.mygo.MygoActivity}}"
printf '组件：%s\n' "$COMPONENT"

mkdir -p "$OUTDIR"
printf '日志/TODO：本脚本用「截图 PNG 体积」+「logcat 关键字」两条启发式判空白帧；\n'
printf '          T2A/T2B 落地后可加一个稳定的 logcat 标记（tag=%s）提升判定精度。\n' "$LOGTAG"

# ---- 工具函数 ---------------------------------------------------------------
blank_suspicious=0
shots=0

frame() { # $1 = 标签
  local tag="$1" f="$OUTDIR/$tag.png" sz
  "$ADB" exec-out screencap -p > "$f" 2>/dev/null
  if [ ! -s "$f" ]; then
    printf '      \360\237\224\264 %s：截图失败（空文件）\n' "$tag"
    blank_suspicious=$((blank_suspicious + 1)); return
  fi
  sz="$(wc -c < "$f" | tr -d ' ')"
  shots=$((shots + 1))
  if [ "$sz" -lt "$BLANK_BYTES" ]; then
    printf '      \342\232\240\357\270\217  %s：PNG %s 字节 < 阈值 %s → 疑似空白帧\n' "$tag" "$sz" "$BLANK_BYTES"
    blank_suspicious=$((blank_suspicious + 1))
  else
    printf '      \342\234\205 %s：PNG %s 字节\n' "$tag" "$sz"
  fi
}

check_logcat() { # $1 = 标签
  local tag="$1" hits
  hits="$("$ADB" logcat -d 2>/dev/null | grep -iE 'EGL_BAD|eglSwapBuffers.*(fail|error)|Failed to (create|make).*EGL|blank frame' | tail -5)"
  if [ -n "$hits" ]; then
    printf '      \360\237\224\264 %s：logcat 命中 EGL 异常：\n' "$tag"
    printf '%s\n' "$hits" | sed 's/^/          /'
    blank_suspicious=$((blank_suspicious + 1))
  fi
}

echo
echo "== [1/2] 前后台切换 x$FG_BG =="
"$ADB" logcat -c
"$ADB" shell am start -n "$COMPONENT" >/dev/null
"$ADB" shell settings put system accelerometer_rotation 0 >/dev/null 2>&1
for i in $(seq 1 "$FG_BG"); do
  printf '   --- 轮 %d/%d ---\n' "$i" "$FG_BG"
  # 切后台（回桌面）
  "$ADB" shell input keyevent KEYCODE_HOME >/dev/null 2>&1
  sleep 1
  # 切回前台
  "$ADB" shell am start -n "$COMPONENT" >/dev/null 2>&1
  sleep 1
  frame "fg_bg_$(printf '%02d' "$i")"
  check_logcat "fg_bg_$(printf '%02d' "$i")"
done

echo
echo "== [2/2] 旋转 x$ROT =="
"$ADB" shell am start -n "$COMPONENT" >/dev/null 2>&1
for i in $(seq 1 "$ROT"); do
  if [ $((i % 2)) -eq 1 ]; then rot=1; else rot=0; fi   # 1=横 0=竖
  printf '   --- 轮 %d/%d（user_rotation=%d）---\n' "$i" "$ROT" "$rot"
  "$ADB" shell settings put system accelerometer_rotation 0 >/dev/null 2>&1
  "$ADB" shell settings put system user_rotation "$rot" >/dev/null 2>&1
  sleep 1
  frame "rot_$(printf '%02d' "$i")"
  check_logcat "rot_$(printf '%02d' "$i")"
done

# ---- 汇总 -------------------------------------------------------------------
echo
echo "=== 汇总 ==="
printf '   截图数：%d（目录：%s）\n' "$shots" "$OUTDIR"
if [ "$blank_suspicious" -eq 0 ]; then
  printf '   \342\234\205 是否出现空白帧：否（无低于阈值的帧，无 EGL 异常日志）\n'
  printf '      （判定为启发式；PNG 阈值 %s 字节，可用 MYGO_BLANK_BYTES 调整）\n' "$BLANK_BYTES"
  exit 0
else
  printf '   \360\237\224\264 是否出现空白帧：是/疑似 %d 处（见上面 ⚠️/🔴 行）\n' "$blank_suspicious"
  printf '      \342\206\263 \346\237\245\357\274\232对照 T2A 的 egl.go —— 前后台切换应只重建 EGLSurface，不要丢 EGLContext\n'
  exit 1
fi
