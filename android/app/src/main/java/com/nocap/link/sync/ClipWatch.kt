package com.nocap.link.sync

import android.accessibilityservice.AccessibilityService
import android.accessibilityservice.AccessibilityServiceInfo
import android.app.KeyguardManager
import android.app.Notification
import android.content.Context
import android.content.Intent
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.provider.Settings
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityWindowInfo
import com.nocap.link.data.Opt
import com.nocap.link.data.Vault
import com.nocap.link.system.LockService

/**
 * Phone → NoCap OS without a "send" tap. Android lets only the app in front
 * read the clipboard, so when the accessibility helper sees a copy in
 * another app (a "Copy" tap or a "Copied" toast), NoCap OS steps in front
 * for a moment with a see-through, untouchable window, reads the clipboard
 * and is gone. It waits while the keyboard is up (never pulling you out of
 * typing), runs at most every 1.5 s, and skips apps you excluded.
 */
object ClipWatch {
    /** The app last in front (not NoCap OS, the system bars or the keyboard): where a copy came from. */
    @Volatile var app: String? = null
        private set

    private val main = Handler(Looper.getMainLooper())
    private var lastRun = 0L
    private var waitingSince = 0L
    private var source: String? = null
    private var ownCopy: String? = null
    private val retry = Runnable { LockService.instance?.let { run(it) } }

    private const val EXTRA_EVENTS = AccessibilityEvent.TYPE_VIEW_CLICKED or
        AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED or AccessibilityEvent.TYPE_WINDOWS_CHANGED

    /** Copies in other apps are sent on their own: shared clipboard on, automatic on, linked. */
    fun wanted(ctx: Context): Boolean =
        runCatching { Opt.init(ctx) }.isSuccess && Opt.clipSync.v && Opt.clipAuto.v && runCatching { Vault.get(ctx).link != null }.getOrDefault(false)

    /** Applies the current choices to the running helper (call after changing them). */
    fun refresh() {
        LockService.instance?.let { configure(it) }
    }

    /** Listens for copies only while that's wanted; otherwise the helper stays as quiet as before. */
    fun configure(svc: AccessibilityService) {
        runCatching {
            val info = svc.serviceInfo ?: return
            if (wanted(svc)) {
                info.eventTypes = info.eventTypes or EXTRA_EVENTS
                info.flags = info.flags or AccessibilityServiceInfo.FLAG_RETRIEVE_INTERACTIVE_WINDOWS
            } else {
                info.eventTypes = (info.eventTypes and EXTRA_EVENTS.inv()) or AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED
                main.removeCallbacks(retry)
                waitingSince = 0L
            }
            svc.serviceInfo = info
        }.onFailure { com.nocap.link.system.CrashLog.warn("clipboard watch", it) }
    }

    fun onEvent(svc: AccessibilityService, e: AccessibilityEvent) {
        val pkg = e.packageName?.toString() ?: return
        when (e.eventType) {
            AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED ->
                if (pkg != svc.packageName && pkg != "com.android.systemui" && pkg != "android" && !isKeyboard(svc, pkg)) app = pkg
            AccessibilityEvent.TYPE_VIEW_CLICKED -> {
                val own = ownCopy ?: runCatching { svc.getString(android.R.string.copy) }.getOrNull().also { ownCopy = it }
                if (e.text.any { ClipRules.isCopyLabel(it, own) } || ClipRules.isCopyLabel(e.contentDescription, own)) copied(svc, pkg)
            }
            AccessibilityEvent.TYPE_NOTIFICATION_STATE_CHANGED ->
                if (e.parcelableData !is Notification && e.text.any { ClipRules.isCopiedToast(it) }) copied(svc, pkg)
            AccessibilityEvent.TYPE_WINDOWS_CHANGED -> if (waitingSince != 0L) run(svc)
        }
    }

    private fun isKeyboard(ctx: Context, pkg: String): Boolean {
        val ime = runCatching { Settings.Secure.getString(ctx.contentResolver, Settings.Secure.DEFAULT_INPUT_METHOD) }.getOrNull()
        return ime != null && ime.substringBefore('/') == pkg
    }

    private fun copied(svc: AccessibilityService, pkg: String) {
        if (pkg == svc.packageName || !wanted(svc)) return // NoCap OS sends its own when it's in front
        val from = if (pkg == "com.android.systemui" || pkg == "android") app ?: pkg else pkg
        if (from in Opt.clipSkipApps.v) return
        source = from
        waitingSince = SystemClock.elapsedRealtime()
        main.removeCallbacks(retry)
        main.postDelayed(retry, 400) // let the app finish copying
    }

    private fun keyboardUp(svc: AccessibilityService): Boolean =
        runCatching { svc.windows.any { it.type == AccessibilityWindowInfo.TYPE_INPUT_METHOD } }.getOrDefault(false)

    private fun run(svc: AccessibilityService) {
        if (waitingSince == 0L) return
        val now = SystemClock.elapsedRealtime()
        if (now - waitingSince > ClipRules.WAIT_MS || !wanted(svc)) { waitingSince = 0L; return }
        if (svc.getSystemService(KeyguardManager::class.java)?.isKeyguardLocked == true) { waitingSince = 0L; return }
        // Typing: wait until the keyboard closes (a windows-changed event brings us back).
        if (keyboardUp(svc)) return
        if (!ClipRules.due(now, lastRun)) {
            main.removeCallbacks(retry)
            main.postDelayed(retry, ClipRules.GAP_MS - (now - lastRun))
            return
        }
        waitingSince = 0L
        lastRun = now
        val i = Intent(svc, ClipActivity::class.java)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_NO_ANIMATION or Intent.FLAG_ACTIVITY_EXCLUDE_FROM_RECENTS)
            .putExtra(ClipActivity.AUTO, true)
            .putExtra(ClipActivity.SOURCE, source)
        runCatching { svc.startActivity(i) }.onFailure { com.nocap.link.system.CrashLog.warn("clipboard read", it) }
    }
}
