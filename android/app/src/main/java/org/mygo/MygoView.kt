package org.mygo

import android.content.Context
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.SurfaceHolder
import android.view.SurfaceView

/**
 * 承载 Go 侧渲染的 SurfaceView。
 *
 * ============================================================================
 * 🔴 面创建/销毁的顺序陷阱（本文件最重要的一段注释）
 * ============================================================================
 * AOSP 官方说明：**Activity 的生命周期与 Surface 的生命周期是两个独立的状态机**
 * （参见 SurfaceHolder.Callback 文档："the surface is created and destroyed at
 * different times than the activity"）。因此：
 *
 *   · 切后台再回前台，事件序列是 surfaceDestroyed → (Activity onPause/onStop/onStart/onResume)
 *     → surfaceCreated → surfaceChanged。**不是** onPause/onResume 一对一映射。
 *   · 同一份 surface 可能在一个 Activity 实例内被创建/销毁多次；
 *     也可能 surface 还活着而 Activity 已经在 onStop。
 *
 * 由此推出本工程的两条硬约束：
 *   1) surfaceDestroyed() 里 **不做任何销毁 Go 侧状态的动作**（不 detach、不 stop 渲染线程、
 *      不释放 context）。Go 侧负责保活，Java 侧只做「事件转发」。Java 侧一旦在这里
 *      越权销毁，回前台时 Go 侧拿到的是一个已死的 context，表现为黑屏或崩溃。
 *   2) surfaceCreated() 之后 surface **未必**立刻可用尺寸；尺寸一律以
 *      surfaceChanged()/onSizeChanged() 为准，不在 surfaceCreated 里假设 w/h。
 *
 * ============================================================================
 * 事件转发
 * ============================================================================
 * · 触摸：坐标用 MotionEvent.getX()/getY()。这两个 API 返回的**本来就是相对本 View
 *   左上角的坐标**（view 内坐标），不能用 getRawX()/getRawY()（那是屏幕坐标）。
 *   这里显式用 getX(actionIndex)，在注释里点明这一点，避免后人误改成 raw 坐标。
 * · 按键：需要本 View 拿到焦点才会收到 onKeyDown/Up，所以构造时设
 *   focusable / focusableInTouchMode，并由 Activity 调 requestFocus()。
 *
 * ⚠️ 多指表达：契约里的 nativeOnTouch(int action, float x, float y, long eventTimeMs)
 *    **只能表达一个指针**。本文件按契约原样转发「action 所指的那个指针」，
 *    多指的 pointerCount / 每指坐标 / pointerIndex **无法表达**。
 *    按任务书要求：不改契约，改为在 README 与产出里写「需扩契约」。见 README。
 */
class MygoView(context: Context) : SurfaceView(context), SurfaceHolder.Callback {

    init {
        // 自己当回调接收者；不要在 Activity 里另外注册，避免一份 surface 两个回调节点。
        holder.addCallback(this)
        isFocusable = true
        isFocusableInTouchMode = true
        // 保持这一层 View 极简：SurfaceView 之下不再叠别的 View。
        // 🔴 不要在 setContentView(SurfaceView) 之后又往同一个容器里加会改变
        //    surface z-order 语义的 View（那会让 SurfaceView 从「独立合成层」
        //    退化成「与父 View 同步的层」，出现闪烁/遮挡）。当前实现就这么简单。
    }

    // ─────────────────── SurfaceHolder.Callback ───────────────────

    override fun surfaceCreated(holder: SurfaceHolder) {
        // 只转发。不要在这里假设尺寸，也不要在这里 init Go 渲染上下文
        // （尺寸以 surfaceChanged / onSizeChanged 为准）。
        Native.onSurfaceCreated(holder.surface)
    }

    override fun surfaceChanged(holder: SurfaceHolder, format: Int, width: Int, height: Int) {
        // dpi 用当前 display 的 densityDpi。注意：Surface 尺寸变化（分屏、折叠、旋屏）
        // 都会走到这里，Go 侧据此重建 framebuffer / 视口。
        Native.onSurfaceChanged(width, height, resources.displayMetrics.densityDpi)
    }

    override fun surfaceDestroyed(holder: SurfaceHolder) {
        // 🔴 严禁在此销毁 Go 侧 context（见文件头注释 #1）。
        // Go 侧保活；Java 侧只转发事件。
        Native.onSurfaceDestroyed()
    }

    // ─────────────────── 尺寸变化 ───────────────────

    override fun onSizeChanged(w: Int, h: Int, oldw: Int, oldh: Int) {
        super.onSizeChanged(w, h, oldw, oldh)
        // View 尺寸（含 inset/装饰偏移后的实际可用区）与 Surface 尺寸可能不同，
        // 两者都转给 Go 侧，由 Go 侧决定以哪个为准（这里不做取舍，避免丢信息）。
        Native.onSizeChanged(w, h)
    }

    // ─────────────────── 触摸 ───────────────────

    override fun onTouchEvent(event: MotionEvent): Boolean {
        // actionMasked 去掉高 8 位的 pointerIndex，只留 ACTION_* 常量部分。
        val action = event.actionMasked
        // actionIndex 是「本次 action 真正作用的那个指针」的下标：
        //   DOWN/POINTER_DOWN/UP/POINTER_UP 有效；MOVE 恒为 0。
        // ⚠️ pointerIndex 本身在契约里无法传递 ⇒ 需扩契约，见 README。
        val index = event.actionIndex
        // view 内坐标（相对本 View 左上角）。
        val x = event.getX(index)
        val y = event.getY(index)
        Native.onTouch(action, x, y, event.eventTime)
        return true // 消费掉，避免系统再做手势判定
    }

    // ─────────────────── 按键 ───────────────────

    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean {
        Native.onKey(keyCode, KeyEvent.ACTION_DOWN, event.metaState)
        return true
    }

    override fun onKeyUp(keyCode: Int, event: KeyEvent): Boolean {
        Native.onKey(keyCode, KeyEvent.ACTION_UP, event.metaState)
        return true
    }

    /** 由 Activity 在 setContentView 之后调用，确保能收到按键事件。 */
    fun grabInputFocus() {
        requestFocus()
    }
}
