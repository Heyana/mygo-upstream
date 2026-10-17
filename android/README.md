# mygo Android 宿主工程

让 Go 侧产出的 `libmygo.so` 被加载，并在 `SurfaceView` 上渲染。

面（facet）：`mygo/android/**`（整条线独占，含 build/ 产物目录）。
本工程**不改** `mygo/` 下任何 `.go` 文件、`scripts/`、`AGENTS.md`。

---

## 0. 实测环境（本机，命令原始输出见产出报告 `.workbuddy/pe-jobs-20261012/android-wave2/T2B.md`）

| 项 | 实测值 |
|---|---|
| Android SDK | `C:\Users\hzy\AppData\Local\Android\Sdk` |
| NDK | `28.2.13676358` |
| build-tools | `35.0.0` / `36.0.0` / `36.1.0` / `37.0.0` |
| platforms | `android-34` / `android-35` / `android-36` / `android-36.1` |
| platform-tools | `adb.exe` 存在 |
| emulator | 存在 |
| JDK | `C:\Users\hzy\AppData\Local\Programs\Microsoft\jdk-17.0.10.7-hotspot`（Microsoft OpenJDK 17.0.10） |
| **cmdline-tools** | **该目录完全不存在**（不是「空」）⇒ **没有 sdkmanager** |
| **gradle（PATH 上）** | **没有** `which gradle` 无结果；`JAVA_HOME` 也未设置 |
| **gradle（实际可用）** | **有！** `~/.gradle/wrapper/dists/gradle-9.1.0-all/` 下是**完整解压**产物（带 `gradle-9.1.0-all.zip.ok`），wrapper 可直接命中缓存，**不需要联网下载 Gradle** |

### ⚠️ 前提修正（任务书说「没有 gradle」）

任务书写「本机没有 sdkmanager / gradle」。**实测结论分两半**：

* **sdkmanager：确认没有**，而且成因比任务书更彻底 —— `cmdline-tools` 目录**不存在**（不是空目录）。
* **gradle：其实能跑**。`~/.gradle/wrapper/dists/gradle-9.1.0-all/7wzd0jkjit61aq2p43wpjgij9/gradle-9.1.0/`
  是完整解压的 Gradle 9.1.0，`bin/gradle.bat` 与 `lib/gradle-launcher-9.1.0.jar` 都在，且有 `.zip.ok` 标记。
  本工程的 `./gradlew` 指向 `distributionUrl=…gradle-9.1.0-all.zip`，哈希与缓存目录名一致 ⇒ **零下载直接跑通**。

所以本工程不是「待 Gradle 可用时即可构建」，而是**当下就能真构建**（本机已实测 `BUILD SUCCESSFUL`）。

---

## 1. 版本选择与依据

| 组件 | 取值 | 依据 |
|---|---|---|
| AGP | `9.0.1` | 官方兼容矩阵：AGP 9.0.x = 最低/默认 Gradle **9.1.0**、默认 Build Tools **36.0.0**、默认 NDK **28.2.13676358**、最低 JDK **17**、最高 API **36.1**。本机 Gradle 9.1.0 / BT 36.0.0 / NDK 28.2.13676358 / JDK 17 **四项全中**，且 `~/.gradle` 缓存里已有 `com.android.tools.build:gradle:9.0.1`。 |
| Gradle | `9.1.0` | AGP 9.0.x 的官方默认，且本机 `wrapper/dists` 已有完整解压产物（无下载）。 |
| compileSdk | `36` | 本机有 `platforms/android-36`；build-tools `36.0.0` 与之配套；AGP 9.0 最高支持 36.1，取 36 以对齐 AGP 默认 Build Tools 组合。 |
| targetSdk | `36` | 与 compileSdk 对齐。**副作用**：Android 16/API 36 会**忽略** targetSdk≥36 应用在大屏（≥600dp）上的 `android:screenOrientation` 锁定，见 §3。 |
| **minSdk** | **`24`** | ① Go 运行时在 Android 上的支持下限是 **API 21**（NDK r28 最低支持 API 也是 21）⇒ 不能低于 21；② API 21–23 上 ART 对 `.so` 加载 / JNI 静态方法 / `System.loadLibrary` 的边界行为差异大；③ API 24（Android 7.0）在 2026 年设备覆盖率近乎 100%，且免除 multidex 兼容路径 ⇒ **取 24**，用最少的兼容成本换最大的覆盖面。要压到 21 只改这一个数字。 |
| Java/Kotlin target | 17 | JDK 17 是 AGP 9.0 的最低要求。 |

### AGP 9.0 的两个破坏性默认值（本工程已按官方默认路径写）

* `android.newDsl=true` —— 旧 `applicationVariants` 等 API 不可用。
* `android.builtInKotlin=true` —— **`org.jetbrains.kotlin.android` 插件与新 DSL 不兼容**。

因此本工程的 `app/build.gradle` **故意不声明 kotlin-android 插件**，`.kt` 源码直接交给 AGP 内置 Kotlin 编译（本机实测通过，见产出报告构建日志里的 `:app:compileDebugKotlin`）。

---

## 2. 目录结构

```
android/
├── settings.gradle              pluginManagement + 版本依据注释
├── build.gradle                 根脚本（AGP 9.0.1）
├── gradle.properties            属性 / JDK 说明
├── local.properties             本机 sdk.dir（机器相关，通常应 gitignore）
├── gradlew / gradlew.bat        wrapper 脚本（从本机 flutter 缓存的官方 wrapper 复制）
├── gradle/wrapper/
│   ├── gradle-wrapper.jar       （来源见 §5 说明）
│   └── gradle-wrapper.properties  distributionUrl = gradle-9.1.0-all.zip（命中本机缓存）
└── app/
    ├── build.gradle             compileSdk 36 / minSdk 24 / targetSdk 36
    └── src/main/
        ├── AndroidManifest.xml  锁竖屏 + configChanges（含理由）
        └── java/org/mygo/
            ├── Native.java      🔴 JNI 契约类（故意用 Java，见 §4）
            ├── MygoView.kt      SurfaceView + SurfaceHolder.Callback + 事件转发
            └── MygoActivity.kt  单层 View 承载
```

### Go 侧 `.so` 放哪里

Go 侧产出后按 ABI 放到（**需要同事/构建脚本放入，当前不存在**）：

```
app/src/main/jniLibs/arm64-v8a/libmygo.so     # 以及 armeabi-v7a / x86_64 视需要
```

`Native.java` 里 `System.loadLibrary("mygo")` ⇒ 运行时加载 `libmygo.so`。

---

## 3. 屏幕方向：锁竖屏（明确选择）

`AndroidManifest.xml` 用 `android:screenOrientation="portrait"` **锁竖屏**，并声明
`configChanges="orientation|screenSize|smallestScreenSize|screenLayout|density|keyboardHidden|uiMode"`。

理由：
1. 锁竖屏 ⇒ 旋转不改配置 ⇒ **Activity 不重建** ⇒ 不触发 `surfaceDestroyed→surfaceCreated`。
   首个宿主工程先把「渲染能不能出来 + 事件转发对不对」跑通，不把生命周期噪声混进来。
2. 声明 `configChanges` 后，分屏 / 折叠屏展开 / 密度变化这类**不改方向**的配置变化也不重建
   Activity，只走 `surfaceChanged(w,h)`，正好是「尺寸变化交给 Go 侧」的语义。
3. 代价（诚实写）：横屏内容不旋转。改回可旋转只需删掉 `screenOrientation` 一行 —— **设计上不欠债**，
   因为 `MygoView` 已经正确处理 surface 重建路径。

⚠️ 已知边界：Android 16 / API 36 上，targetSdk≥36 的应用在大屏（≥600dp）设备会被平台**忽略**方向锁定。
那种设备上方向可能真的变 —— 这也正是注释里要求「surface 重建路径必须正确」而不是只靠锁屏的原因。

---

## 4. 🔴 面创建/销毁的顺序陷阱（核心实现要点）

AOSP 官方说明：**Activity 生命周期与 Surface 生命周期是两个独立状态机**。
切后台回前台的事件序列是 `surfaceDestroyed → (Activity onPause/onStop/onStart/onResume) → surfaceCreated → surfaceChanged`，
**不是** `onPause/onResume` 一对一映射。

因此 `MygoView.surfaceDestroyed()` **只转发事件，不做任何销毁 Go 侧状态的动作**
（不 detach、不 stop 渲染线程、不释放 context）。**Go 侧负责保活**。
Java 侧一旦在这里越权销毁，回前台时 Go 侧拿到的是已死的 context，表现为黑屏或崩溃。

另外：`surfaceCreated()` 之后 surface 未必有尺寸，尺寸一律以 `surfaceChanged()` / `onSizeChanged()` 为准。

**View 层级保持极简**：`setContentView(SurfaceView)` 单层，不在其上/其中再叠会改变 surface
z-order 语义的 View（否则 SurfaceView 会从「独立合成层」退化成「与父 View 同步的层」，出现闪烁/遮挡）。

### 为什么 JNI 契约类用 Java（`Native.java`）而不是 `Native.kt`

契约要求 `public final class Native` + `static { System.loadLibrary("mygo"); }` + `private static native …`。
若用 Kotlin 写，这些成员只能进 `companion object`：

* **不带** `@JvmStatic` ⇒ native 方法实际挂在 `Native$Companion` 上，JNI 符号变成
  `Java_org_mygo_Native_00024Companion_nativeOnTouch`，**与契约不符**。
* Java 的 `private static native` 生成的符号与契约**字面一一对应**，零推断风险。

任务书允许「用 Java 写则用 `Native.java`，包名类名方法名不变」，所以这里用 Java。
（`javap` 反查结果见产出报告，确认是 `public final class` + 6 个 `private static native` + `static {}`。）

---

## 5. ⚠️ 需扩契约：多指触摸无法表达

**不改契约**（契约名/参数由 Go 侧同事按同一份写死）。仅在此声明缺口：

契约里的
```java
private static native void nativeOnTouch(int action, float x, float y, long eventTimeMs);
```
**只能表达一个指针**。以下信息在契约里**无法传递**：

1. `pointerCount`（本次事件有几个手指）；
2. 每个指针各自的 `(x, y)`；
3. `pointerIndex` / `pointerId`（`actionMasked` 把 `pointerIndex` 的高 8 位丢掉了）；
4. 多指同时移动时的批量样本。

当前实现（`MygoView.onTouchEvent`）的取舍：转发 `actionMasked` + `actionIndex` 所指的**那一个**指针的
view 内坐标 + `eventTime`。也就是说 **DOWN/UP/POINTER_DOWN/POINTER_UP 的单指语义是对的，
多指手势（双指缩放、双指拖拽）会被降级为「只有主指针/被指指针」**。

**建议的扩契约方向（留给 PM 裁定，本线不自行实施）**：新增一个批量入口，例如
```java
private static native void nativeOnTouchBatch(int actionMasked, int pointerCount,
                                              float[] xs, float[] ys, int[] ids,
                                              long eventTimeMs, int actionIndex, int metaState);
```
保留原 `nativeOnTouch` 供单指快速路径使用。

---

## 6. 怎么构建

### 6.1 前置（本机必须显式给 JDK 与 SDK）

本机 `JAVA_HOME` **未设置**、PATH 上没有 `java`，所以**必须**先设：

```bash
export JAVA_HOME="C:/Users/hzy/AppData/Local/Programs/Microsoft/jdk-17.0.10.7-hotspot"
export ANDROID_HOME="C:/Users/hzy/AppData/Local/Android/Sdk"
export ANDROID_SDK_ROOT="$ANDROID_HOME"
```

（`sdk.dir` 也已写进 `local.properties`，SDK 路径本身不依赖环境变量。）

### 6.2 构建命令（本机实测可用）

```bash
/d/project/code-lab/mygo/android/gradlew -p /d/project/code-lab/mygo/android :app:assembleDebug
```

产物：`android/app/build/outputs/apk/debug/app-debug.apk`

本机实测输出（摘要，完整日志见产出报告）：

```
> Task :app:compileDebugKotlin
> Task :app:compileDebugJavaWithJavac
...
BUILD SUCCESSFUL in 39s
35 actionable tasks: 35 executed
```

### 6.3 若换一台没有 gradle 缓存的机器

`gradle-wrapper.properties` 里的 `distributionUrl` 指向官方 9.1.0，有网时 wrapper 会自行下载；
无网时把 `~/.gradle/wrapper/dists` 里对应的 Gradle 9.1.0 拷过去即可。

### 6.4 Gradle wrapper jar 的来源

本机 `sdkmanager`/`gradle` 不在 PATH（**cmdline-tools 目录不存在 ⇒ 无法用 `gradle wrapper` 生成**）。
`gradle/wrapper/gradle-wrapper.jar` 是从本机已有的一份**官方** wrapper 复制而来：

```
/c/Users/hzy/scoop/apps/flutter/current/bin/cache/artifacts/gradle_wrapper/gradle/wrapper/gradle-wrapper.jar
```

`gradlew` / `gradlew.bat` 同源。wrapper jar 只负责「定位并启动发行版」，版本无关；
真正执行的是 `gradle-wrapper.properties` 指定的 Gradle 9.1.0（本机实测 `gradlew --version` 输出 9.1.0）。

---

## 7. 怎么装到设备

```bash
ADB="/c/Users/hzy/AppData/Local/Android/Sdk/platform-tools/adb.exe"

"$ADB" devices -l                       # 本机实测：List of devices attached（空，当前无设备）
"$ADB" install -r /d/project/code-lab/mygo/android/app/build/outputs/apk/debug/app-debug.apk
"$ADB" shell am start -n org.mygo.host/org.mygo.MygoActivity
"$ADB" logcat                            # 看 Go 侧日志 / UnsatisfiedLinkError
```

若没有真机，可用 SDK 自带 emulator（本机 `Sdk/emulator` 存在），但**本机没有 `sdkmanager`
⇒ 无法用命令行创建 AVD**；需要图形界面或另装 cmdline-tools。

预期启动失败模式（当前必然出现，直到 Go 侧交付 `.so`）：

```
java.lang.UnsatisfiedLinkError: dlopen failed: library "libmygo.so" not found
  at org.mygo.Native.<clinit>(Native.java)
```

因为 `System.loadLibrary("mygo")` 在 `Native` 的静态初始化块里，而 `.so` 尚未产出。
把 Go 侧 `libmygo.so` 放进 `app/src/main/jniLibs/<abi>/` 后重新 `assembleDebug` 即可。

---

## 8. 当前缺什么（交付边界）

| 缺的东西 | 影响 | 补法 |
|---|---|---|
| Go 侧 `libmygo.so` | APK 装上去会 `UnsatisfiedLinkError`；**编译不受影响**（已实测编译通过） | Go 侧产出后放 `app/src/main/jniLibs/<abi>/` |
| `sdkmanager`（cmdline-tools 不存在） | 无法命令行创建 AVD / 装 SDK 包 | 装 `cmdline-tools;latest` 或走 Android Studio GUI |
| 连接中的设备 | 无法真正 install / 跑起来 | `adb devices` 当前为空，接真机或起模拟器 |
| 多指契约 | 双指手势无法表达 | 见 §5，需 PM 裁定扩契约 |
| 全屏 insets（API 35+ edge-to-edge） | targetSdk 36 下 Surface 可能被系统栏遮挡部分区域 | 后续按 inset 调整 View 尺寸（未做，保持最小实现） |

---

## 9. 验证命令速查

```bash
# a) 静态语法检查（带 android.jar，真实检查）
"C:/Users/hzy/AppData/Local/Programs/Microsoft/jdk-17.0.10.7-hotspot/bin/javac" \
  -encoding UTF-8 -d /tmp/t2b-jar \
  -cp "C:/Users/hzy/AppData/Local/Android/Sdk/platforms/android-36/android.jar" \
  app/src/main/java/org/mygo/Native.java

# b) 真构建
android/gradlew -p android :app:assembleDebug

# c) 设备
/c/Users/hzy/AppData/Local/Android/Sdk/platform-tools/adb.exe devices
```
