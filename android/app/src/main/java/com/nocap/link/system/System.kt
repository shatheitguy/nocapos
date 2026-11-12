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
    override fun onListenerConnected() {
        instance = this
        recount()
        com.nocap.link.widget.Media.start(this)
    }

    override fun onNotificationPosted(sbn: StatusBarNotification?) {
        recount()
        if (sbn != null) runCatching { com.nocap.link.sync.Sync.posted(this, sbn) }
    }

    override fun onNotificationRemoved(sbn: StatusBarNotification?) {
        recount()
        if (sbn != null) runCatching { com.nocap.link.sync.Sync.removed(this, sbn) }
    }

    override fun onListenerDisconnected() {
        counts.clear()
        if (instance === this) instance = null
    }

    private fun recount() {
        val now = runCatching { activeNotifications }.getOrNull() ?: return
        val m = now.filter { it.isClearable && !it.isOngoing }.groupingBy { it.packageName }.eachCount()
        counts.keys.retainAll(m.keys)
        counts.putAll(m)
    }

    companion object {
        /** package → number of notifications, read by the home screen. */
        val counts = mutableStateMapOf<String, Int>()

        @SuppressLint("StaticFieldLeak")
        @Volatile var instance: DotsService? = null

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

    /** Pulls down quick settings. */
    @SuppressLint("WrongConstant")
    fun expandQuickSettings(ctx: Context): Boolean = runCatching {
        val sb = ctx.getSystemService("statusbar")
        Class.forName("android.app.StatusBarManager").getMethod("expandSettingsPanel").invoke(sb)
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

    /** The device admin receiver, as written in the manifest (for the device-owner command). */
    const val ADMIN_RECEIVER = ".system.LockAdmin"

    /** Android's "activate device admin" page for the lock-only admin. */
    fun adminIntent(ctx: Context): Intent =
        Intent(DevicePolicyManager.ACTION_ADD_DEVICE_ADMIN)
            .putExtra(DevicePolicyManager.EXTRA_DEVICE_ADMIN, admin(ctx))
            .putExtra(DevicePolicyManager.EXTRA_ADD_EXPLANATION, "Lets NoCap OS lock the screen when you use your lock gesture. Nothing else.")

    fun isDeviceOwner(ctx: Context) = runCatching { ctx.getSystemService(DevicePolicyManager::class.java)?.isDeviceOwnerApp(ctx.packageName) == true }.getOrDefault(false)

    /** Gives up device owner (only if it was set with the USB command). */
    @Suppress("DEPRECATION")
    fun releaseDeviceOwner(ctx: Context) {
        if (isDeviceOwner(ctx)) runCatching { ctx.getSystemService(DevicePolicyManager::class.java).clearDeviceOwnerApp(ctx.packageName) }
    }

    fun adminActive(ctx: Context) = ctx.getSystemService(DevicePolicyManager::class.java)?.isAdminActive(admin(ctx)) == true

    /** Where to switch the lock helper on. */
    fun lockSetupIntent(ctx: Context): Intent =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS)
        } else {
            adminIntent(ctx)
        }

    fun turnOffLockHelpers(ctx: Context) {
        if (adminActive(ctx)) runCatching { ctx.getSystemService(DevicePolicyManager::class.java).removeActiveAdmin(admin(ctx)) }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) LockService.instance?.disableSelf()
    }
}
