package com.nocap.link.settings

import android.Manifest
import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.PowerManager
import android.provider.Settings
import androidx.core.content.ContextCompat
import com.nocap.link.data.Opt
import com.nocap.link.data.t
import com.nocap.link.MainActivity
import com.nocap.link.system.Phone

/**
 * The full-access setup guide: one screen per thing NoCap OS can use, in a
 * sensible order. Each has what it's for, the exact steps, a button that
 * opens the right Android page, and a live status.
 */
object Guide {
    class Item(
        val id: String,
        val title: () -> String,
        val why: () -> String,
        val steps: () -> List<String>,
        /** true = done, false = not yet, null = nothing to switch on (info, or not on this phone). */
        val status: (Context) -> Boolean?,
        val doIt: (MainActivity) -> Unit,
        val doLabel: () -> String = { t("Do it", "Open settings") },
        /** Shows the restricted-settings help. */
        val restricted: Boolean = false,
        /** Only an explanation, no switch. */
        val info: Boolean = false,
    )

    private fun perm(id: String) = Permissions.all.first { it.id == id }

    private fun granted(ctx: Context, p: String) = ContextCompat.checkSelfPermission(ctx, p) == PackageManager.PERMISSION_GRANTED

    /** Runtime permissions the app asks for in its manifest (another feature may add phone/SMS ones). */
    private fun requested(ctx: Context): Set<String> = runCatching {
        @Suppress("DEPRECATION")
        ctx.packageManager.getPackageInfo(ctx.packageName, PackageManager.GET_PERMISSIONS).requestedPermissions?.toSet()
    }.getOrNull().orEmpty()

    private val phonePerms = listOf(
        Manifest.permission.READ_PHONE_STATE, Manifest.permission.ANSWER_PHONE_CALLS, Manifest.permission.READ_CALL_LOG, Manifest.permission.READ_CONTACTS,
        Manifest.permission.READ_SMS, Manifest.permission.RECEIVE_SMS, Manifest.permission.SEND_SMS,
    )

    fun batteryFree(ctx: Context) = ctx.getSystemService(PowerManager::class.java)?.isIgnoringBatteryOptimizations(ctx.packageName) == true

    @SuppressLint("BatteryLife") // a sideloaded companion app that keeps a live link to your own server
    fun askBattery(a: MainActivity) {
        val direct = Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, Uri.parse("package:${a.packageName}"))
        if (!Permissions.open(a, direct)) Permissions.open(a, Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
    }

    fun items(ctx: Context): List<Item> {
        val req = requested(ctx)
        val phone = phonePerms.filter { it in req }
        val list = mutableListOf(
            Item(
                "home", perm("home").title, perm("home").why,
                { listOf(t("Tap the button, pick NoCap OS, then \"Always\" or \"Set as default\".", "Tap the button, choose NoCap OS, and confirm it as the default home app.")) },
                perm("home").granted, perm("home").grant,
            ),
            Item(
                "notifications", perm("notifications").title, perm("notifications").why,
                { listOf(t("Tap the button and hit Allow.", "Tap the button and choose Allow.")) },
                perm("notifications").granted, perm("notifications").grant,
            ),
        )
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) list += Item(
            "restricted",
            { t("Allow restricted settings", "Allow restricted settings") },
            {
                t(
                    "Android locks accessibility, notification access and device admin for apps installed from a file. This unlocks them for NoCap OS only.",
                    "Android restricts accessibility, notification access and device administrator for apps installed from a file. This lifts that restriction for NoCap OS only.",
                )
            },
            {
                listOf(
                    t("1. Tap \"Try it first\". Android says \"App was denied access\". That's expected, tap OK.", "1. Tap \"Try it first\". Android shows \"App was denied access\"; this is expected. Tap OK."),
                    t("2. Come back and tap \"Open App info\".", "2. Return here and tap \"Open App info\"."),
                    t("3. Tap ⋮ (top-right) → Allow restricted settings, then confirm with your PIN.", "3. Tap ⋮ (top-right menu) → Allow restricted settings and confirm with your PIN."),
                    t("4. Come back here. This turns green when Android shows it's allowed.", "4. Return here. The status turns green once Android reports it as allowed."),
                ) + if (Build.VERSION.SDK_INT >= 35) listOf(t("Android 15: the popup may say \"Restricted setting\". Same steps.", "Android 15: the message may read \"Restricted setting\". The steps are the same.")) else emptyList()
            },
            { c ->
                when (Restricted.blocked(c)) {
                    false -> true
                    true -> false
                    // Can't read it on this phone: done once one of the restricted ones is on.
                    null -> DotsServiceCheck.on(c) || Phone.canLock(c)
                }
            },
            { a ->
                if (!Restricted.tried(a)) { Restricted.markTried(a); Permissions.open(a, Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS)) }
                else Permissions.appSettings(a)
            },
            doLabel = { t("Try it first / Open App info", "Try first, then open App info") },
        )
        list += Item(
            "lock", perm("lock").title, perm("lock").why,
            {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) listOf(
                    t("Tap the button. Find \"NoCap OS lock\" (maybe under Installed apps / Downloaded apps).", "Tap the button and find \"NoCap OS lock\" (it may be under Installed or Downloaded apps)."),
                    t("Switch it on and confirm.", "Turn it on and confirm."),
                ) else listOf(t("Tap the button and Activate.", "Tap the button and choose Activate."))
            },
            perm("lock").granted, { a -> Restricted.markTried(a); perm("lock").grant(a) }, restricted = true,
        )
        list += Item(
            "listener", perm("listener").title, perm("listener").why,
            { listOf(t("Tap the button, find NoCap OS, switch it on and Allow.", "Tap the button, select NoCap OS, turn it on and confirm.")) },
            perm("listener").granted, { a -> Restricted.markTried(a); perm("listener").grant(a) }, restricted = true,
        )
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) list += Item(
            "admin", perm("admin").title, perm("admin").why,
            { listOf(t("Tap the button, read what it can do (only lock), then Activate.", "Tap the button, review the single policy (lock the screen) and choose Activate.")) },
            perm("admin").granted, { a -> Restricted.markTried(a); perm("admin").grant(a) }, restricted = true,
        )
        list += Item(
            "battery",
            { t("No battery limits", "Battery optimization exemption") },
            {
                t(
                    "Keeps NoCap Link, sign-in alerts and clipboard sync alive in the background. NoCap OS sleeps when the screen is off anyway.",
                    "Lets NoCap Link, sign-in alerts and clipboard sync run in the background. NoCap OS already pauses when the screen is off.",
                )
            },
            { listOf(t("Tap the button and Allow. On some phones: All apps → NoCap OS → Don't optimize / Unrestricted.", "Tap the button and choose Allow. On some phones: All apps → NoCap OS → Don't optimize (or Unrestricted).")) },
            { batteryFree(it) }, { askBattery(it) },
        )
        list += Item(
            "installs", perm("installs").title, perm("installs").why,
            { listOf(t("Tap the button and switch on \"Allow from this source\".", "Tap the button and turn on \"Allow from this source\".")) },
            perm("installs").granted, perm("installs").grant,
        )
        list += Item(
            "camera", perm("camera").title, perm("camera").why,
            { listOf(t("Tap the button and Allow (\"While using the app\").", "Tap the button and choose \"While using the app\".")) },
            perm("camera").granted, perm("camera").grant,
        )
        if (phone.isNotEmpty()) list += Item(
            "phone",
            { t("Calls & messages", "Phone, call log and SMS") },
            { t("For the NoCap features that show calls and texts on your server. Only to your own server.", "Used by NoCap features that show calls and messages on your own server. Sent nowhere else.") },
            { listOf(t("Tap the button and Allow each one Android asks about.", "Tap the button and allow each request.")) },
            { c -> phone.all { granted(c, it) } },
            { a -> phone.firstOrNull { !granted(a, it) }?.let { Permissions.ask(a, it) } },
        )
        list += Item(
            "location",
            { t("Weather (approximate location)", "Weather and approximate location") },
            {
                t(
                    "Opt-in. Weather comes from Open-Meteo, the only outside service NoCap OS uses for it: it gets a rough spot (about 1 km), never your exact location. Or skip and pick a city.",
                    "Optional. Forecasts come from Open-Meteo, a third-party service that receives an approximate location (about 1 km), never a precise one. You can pick a city instead.",
                )
            },
            { listOf(t("Tap the button to turn weather on and allow approximate location.", "Tap the button to enable weather and allow approximate location.")) },
            { c -> Opt.weatherOn.v && granted(c, Manifest.permission.ACCESS_COARSE_LOCATION) },
            { a -> Opt.weatherOn.set(true); Permissions.ask(a, Manifest.permission.ACCESS_COARSE_LOCATION) },
            doLabel = { t("Turn on weather", "Enable weather") },
        )
        list += Item(
            "nearby", perm("nearby").title, perm("nearby").why,
            {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                    listOf(t("Tap the button and Allow \"Nearby devices\".", "Tap the button and allow \"Nearby devices\"."))
                } else {
                    listOf(
                        t("Tap the button and Allow location (it's only how Android unlocks Bluetooth scans here).", "Tap the button and allow location; on this Android version it's required for Bluetooth scanning."),
                        t("Scanning also needs location switched on in quick settings while you look.", "Location services must also be on while scanning."),
                    )
                }
            },
            perm("nearby").granted, perm("nearby").grant,
            doLabel = { t("Allow", "Allow") },
        )
        list += Item(
            "calendar", perm("calendar").title, perm("calendar").why,
            { listOf(t("Tap the button and Allow.", "Tap the button and choose Allow.")) },
            perm("calendar").granted, perm("calendar").grant,
        )
        list += Item(
            "capture",
            { t("Screen mirroring", "Screen capture (mirroring)") },
            {
                t(
                    "Nothing to switch on now. Whenever something wants to show your phone screen on your server, Android asks every single time, and a cast icon shows while it runs.",
                    "No setting is needed now. Android asks for permission each time screen mirroring starts and shows a cast icon while it runs.",
                )
            },
            { listOf(t("When Android asks \"Start recording or casting?\", pick Start only if you started it.", "When Android asks to start recording or casting, choose Start only if you initiated it.")) },
            { null }, {}, info = true,
        )
        return list
    }

    /** "6 of 11 done": items that can be switched on, and how many are. */
    fun progress(ctx: Context, items: List<Item>): Pair<Int, Int> =
        tally(items.filter { !it.info }.map { runCatching { it.status(ctx) }.getOrNull() })

    /** Done / total over the statuses; null ones (not on this phone) don't count either way. */
    fun tally(statuses: List<Boolean?>): Pair<Int, Int> {
        val real = statuses.filterNotNull()
        return real.count { it } to real.size
    }

    // Resume where you left off.
    private fun sp(ctx: Context) = ctx.getSharedPreferences("nocap_launcher", Context.MODE_PRIVATE)
    fun savedIndex(ctx: Context) = sp(ctx).getInt("guide_index", 0)
    fun saveIndex(ctx: Context, i: Int) = sp(ctx).edit().putInt("guide_index", i).apply()
    fun savedStep(ctx: Context): SetupStep? = sp(ctx).getString("setup_step", null)?.let { s -> SetupStep.entries.firstOrNull { it.name == s } }
    fun saveStep(ctx: Context, s: SetupStep) = sp(ctx).edit().putString("setup_step", s.name).apply()
    fun termsOk(ctx: Context) = sp(ctx).getBoolean("terms_ok", false)
    fun agree(ctx: Context) = sp(ctx).edit().putBoolean("terms_ok", true).apply()
}

/** Notification access, without importing the service everywhere. */
private object DotsServiceCheck {
    fun on(ctx: Context) = com.nocap.link.system.DotsService.allowed(ctx)
}
