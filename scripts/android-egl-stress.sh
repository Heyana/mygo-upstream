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
#   MYGO_EXPECT_HEX                   期望帧色（默认 1C1F29 = Go 侧 clear(0.11,0.12,0.16)）
#   MYGO_EXPECT_COVER                 期望色占比下限 %（默认 95）
#   MYGO_ANDROID_LOGTAG               logcat tag（默认 mygo）
#
# 无设备时：打印「无设备，未跑」并以 0 退出（不是脚本错误）。
set -u

norm() { printf '%s' "${1//\\//}"; }

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SDK="$(norm "${ANDROID_HOME:-${ANDROID_SDK_ROOT:-C:/Users/hzy/AppData/Local/Android/Sdk}}")"
ADB="$SDK/platform-tools/adb.exe"

OUTDIR="${MYGO_ANDROID_SHOTS:-$REPO/.mygo/android/frames}"
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

# ---- 判据 -------------------------------------------------------------------
# 🔴 判据是「像素」不是「PNG 字节数」。
#   纯色帧的 PNG 压缩率极高：我们 1440x3168 的全屏纯色帧只有约 25 KB，
#   任何字节阈值（旧默认 30000）都会把正常帧误判成空白帧（实测假红）。
# 期望色来源 —— Go 侧 internal/android/backend/surface.go:76 的
#   s.egl.clear(0.11, 0.12, 0.16, 1)
# 映射到 8 位 UNORM：0.11*255=28, 0.12*255=31, 0.16*255=41 ⇒ #1C1F29。
# 桌面是纯黑背景，所以「中心像素 = #1C1F29」只可能来自我们的 EGL 清屏。
EXPECT_HEX="${MYGO_EXPECT_HEX:-1C1F29}"
EXPECT_COVER="${MYGO_EXPECT_COVER:-95}"   # 期望色占比下限（%）

PYTHON_BIN="${MYGO_PYTHON:-}"
if [ -z "$PYTHON_BIN" ]; then
  for c in python3 python "/c/Users/hzy/.workbuddy/binaries/python/envs/default/Scripts/python.exe"; do
    if [ -x "$c" ]; then PYTHON_BIN="$c"; break; fi
  done
fi

# ---- 工具函数 ---------------------------------------------------------------
blank_suspicious=0
shots=0

# 采样一帧：stdout 为 "<r>,<g>,<b> <cover%>"；无 Python/PIL 时非零退出。
sample_frame() { # $1 = png 路径
  [ -n "$PYTHON_BIN" ] || return 1
  "$PYTHON_BIN" - "$1" "$EXPECT_HEX" <<'PY' 2>/dev/null || return 1
import sys
from PIL import Image
im = Image.open(sys.argv[1]).convert("RGB")
exp = tuple(int(sys.argv[2][i:i + 2], 16) for i in (0, 2, 4))
w, h = im.size
c = im.getpixel((w // 2, h // 2))
rows = im.getdata()
near = sum(1 for k in rows if all(abs(a - b) <= 2 for a, b in zip(k, exp)))
print("%d,%d,%d %.2f" % (c[0], c[1], c[2], 100.0 * near / (w * h)))
PY
}

frame() { # $1 = 标签
  local tag="$1" f sz probe rgb cover
  f="$OUTDIR/$tag.png"
  "$ADB" exec-out screencap -p > "$f" 2>/dev/null
  if [ ! -s "$f" ]; then
    printf '      ð´ %s：截图失败（空文件）
' "$tag"
    blank_suspicious=$((blank_suspicious + 1)); return
  fi
  sz="$(wc -c < "$f" | tr -d ' ')"
  shots=$((shots + 1))

  if probe="$(sample_frame "$f")"; then
    rgb="${probe%% *}"; cover="${probe##* }"
    # awk 比 bc 稳（bc 在 Git Bash 上不一定有）
    if awk -v c="$cover" -v m="$EXPECT_COVER" 'BEGIN { exit !(c >= m) }'; then
      printf '      â %s：中心 %s，期望色 #%s 占比 %s%%
' "$tag" "$rgb" "$EXPECT_HEX" "$cover"
    else
      printf '      â ï¸  %s：中心 %s，期望色占比 %s%% < %s%% → 非我方帧（PNG %s 字节）
'         "$tag" "$rgb" "$cover" "$EXPECT_COVER" "$sz"
      blank_suspicious=$((blank_suspicious + 1))
    fi
  else
    # 回退：无 Python/PIL。只能给体积启发式，且要说明它不可靠。
    printf '      --  %s：无 Python/PIL，回退体积启发式（不可靠，%s 字节）
' "$tag" "$sz"
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
  printf '   â 是否出现空白帧：否（每帧中心都是 #%s，占比均 >= %s%%）
' "$EXPECT_HEX" "$EXPECT_COVER"
  printf '      （判据 = 像素采样；可用 MYGO_EXPECT_HEX / MYGO_EXPECT_COVER 覆盖）
'
  exit 0
else
  printf '   \360\237\224\264 是否出现空白帧：是/疑似 %d 处（见上面 ⚠️/🔴 行）\n' "$blank_suspicious"
  printf '      \342\206\263 \346\237\245\357\274\232对照 T2A 的 egl.go —— 前后台切换应只重建 EGLSurface，不要丢 EGLContext\n'
  exit 1
fi
