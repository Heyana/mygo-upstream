package org.mygo

import android.content.Context
import android.view.KeyEvent
import android.view.MotionEvent
import android.view.SurfaceHolder
import android.view.SurfaceView
import android.text.InputType
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputConnection

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
 * ============================================================================
 * 多指：走 MygoInput，不再走 Native.onTouch
 * ============================================================================
 * Native.onTouch(int action, float x, float y, long eventTimeMs) 是**单点**契约：
 * 它只能表达一个指针，多指的 pointerCount / 各指坐标 / pointer id 传不下去。
 * 所以触摸与按键改由 MygoInput 整事件转发（一次调用带整组指针 —— 见 MygoInput.kt），
 * Go 侧再映射成 platform.SurfaceEvent：多指的完整信息在那一层作为契约之外的
 * payload 产出，方案见产出文档 T32.md。
 *
 * 🔴 为什么是「替换」而不是「同时转发」：两条通道同时喂同一批事件，等 PM 收尾把
 *    两边都接到内容上时，同一次按下会被投递两次。一件事一个入口。
 *    Native.onTouch / Native.onKey 本身没动（那是单点契约，仍可用）；若要退回旧路，
 *    把下面两处 MygoInput 调用换回 Native.onTouch / Native.onKey 即可。
 *    面生命周期（surfaceCreated/Changed/Destroyed、onSizeChanged）仍走 Native。
 * · 滚动：鼠标/触控板的 ACTION_SCROLL 在 onGenericMotionEvent 里，与触摸同一条
 *   MygoInput 通道（都是 MotionEvent，字段口径一致）。
 */
class MygoView(context: Context) : SurfaceView(context), SurfaceHolder.Callback {

    init {
        // 自己当回调接收者；不要在 Activity 里另外注册，避免一份 surface 两个回调节点。
        holder.addCallback(this)
        isFocusable = true
        isFocusableInTouchMode = true
        // 把承载文字输入的 View 交给 Go 侧（Java → Go，一次性登记）：Go 才能在自己那侧的
        // 可编辑元素拿到键盘时唤起软键盘。必须在第一次要 InputConnection 之前登记，
        // 所以放 init 而不是 onCreateInputConnection（那时系统还没被要求弹键盘）。
        MygoIme.bindView(this)
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
        // 触摸坐标是 view 像素，Go 侧要换算成 DIP；密度的唯一来源就是这里。
        // 与 onSurfaceChanged 同一个值一起报过去，两者不会不一致。
        MygoInput.onDisplayInfo(resources.displayMetrics.densityDpi)
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
        // 整个事件交给 MygoInput：actionMasked、actionIndex、全部指针的 id 与
        // view 内坐标都在那一趟里过去（多指可见 —— 见文件头「多指」一段）。
        MygoInput.onTouchEvent(event)
        return true // 消费掉，避免系统再做手势判定
    }

    /**
     * 鼠标/触控板的滚轮走这里（onTouchEvent 收不到 ACTION_SCROLL）。
     * ACTION_SCROLL 同样是 MotionEvent，字段口径一致，交给同一条通道。
     */
    override fun onGenericMotionEvent(event: MotionEvent): Boolean {
        if (event.actionMasked == MotionEvent.ACTION_SCROLL) {
            MygoInput.onTouchEvent(event)
            return true
        }
        return super.onGenericMotionEvent(event)
    }

    // ─────────────────── 按键 ───────────────────

    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean {
        MygoInput.onKeyEvent(event)
        return true
    }

    override fun onKeyUp(keyCode: Int, event: KeyEvent): Boolean {
        MygoInput.onKeyEvent(event)
        return true
    }

    // ─────────────────── 文字输入（IME）────────────────────────────────────────
    // 真正的编辑状态在 Go 侧：本 View 不持有文本，只把系统输入法接到 MygoImeConnection。
    // 文本、光标、选中、预编辑都在 Go 侧那个可编辑元素的 TextInputClient 里
    // （internal/android/backend/textinput.go 是两边语义的翻译）。

    /** 本 View 是文字编辑器：系统据此才会为它建 InputConnection 并派发 IME 事件。 */
    override fun onCheckIsTextEditor(): Boolean = true

    override fun onCreateInputConnection(outAttrs: EditorInfo): InputConnection {
        // 光标/选中的初值从 Go 侧取；取不到（-1，表示当前没有可编辑元素）时给 0，
        // 输入法至少不会拿到越界的位置。之后每次编辑后输入法都会用
        // getTextBeforeCursor / getTextAfterCursor / getSelectedText 重新问。
        val start = MygoIme.nativeSelectionStart()
        val end = MygoIme.nativeSelectionEnd()
        outAttrs.initialSelStart = if (start >= 0) start else 0
        outAttrs.initialSelEnd = if (end >= 0) end else 0
        // 多行文本 + 关掉输入法自己的建议/自动纠正：文本、编辑策略与撤销历史属于内容
        // （Go 侧），输入法只负责送键与送候选。
        outAttrs.inputType = InputType.TYPE_CLASS_TEXT or
            InputType.TYPE_TEXT_FLAG_MULTI_LINE or
            InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
        // 不要全屏 IME；多行编辑器里 Enter 就是换行，不要给它一个「动作」。
        outAttrs.imeOptions = EditorInfo.IME_FLAG_NO_FULLSCREEN or EditorInfo.IME_ACTION_NONE
        return MygoImeConnection(this)
    }

    /** 由 Activity 在 setContentView 之后调用，确保能收到按键事件。 */
    fun grabInputFocus() {
        requestFocus()
    }
}
