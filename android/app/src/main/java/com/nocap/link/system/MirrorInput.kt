package com.nocap.link.system

import android.accessibilityservice.AccessibilityService
import android.accessibilityservice.GestureDescription
import android.content.Context
import android.content.Intent
import android.graphics.Path
import android.hardware.display.DisplayManager
import android.media.AudioManager
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.util.DisplayMetrics
import android.view.Display
import android.view.View
import android.view.accessibility.AccessibilityNodeInfo
import com.nocap.link.mirror.Protocol
import com.nocap.link.mirror.Protocol.Control

/**
 * Taps, swipes, keys and typing from a NoCap OS window, while this phone is
 * mirrored. It goes through the opt-in [LockService] (the same accessibility
 * helper as "double-tap to lock"): without it, mirroring is view-only.
 */
object MirrorInput {
    /** Whether NoCap OS can control this phone right now. */
    fun available(): Boolean = LockService.instance != null

    fun accessibilitySettings(ctx: Context) {
        runCatching { ctx.startActivity(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
    }

    /** The screen's real size in its current rotation. */
    @Suppress("DEPRECATION")
    fun screenSize(ctx: Context): Pair<Int, Int> {
        val d = ctx.getSystemService(DisplayManager::class.java)?.getDisplay(Display.DEFAULT_DISPLAY) ?: return 1080 to 1920
        val m = DisplayMetrics()
        d.getRealMetrics(m)
        return m.widthPixels to m.heightPixels
    }

    /** Does one input message; false when it couldn't (no helper, no field…). */
    fun handle(ctx: Context, c: Control): Boolean {
        if (c is Control.Volume) {
            val am = ctx.getSystemService(AudioManager::class.java) ?: return false
            return runCatching {
                am.adjustStreamVolume(AudioManager.STREAM_MUSIC, if (c.up) AudioManager.ADJUST_RAISE else AudioManager.ADJUST_LOWER, AudioManager.FLAG_SHOW_UI)
            }.isSuccess
        }
        val svc = LockService.instance ?: return false
        val (w, h) = screenSize(ctx)
        return runCatching {
            when (c) {
                is Control.Tap -> stroke(svc, Protocol.toScreen(c.x, c.y, w, h), null, 40)
                is Control.LongPress -> stroke(svc, Protocol.toScreen(c.x, c.y, w, h), null, 650)
                is Control.Swipe -> stroke(svc, Protocol.toScreen(c.x1, c.y1, w, h), Protocol.toScreen(c.x2, c.y2, w, h), c.ms)
                is Control.Scroll -> {
                    val p = Protocol.scrollSwipe(c, w, h)
                    stroke(svc, p[0] to p[1], p[2] to p[3], 220)
                }
                is Control.Global -> svc.performGlobalAction(
                    when (c.action) {
                        "back" -> AccessibilityService.GLOBAL_ACTION_BACK
                        "home" -> AccessibilityService.GLOBAL_ACTION_HOME
                        "recents" -> AccessibilityService.GLOBAL_ACTION_RECENTS
                        else -> AccessibilityService.GLOBAL_ACTION_NOTIFICATIONS
                    },
                )
                is Control.Text -> type(svc, c.s, null)
                is Control.Key -> key(svc, c.k)
                else -> false
            }
        }.getOrDefault(false)
    }

    private fun stroke(svc: AccessibilityService, from: Pair<Float, Float>, to: Pair<Float, Float>?, ms: Long): Boolean {
        val path = Path().apply {
            moveTo(from.first, from.second)
            // A tap is a stroke that doesn't move; some phones want a tiny line.
            if (to != null) lineTo(to.first, to.second) else lineTo(from.first + 0.5f, from.second + 0.5f)
        }
        val g = GestureDescription.Builder().addStroke(GestureDescription.StrokeDescription(path, 0, ms.coerceIn(1, 3000))).build()
        return svc.dispatchGesture(g, null, null)
    }

    private fun focused(svc: AccessibilityService): AccessibilityNodeInfo? =
        svc.findFocus(AccessibilityNodeInfo.FOCUS_INPUT) ?: svc.rootInActiveWindow?.findFocus(AccessibilityNodeInfo.FOCUS_INPUT)

    private fun key(svc: AccessibilityService, k: String): Boolean {
        val node = focused(svc) ?: return false
        return when (k) {
            "Enter" -> {
                if (Build.VERSION.SDK_INT >= 30 && node.actionList.any { it.id == AccessibilityNodeInfo.AccessibilityAction.ACTION_IME_ENTER.id }) {
                    node.performAction(AccessibilityNodeInfo.AccessibilityAction.ACTION_IME_ENTER.id)
                } else if (node.isMultiLine) {
                    type(svc, "\n", null)
                } else false
            }
            "Tab" -> node.focusSearch(View.FOCUS_FORWARD)?.performAction(AccessibilityNodeInfo.ACTION_FOCUS) ?: false
            else -> type(svc, null, k)
        }
    }

    /** Types into the focused field: inserts [insert] at the cursor, or applies [key]. */
    private fun type(svc: AccessibilityService, insert: String?, key: String?): Boolean {
        val node = focused(svc) ?: return false
        if (!node.isEditable) return false
        val text = if (node.isShowingHintText) "" else node.text?.toString().orEmpty()
        val e = Protocol.edit(text, node.textSelectionStart, node.textSelectionEnd, insert, key) ?: return false
        if (e.text != text) {
            val args = Bundle().apply { putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, e.text) }
            if (!node.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, args)) return false
        }
        val sel = Bundle().apply {
            putInt(AccessibilityNodeInfo.ACTION_ARGUMENT_SELECTION_START_INT, e.cursor)
            putInt(AccessibilityNodeInfo.ACTION_ARGUMENT_SELECTION_END_INT, e.cursor)
        }
        node.performAction(AccessibilityNodeInfo.ACTION_SET_SELECTION, sel)
        return true
    }
}
