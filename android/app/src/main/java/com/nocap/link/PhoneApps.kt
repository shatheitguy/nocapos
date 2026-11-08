package com.nocap.link

import android.content.ActivityNotFoundException
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.content.pm.LauncherApps
import android.content.pm.ShortcutInfo
import android.net.Uri
import android.os.Build
import android.os.Process
import android.provider.MediaStore
import android.provider.Settings
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.core.graphics.drawable.toBitmap

/** An app on this phone with a launcher icon. */
data class PhoneApp(
    val label: String, val pkg: String, val activity: String, val icon: ImageBitmap,
    val category: Int, val system: Boolean,
) {
    val key get() = "$pkg/$activity"
}

/** A long-press shortcut an app offers ("New message", "Scan"…). */
data class AppShortcut(val id: String, val label: String, val icon: ImageBitmap?, val info: ShortcutInfo)

object PhoneApps {
    /** Every launchable app except this one, A–Z. */
    fun load(ctx: Context): List<PhoneApp> {
        val pm = ctx.packageManager
        val px = (64 * ctx.resources.displayMetrics.density).toInt()
        return pm.queryIntentActivities(Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER), 0)
            .filter { it.activityInfo.packageName != ctx.packageName }
            .map {
                val ai = it.activityInfo
                val app = ai.applicationInfo
                PhoneApp(
                    it.loadLabel(pm).toString(), ai.packageName, ai.name, it.loadIcon(pm).toBitmap(px, px).asImageBitmap(),
                    app.category, app.flags and ApplicationInfo.FLAG_SYSTEM != 0,
                )
            }
            .distinctBy { it.key }
            .sortedBy { it.label.lowercase() }
    }

    fun launch(ctx: Context, app: PhoneApp) = launch(ctx, app.pkg, app.activity)

    fun launch(ctx: Context, pkg: String, activity: String) {
        val i = Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER)
            .setComponent(ComponentName(pkg, activity))
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_RESET_TASK_IF_NEEDED)
        try { ctx.startActivity(i) } catch (_: ActivityNotFoundException) { } catch (_: SecurityException) { }
    }

    fun info(ctx: Context, app: PhoneApp) {
        ctx.startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:${app.pkg}")).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }

    fun uninstall(ctx: Context, app: PhoneApp) {
        try {
            @Suppress("DEPRECATION")
            ctx.startActivity(Intent(Intent.ACTION_UNINSTALL_PACKAGE, Uri.parse("package:${app.pkg}")).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        } catch (_: ActivityNotFoundException) {
            info(ctx, app)
        }
    }

    /** Shortcuts only work while NoCap OS is the default home app. */
    fun shortcuts(ctx: Context, app: PhoneApp): List<AppShortcut> {
        val la = ctx.getSystemService(LauncherApps::class.java) ?: return emptyList()
        if (!runCatching { la.hasShortcutHostPermission() }.getOrDefault(false)) return emptyList()
        val q = LauncherApps.ShortcutQuery()
            .setPackage(app.pkg)
            .setQueryFlags(LauncherApps.ShortcutQuery.FLAG_MATCH_DYNAMIC or LauncherApps.ShortcutQuery.FLAG_MATCH_MANIFEST or LauncherApps.ShortcutQuery.FLAG_MATCH_PINNED)
        val list = runCatching { la.getShortcuts(q, Process.myUserHandle()) }.getOrNull().orEmpty()
        val px = (32 * ctx.resources.displayMetrics.density).toInt()
        return list.filter { it.isEnabled }.sortedBy { it.rank }.take(5).map { s ->
            val icon = runCatching { la.getShortcutIconDrawable(s, ctx.resources.displayMetrics.densityDpi)?.toBitmap(px, px)?.asImageBitmap() }.getOrNull()
            AppShortcut(s.id, (s.shortLabel ?: s.longLabel ?: s.id).toString(), icon, s)
        }
    }

    fun startShortcut(ctx: Context, s: AppShortcut) {
        val la = ctx.getSystemService(LauncherApps::class.java) ?: return
        runCatching { la.startShortcut(s.info, null, null) }
    }

    /** "Smart folders" for the categories drawer. */
    fun categoryName(c: Int): String = when (c) {
        ApplicationInfo.CATEGORY_GAME -> t("Games", "Games")
        ApplicationInfo.CATEGORY_AUDIO -> t("Ear Candy", "Music & audio")
        ApplicationInfo.CATEGORY_VIDEO -> t("Watch Party", "Video")
        ApplicationInfo.CATEGORY_IMAGE -> t("Pics", "Photos")
        ApplicationInfo.CATEGORY_SOCIAL -> t("The Group Chat", "Social")
        ApplicationInfo.CATEGORY_NEWS -> t("The Tea", "News")
        ApplicationInfo.CATEGORY_MAPS -> t("Out & About", "Maps & travel")
        ApplicationInfo.CATEGORY_PRODUCTIVITY -> t("Grind Mode", "Productivity")
        ApplicationInfo.CATEGORY_ACCESSIBILITY -> t("Accessibility", "Accessibility")
        else -> t("Everything Else", "Other")
    }

    fun groupedByCategory(apps: List<PhoneApp>): List<Pair<String, List<PhoneApp>>> {
        val sys = apps.filter { it.system && it.category == ApplicationInfo.CATEGORY_UNDEFINED }
        val rest = apps - sys.toSet()
        val groups = rest.groupBy { categoryName(it.category) }.toMutableMap()
        if (sys.isNotEmpty()) groups[t("Phone Basics", "System")] = sys
        return groups.entries.sortedBy { it.key }.map { it.key to it.value }
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

    /** Opens the phone's "default home app" picker. */
    fun askToBeHome(activity: android.app.Activity) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            val rm = activity.getSystemService(android.app.role.RoleManager::class.java)
            if (rm != null && rm.isRoleAvailable(android.app.role.RoleManager.ROLE_HOME) && !rm.isRoleHeld(android.app.role.RoleManager.ROLE_HOME)) {
                runCatching { activity.startActivityForResult(rm.createRequestRoleIntent(android.app.role.RoleManager.ROLE_HOME), 77) }.onSuccess { return }
            }
        }
        runCatching { activity.startActivity(Intent(Settings.ACTION_HOME_SETTINGS)) }
            .onFailure { activity.startActivity(Intent(Settings.ACTION_SETTINGS)) }
    }

    fun isDefaultHome(ctx: Context): Boolean {
        val r = ctx.packageManager.resolveActivity(Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_HOME), 0)
        return r?.activityInfo?.packageName == ctx.packageName
    }
}
