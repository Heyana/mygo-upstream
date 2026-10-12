package org.mygo;

import android.view.Surface;

/**
 * JNI 契约类（与 Go 侧逐字对齐）。
 *
 * <p><b>为什么用 Java 而不是 Kotlin 写这个类</b>（任务书允许，且这是有意的选择）：
 * <ul>
 *   <li>契约要求的是 <code>public final class Native</code> + <code>static { System.loadLibrary("mygo"); }</code>
 *       + <code>private static native ...</code>。</li>
 *   <li>若用 Kotlin 写，这些成员只能放进 <code>companion object</code>。不带
 *       <code>@JvmStatic</code> 时，native 方法实际挂在 <code>Native$Companion</code> 上，
 *       生成的 JNI 符号会变成 <code>Java_org_mygo_Native_00024Companion_nativeOnTouch</code>，
 *       而不是 Go 侧按契约期望的 <code>Java_org_mygo_Native_nativeOnTouch</code>。</li>
 *   <li>Java 的 <code>private static native</code> 生成的符号与契约字面一一对应，零推断风险。</li>
 * </ul>
 *
 * <p>库名固定为 <code>"mygo"</code> ⇒ 运行时加载 <code>libmygo.so</code>，
 * 由 Go 侧产出后放进 <code>app/src/main/jniLibs/&lt;abi&gt;/libmygo.so</code>。
 *
 * <p>⚠️ 下面 6 个 native 方法的**名字与签名不许改**（Go 侧按同一份契约实现）。
 */
public final class Native {

    static {
        // 库名必须逐字是 "mygo"
        System.loadLibrary("mygo");
    }

    private Native() {
        // 纯静态契约类，禁止实例化
    }

    // ───────────────────────── 契约：6 个 private static native ─────────────────────────
    // 名字/参数逐字对齐 Go 侧，不许改。

    private static native void nativeOnSurfaceCreated(Surface s);

    private static native void nativeOnSurfaceChanged(int w, int h, int dpi);

    private static native void nativeOnSurfaceDestroyed();

    private static native void nativeOnTouch(int action, float x, float y, long eventTimeMs);

    private static native void nativeOnKey(int keyCode, int action, int metaState);

    private static native void nativeOnSizeChanged(int w, int h);

    // ───────────────────────── 包内包装方法（可加，不涉及契约名）─────────────────────────
    // native 方法是 private，同包的 View 需要一层包内可见的转发入口。
    // 包装方法只做转发，不改变任何语义。

    static void onSurfaceCreated(Surface s) {
        nativeOnSurfaceCreated(s);
    }

    static void onSurfaceChanged(int w, int h, int dpi) {
        nativeOnSurfaceChanged(w, h, dpi);
    }

    static void onSurfaceDestroyed() {
        nativeOnSurfaceDestroyed();
    }

    static void onTouch(int action, float x, float y, long eventTimeMs) {
        nativeOnTouch(action, x, y, eventTimeMs);
    }

    static void onKey(int keyCode, int action, int metaState) {
        nativeOnKey(keyCode, action, metaState);
    }

    static void onSizeChanged(int w, int h) {
        nativeOnSizeChanged(w, h);
    }
}
