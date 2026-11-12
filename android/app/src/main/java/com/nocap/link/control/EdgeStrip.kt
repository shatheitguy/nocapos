package com.nocap.link.control

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.graphics.PixelFormat
import android.net.Uri
import android.provider.Settings
import android.view.Gravity
import android.view.MotionEvent
import android.view.View
import android.view.WindowManager
import com.nocap.link.data.Opt
import com.nocap.link.system.CrashLog

/**
 * Opt-in: a thin, invisible strip at the top-center of the screen (just
 * under the status bar) that opens the NoCap control center when you pull
 * down on it, in any app. Needs "Display over other apps"; it only watches
 * touches that land on the strip itself and never reads the screen.
 */
@SuppressLint("StaticFieldLeak") // the strip is made with the application context and removed when switched off
object EdgeStrip {
    private var view: View? = null

    fun allowed(ctx: Context): Boolean = Settings.canDrawOverlays(ctx)

    fun permissionIntent(ctx: Context): Intent =
        Intent(Settings.ACTION_MANAGE_OVERLAY_PERMISSION, Uri.parse("package:" + ctx.packageName)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

    /** Adds or removes the strip to match Settings (and the permission). Main thread. */
    fun sync(ctx: Context) {
        val app = ctx.applicationContext
        val want = Opt.edgeSwipe.v && allowed(app)
        if (want && view == null) add(app) else if (!want && view != null) remove(app)
    }

    /** A pull counts once it travels this far down, mostly downward. */
    fun isPull(dx: Float, dy: Float, threshold: Float): Boolean = dy > threshold && dy > kotlin.math.abs(dx) * 1.2f

    @SuppressLint("ClickableViewAccessibility")
    private fun add(ctx: Context) {
        val wm = ctx.getSystemService(WindowManager::class.java) ?: return
        val dm = ctx.resources.displayMetrics
        val v = View(ctx)
        var x0 = 0f
        var y0 = 0f
        var fired = false
        val threshold = 28 * dm.density
        v.setOnTouchListener { _, e ->
            when (e.actionMasked) {
                MotionEvent.ACTION_DOWN -> { x0 = e.rawX; y0 = e.rawY; fired = false }
                MotionEvent.ACTION_MOVE -> if (!fired && isPull(e.rawX - x0, e.rawY - y0, threshold)) { fired = true; open(ctx) }
            }
            true
        }
        val lp = WindowManager.LayoutParams(
            (dm.widthPixels * 0.4f).toInt(), (14 * dm.density).toInt(),
            WindowManager.LayoutParams.TYPE_APPLICATION_OVERLAY,
            WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or WindowManager.LayoutParams.FLAG_NOT_TOUCH_MODAL,
            PixelFormat.TRANSLUCENT,
        ).apply { gravity = Gravity.TOP or Gravity.CENTER_HORIZONTAL; title = "NoCap control center edge" }
        runCatching { wm.addView(v, lp); view = v }.onFailure { CrashLog.warn("edge strip", it) }
    }

    private fun remove(ctx: Context) {
        val v = view ?: return
        view = null
        runCatching { ctx.getSystemService(WindowManager::class.java)?.removeView(v) }
    }

    private fun open(ctx: Context) {
        runCatching {
            ctx.startActivity(Intent(ctx, ControlActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_NO_ANIMATION))
        }.onFailure { CrashLog.warn("edge strip open", it) }
    }
}
