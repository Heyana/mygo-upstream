package org.mygo

import android.app.Activity
import android.os.Bundle
import android.view.Window
import android.view.WindowManager

/**
 * 宿主 Activity：只做「挂一个 MygoView 并保持前台」。
 *
 * 设计要点：
 *  · 不在 onDestroy 里通知 Go 侧释放 —— Go 侧保活（见 MygoView 文件头注释 #1）。
 *    语义上：「Activity 没了」不等于「Go 侧渲染上下文没了」。
 *  · 不做任何会改动 SurfaceView z-order 语义的层级加工（保持 setContentView(view) 单层）。
 *  · 屏幕方向策略见 AndroidManifest.xml 的注释（锁竖屏 + configChanges）。
 */
class MygoActivity : Activity() {

    private lateinit var view: MygoView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        requestWindowFeature(Window.FEATURE_NO_TITLE)
        // 渲染场景常亮；避免用户触摸时息屏把 surface 带走。
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)

        view = MygoView(this)
        // 单层 View 层级：SurfaceView 直接作为 content view。
        setContentView(view)

        // 让 View 拿到焦点，否则 onKeyDown/onKeyUp 不会派发到它。
        view.grabInputFocus()

        // 🔴 图片服务地址不在这里传了。
        //
        // 以前是 gradle -PmygoLabList → BuildConfig.LAB_LIST → Os.setenv →
        // Go 的 os.Getenv：把编译期常量绕两个运行时弯交给 Go，还带一个
        // 「setenv 必须早于 loadLibrary」的时序雷（Go 在包 init 里读，晚一步
        // 就静默为空）。
        //
        // 现在它由 lab 自己的构建脚本编进 .so：
        //   MYGO_LAB_LIST=http://<主机>:8098 bash build.sh android
        // 见 mygo-wind-router-lab/lab/listbase_android.go。地址随网络变、
        // 不随运行变，而那个 .so 本来就是「为拿 adb 的这台机器」编的。

        // 平台能力（剪贴板/URL/通知/无障碍）的 Java 侧握手；必须在任何平台能力被使用前调用。
        MygoPlatform.install(this, view)
    }

    override fun onResume() {
        super.onResume()
        // 回到前台后焦点可能被系统收走，重新要一次；这里**不**做任何 surface 生命周期动作。
        view.grabInputFocus()
    }

    // 有意不重写 onDestroy 去释放 Go 侧状态：Go 侧负责保活。
    // 若将来需要「App 真正退出才释放」，应在 Application/进程级做，而不是 Activity 级。
}
