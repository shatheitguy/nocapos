package com.nocap.link.control

import android.app.NotificationManager
import android.bluetooth.BluetoothManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.net.wifi.WifiManager
import android.os.Build
import android.provider.Settings
import com.nocap.link.widget.Torch

/**
 * The control center's quick toggles. Android doesn't let ordinary apps flip
 * Wi-Fi or Bluetooth themselves (Android 10+), so those open Android's own
 * panel; flashlight and Do not disturb flip right here; rotation lock and
 * brightness need "Modify system settings", which you allow once.
 */
object Switches {
    fun wifi(ctx: Context): Boolean? = runCatching { ctx.applicationContext.getSystemService(WifiManager::class.java)?.isWifiEnabled }.getOrNull()

    fun bluetooth(ctx: Context): Boolean? = runCatching { ctx.getSystemService(BluetoothManager::class.java)?.adapter?.isEnabled }.getOrNull()

    fun torch(): Boolean = Torch.on

    fun dnd(ctx: Context): Boolean? = runCatching { ctx.getSystemService(NotificationManager::class.java)?.currentInterruptionFilter }.getOrNull()
        ?.let { it != NotificationManager.INTERRUPTION_FILTER_ALL && it != NotificationManager.INTERRUPTION_FILTER_UNKNOWN }

    /** True when rotation is locked (auto-rotate off). */
    fun rotationLocked(ctx: Context): Boolean? =
        runCatching { Settings.System.getInt(ctx.contentResolver, Settings.System.ACCELEROMETER_ROTATION) == 0 }.getOrNull()

    fun canWrite(ctx: Context): Boolean = Settings.System.canWrite(ctx)

    /** Where to allow "Modify system settings" for NoCap OS. */
    fun writeSettingsIntent(ctx: Context): Intent =
        Intent(Settings.ACTION_MANAGE_WRITE_SETTINGS, Uri.parse("package:" + ctx.packageName)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)

    fun wifiIntent(): Intent = Intent(if (Build.VERSION.SDK_INT >= 29) Settings.Panel.ACTION_WIFI else Settings.ACTION_WIFI_SETTINGS)

    fun bluetoothIntent(): Intent = Intent(Settings.ACTION_BLUETOOTH_SETTINGS)

    /** Flips Do not disturb; false when NoCap OS hasn't been allowed to. */
    fun toggleDnd(ctx: Context): Boolean {
        val nm = ctx.getSystemService(NotificationManager::class.java) ?: return false
        if (!nm.isNotificationPolicyAccessGranted) return false
        return runCatching {
            nm.setInterruptionFilter(if (dnd(ctx) == true) NotificationManager.INTERRUPTION_FILTER_ALL else NotificationManager.INTERRUPTION_FILTER_PRIORITY)
        }.isSuccess
    }

    /** Locks or unlocks rotation; false without "Modify system settings". */
    fun toggleRotation(ctx: Context): Boolean {
        if (!canWrite(ctx)) return false
        val locked = rotationLocked(ctx) ?: return false
        return runCatching { Settings.System.putInt(ctx.contentResolver, Settings.System.ACCELEROMETER_ROTATION, if (locked) 1 else 0) }.getOrDefault(false)
    }

    /** Screen brightness as 0..1 (Android stores 0..255 for the manual level). */
    fun brightness(ctx: Context): Float =
        runCatching { Settings.System.getInt(ctx.contentResolver, Settings.System.SCREEN_BRIGHTNESS) }.getOrNull()?.let { fromLevel(it) } ?: 0.5f

    /** Sets the manual brightness (turning adaptive brightness off, like the system slider does). */
    fun setBrightness(ctx: Context, f: Float): Boolean {
        if (!canWrite(ctx)) return false
        return runCatching {
            val cr = ctx.contentResolver
            Settings.System.putInt(cr, Settings.System.SCREEN_BRIGHTNESS_MODE, Settings.System.SCREEN_BRIGHTNESS_MODE_MANUAL)
            Settings.System.putInt(cr, Settings.System.SCREEN_BRIGHTNESS, toLevel(f))
        }.getOrDefault(false)
    }

    /** 0..1 → 1..255 (never fully black). */
    fun toLevel(f: Float): Int = (f.coerceIn(0f, 1f) * 254f).toInt() + 1

    fun fromLevel(level: Int): Float = ((level.coerceIn(1, 255) - 1) / 254f)
}
