package com.nocap.link.data

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
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.ImageBitmap
import androidx.core.graphics.drawable.toBitmap

/** An app on this phone with a launcher icon. */
data class PhoneApp(
    val label: String, val pkg: String, val activity: String, val icon: ImageBitmap,
    val category: Int, val system: Boolean,
    /** Adaptive icons: both layers full-bleed (no mask), for our own shapes. */
    val full: ImageBitmap? = null,
    /** Adaptive icons: just the foreground layer, for NoCap tiles. */
    val fg: ImageBitmap? = null,
    /** Android 13+ themed (monochrome) layer, full-bleed. */
    val mono: ImageBitmap? = null,
    val installed: Long = 0,
    val custom: Boolean = false,
) {
    val key get() = "$pkg/$activity"
}

/** How often and how recently each app was opened (for drawer sort and the recents row). */
object Usage {
    private var sp: android.content.SharedPreferences? = null
    var tick by mutableIntStateOf(0)
        private set

    private fun sp(ctx: Context) = sp ?: ctx.applicationContext.getSharedPreferences("nocap_usage", Context.MODE_PRIVATE).also { sp = it }

    fun record(ctx: Context, key: String) {
        val s = sp(ctx)
        s.edit().putInt("n:$key", s.getInt("n:$key", 0) + 1).putLong("t:$key", System.currentTimeMillis()).apply()
        tick++
    }

    fun count(ctx: Context, key: String) = sp(ctx).getInt("n:$key", 0)
    fun last(ctx: Context, key: String) = sp(ctx).getLong("t:$key", 0)

    fun recent(ctx: Context, apps: List<PhoneApp>, n: Int): List<PhoneApp> =
        apps.map { it to last(ctx, it.key) }.filter { it.second > 0 }.sortedByDescending { it.second }.take(n).map { it.first }
}

/** Per-app custom icons picked from the gallery, kept in app storage. */
object CustomIcons {
    private fun dir(ctx: Context) = java.io.File(ctx.filesDir, "icons").apply { mkdirs() }

    fun load(ctx: Context, name: String, px: Int): ImageBitmap? = runCatching {
        val f = java.io.File(dir(ctx), name)
        if (!f.exists()) return null
        val b = android.graphics.BitmapFactory.decodeFile(f.path) ?: return null
        android.graphics.Bitmap.createScaledBitmap(b, px, px, true).asImageBitmap()
    }.getOrNull()

    /** Copies a picked picture in as a square icon; returns the file name. */
    fun save(ctx: Context, key: String, uri: Uri): String? = runCatching {
        // Read the size first and decode at most ~1024 px: phone photos are huge.
        val bounds = android.graphics.BitmapFactory.Options().apply { inJustDecodeBounds = true }
        ctx.contentResolver.openInputStream(uri)?.use { android.graphics.BitmapFactory.decodeStream(it, null, bounds) }
        var sample = 1
        while (minOf(bounds.outWidth, bounds.outHeight) / (sample * 2) >= 512) sample *= 2
        val opts = android.graphics.BitmapFactory.Options().apply { inSampleSize = sample }
        val src = ctx.contentResolver.openInputStream(uri)?.use { android.graphics.BitmapFactory.decodeStream(it, null, opts) } ?: return null
        if (src.width <= 0 || src.height <= 0) return null
        val side = minOf(src.width, src.height)
        val sq = android.graphics.Bitmap.createBitmap(src, (src.width - side) / 2, (src.height - side) / 2, side, side)
        val out = android.graphics.Bitmap.createScaledBitmap(sq, 256, 256, true)
        val name = "i" + Integer.toHexString(key.hashCode()) + "_" + (System.currentTimeMillis() % 100000) + ".png"
        java.io.File(dir(ctx), name).outputStream().use { out.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, it) }
        name
    }.getOrNull()

    fun prune(ctx: Context) {
        val keep = Opt.customIcons.v.values.toSet()
        dir(ctx).listFiles()?.filter { it.name !in keep }?.forEach { it.delete() }
    }
}

/** A long-press shortcut an app offers ("New message", "Scan"…). */
data class AppShortcut(val id: String, val label: String, val icon: ImageBitmap?, val info: ShortcutInfo)

object PhoneApps {
    /** Every launchable app except this one, A–Z. */
    fun load(ctx: Context): List<PhoneApp> {
        val pm = ctx.packageManager
        val px = (64 * ctx.resources.displayMetrics.density).toInt()
        val labels = Opt.customLabels.v
        val icons = Opt.customIcons.v
        return pm.queryIntentActivities(Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER), 0)
            .filter { it.activityInfo.packageName != ctx.packageName }
            .mapNotNull { runCatching { one(ctx, pm, it, px, labels, icons) }.getOrNull() } // one broken app mustn't empty the list
            .distinctBy { it.key }
            .sortedBy { it.label.lowercase() }
    }

    private fun one(
        ctx: Context, pm: android.content.pm.PackageManager, it: android.content.pm.ResolveInfo, px: Int,
        labels: Map<String, String>, icons: Map<String, String>,
    ): PhoneApp {
        val ai = it.activityInfo
        val app = ai.applicationInfo
        val key = "${ai.packageName}/${ai.name}"
        val system = app.flags and ApplicationInfo.FLAG_SYSTEM != 0
        val installed = runCatching { pm.getPackageInfo(ai.packageName, 0).firstInstallTime }.getOrDefault(0L)
        val label = labels[key]?.takeIf { l -> l.isNotBlank() } ?: it.loadLabel(pm).toString()
        val customBmp = icons[key]?.let { f -> CustomIcons.load(ctx, f, px) }
        return if (customBmp != null) {
            PhoneApp(label, ai.packageName, ai.name, customBmp, app.category, system, full = customBmp, installed = installed, custom = true)
        } else {
            val d = it.loadIcon(pm)
            val l = layers(d, px)
            PhoneApp(
                label, ai.packageName, ai.name, d.toBitmap(px, px).asImageBitmap(), app.category, system,
                full = l?.first, fg = l?.second, mono = l?.third, installed = installed,
            )
        }
    }

    /** Draws an adaptive icon's layers full-bleed (the visible 72 of 108 units fill the bitmap). */
    private fun layers(d: android.graphics.drawable.Drawable, px: Int): Triple<ImageBitmap?, ImageBitmap?, ImageBitmap?>? {
        if (d !is android.graphics.drawable.AdaptiveIconDrawable) return null
        return runCatching {
            val inset = px / 4
            fun draw(vararg ds: android.graphics.drawable.Drawable?): ImageBitmap {
                val b = android.graphics.Bitmap.createBitmap(px, px, android.graphics.Bitmap.Config.ARGB_8888)
                val c = android.graphics.Canvas(b)
                for (x in ds) {
                    if (x == null) continue
                    x.setBounds(-inset, -inset, px + inset, px + inset)
                    x.draw(c)
                }
                return b.asImageBitmap()
            }
            // Only the layers the current icon style uses (they're big): reloaded when it changes.
            val theme = Opt.iconTheme.v
            val wantFull = Opt.iconShape.v != IconShape.ORIGINAL && theme != IconTheme.NOCAP && theme != IconTheme.THEMED
            val wantFg = theme == IconTheme.NOCAP
            val wantMono = theme == IconTheme.THEMED || (theme == IconTheme.NOCAP && Opt.nocapGlyph.v)
            val mono = if (wantMono && Build.VERSION.SDK_INT >= 33) d.monochrome?.let { m -> draw(m) } else null
            Triple(if (wantFull) draw(d.background, d.foreground) else null, if (wantFg) draw(d.foreground) else null, mono)
        }.getOrNull()
    }

    fun launch(ctx: Context, app: PhoneApp) = launch(ctx, app.pkg, app.activity)

    fun launch(ctx: Context, pkg: String, activity: String) {
        Usage.record(ctx, "$pkg/$activity")
        val i = Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER)
            .setComponent(ComponentName(pkg, activity))
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_RESET_TASK_IF_NEEDED)
        try { ctx.startActivity(i) } catch (_: ActivityNotFoundException) { } catch (_: SecurityException) { }
    }

    fun info(ctx: Context, app: PhoneApp) {
        runCatching { ctx.startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:${app.pkg}")).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
            .onFailure { runCatching { ctx.startActivity(Intent(Settings.ACTION_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) } }
    }

    fun uninstall(ctx: Context, app: PhoneApp) {
        try {
            @Suppress("DEPRECATION")
            ctx.startActivity(Intent(Intent.ACTION_UNINSTALL_PACKAGE, Uri.parse("package:${app.pkg}")).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        } catch (_: Exception) {
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
            .onFailure { runCatching { activity.startActivity(Intent(Settings.ACTION_SETTINGS)) } }
    }

    fun isDefaultHome(ctx: Context): Boolean {
        val r = ctx.packageManager.resolveActivity(Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_HOME), 0)
        return r?.activityInfo?.packageName == ctx.packageName
    }
}
