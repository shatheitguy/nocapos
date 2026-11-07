package com.nocap.link

import android.content.ActivityNotFoundException
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.provider.MediaStore
import android.provider.Settings
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.core.graphics.drawable.toBitmap

/** An app on this phone with a launcher icon. */
data class PhoneApp(val label: String, val pkg: String, val activity: String, val icon: ImageBitmap) {
    val key get() = "$pkg/$activity"
}

object PhoneApps {
    /** Every launchable app except this one, A–Z. */
    fun load(ctx: Context): List<PhoneApp> {
        val pm = ctx.packageManager
        val px = (56 * ctx.resources.displayMetrics.density).toInt()
        return pm.queryIntentActivities(Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER), 0)
            .filter { it.activityInfo.packageName != ctx.packageName }
            .map {
                val ai = it.activityInfo
                PhoneApp(it.loadLabel(pm).toString(), ai.packageName, ai.name, it.loadIcon(pm).toBitmap(px, px).asImageBitmap())
            }
            .distinctBy { it.key }
            .sortedBy { it.label.lowercase() }
    }

    fun launch(ctx: Context, app: PhoneApp) {
        val i = Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER)
            .setComponent(ComponentName(app.pkg, app.activity))
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_RESET_TASK_IF_NEEDED)
        try { ctx.startActivity(i) } catch (_: ActivityNotFoundException) { }
    }

    fun info(ctx: Context, app: PhoneApp) {
        ctx.startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:${app.pkg}")).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }

    /** First-run dock: phone, messages, browser, camera (whatever exists). */
    fun defaultDock(ctx: Context, all: List<PhoneApp>): List<String> {
        val pm = ctx.packageManager
        val wanted = listOf(
            Intent(Intent.ACTION_DIAL),
            Intent.makeMainSelectorActivity(Intent.ACTION_MAIN, Intent.CATEGORY_APP_MESSAGING),
            Intent.makeMainSelectorActivity(Intent.ACTION_MAIN, Intent.CATEGORY_APP_BROWSER),
            Intent(MediaStore.INTENT_ACTION_STILL_IMAGE_CAMERA),
        )
        val pkgs = wanted.mapNotNull { pm.resolveActivity(it, 0)?.activityInfo?.packageName }
        return pkgs.mapNotNull { p -> all.firstOrNull { it.pkg == p }?.key }.distinct().take(4)
    }
}
