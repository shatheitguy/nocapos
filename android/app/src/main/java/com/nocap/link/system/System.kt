package com.nocap.link.system

import android.accessibilityservice.AccessibilityService
import android.annotation.SuppressLint
import android.app.admin.DeviceAdminReceiver
import android.app.admin.DevicePolicyManager
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.os.Build
import android.provider.Settings
import android.service.notification.NotificationListenerService
import android.service.notification.StatusBarNotification
import android.view.accessibility.AccessibilityEvent
import androidx.compose.runtime.mutableStateMapOf
import androidx.core.app.NotificationManagerCompat

/**
 * Notification dots: counts each app's notifications. Opt-in: it only runs
 * after you allow "notification access" for NoCap OS in Android's settings.
 * Nothing is stored or sent anywhere — it only counts.
 */
class DotsService : NotificationListenerService() {
    override fun onListenerConnected() = recount()
    override fun onNotificationPosted(sbn: StatusBarNotification?) = recount()
    override fun onNotificationRemoved(sbn: StatusBarNotification?) = recount()
    override fun onListenerDisconnected() { counts.clear() }

    private fun recount() {
        val now = runCatching { activeNotifications }.getOrNull() ?: return
        val m = now.filter { it.isClearable && !it.isOngoing }.groupingBy { it.packageName }.eachCount()
        counts.keys.retainAll(m.keys)
        counts.putAll(m)
    }

    companion object {
        /** package → number of notifications, read by the home screen. */
        val counts = mutableStateMapOf<String, Int>()

        fun allowed(ctx: Context) = NotificationManagerCompat.getEnabledListenerPackages(ctx).contains(ctx.packageName)

        fun openSettings(ctx: Context) {
            runCatching { ctx.startActivity(Intent(Settings.ACTION_NOTIFICATION_LISTENER_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
        }
    }
}

/**
 * Double-tap to lock, the accessibility way (Android 9+). Opt-in: it does
 * nothing but lock the screen when the home screen asks it to.
 */
class LockService : AccessibilityService() {
    override fun onServiceConnected() { instance = this }
    override fun onAccessibilityEvent(event: AccessibilityEvent?) {}
    override fun onInterrupt() {}
    override fun onDestroy() { if (instance === this) instance = null; super.onDestroy() }

    companion object {
        @SuppressLint("StaticFieldLeak")
        @Volatile var instance: LockService? = null
    }
}

/** Double-tap to lock on older phones: a device admin that may only lock the screen. */
class LockAdmin : DeviceAdminReceiver()

object Phone {
    /** Pulls down the notification shade. */
    @SuppressLint("WrongConstant")
    fun expandNotifications(ctx: Context): Boolean = runCatching {
        val sb = ctx.getSystemService("statusbar")
        Class.forName("android.app.StatusBarManager").getMethod("expandNotificationsPanel").invoke(sb)
    }.isSuccess

    fun canLock(ctx: Context): Boolean =
        (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P && LockService.instance != null) || adminActive(ctx)

    fun lock(ctx: Context): Boolean {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            LockService.instance?.let { return it.performGlobalAction(AccessibilityService.GLOBAL_ACTION_LOCK_SCREEN) }
        }
        if (adminActive(ctx)) {
            return runCatching { ctx.getSystemService(DevicePolicyManager::class.java).lockNow() }.isSuccess
        }
        return false
    }

    private fun admin(ctx: Context) = ComponentName(ctx, LockAdmin::class.java)

    fun adminActive(ctx: Context) = ctx.getSystemService(DevicePolicyManager::class.java)?.isAdminActive(admin(ctx)) == true

    /** Where to switch the lock helper on. */
    fun lockSetupIntent(ctx: Context): Intent =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS)
        } else {
            Intent(DevicePolicyManager.ACTION_ADD_DEVICE_ADMIN)
                .putExtra(DevicePolicyManager.EXTRA_DEVICE_ADMIN, admin(ctx))
                .putExtra(DevicePolicyManager.EXTRA_ADD_EXPLANATION, "Lets NoCap OS lock the screen when you double-tap the home screen. Nothing else.")
        }

    fun turnOffLockHelpers(ctx: Context) {
        if (adminActive(ctx)) runCatching { ctx.getSystemService(DevicePolicyManager::class.java).removeActiveAdmin(admin(ctx)) }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) LockService.instance?.disableSelf()
    }
}
