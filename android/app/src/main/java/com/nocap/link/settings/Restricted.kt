package com.nocap.link.settings

import android.app.AppOpsManager
import android.content.Context
import android.os.Build
import android.os.Process
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.nocap.link.data.t
import com.nocap.link.MainActivity
import com.nocap.link.ui.GhostButton
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.RedButton

/**
 * Android 13+ "restricted settings": an app installed from a file (browser,
 * file manager) can't be given accessibility, notification access or device
 * admin until you allow it in App info → ⋮ → Allow restricted settings. That
 * menu item only shows up after you've tried to switch one of them on once.
 */
object Restricted {
    /** The app-op Android uses for this (not in the public SDK constants). */
    private const val OP = "android:access_restricted_settings"
    private const val SP = "nocap_launcher"
    private const val TRIED = "restricted_tried"

    /** Android has this protection at all. */
    val applies get() = Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU

    /**
     * true = Android is blocking restricted settings for NoCap OS right now,
     * false = allowed, null = can't tell on this phone (then we go by whether
     * the switch stayed off after you tried).
     */
    fun blocked(ctx: Context): Boolean? {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) return false
        return runCatching {
            val ops = ctx.getSystemService(AppOpsManager::class.java) ?: return null
            when (ops.unsafeCheckOpNoThrow(OP, Process.myUid(), ctx.packageName)) {
                AppOpsManager.MODE_ALLOWED -> false
                AppOpsManager.MODE_ERRORED, AppOpsManager.MODE_IGNORED -> true
                else -> null
            }
        }.getOrNull()
    }

    /** Remembers that you went to switch a restricted one on (so the ⋮ item now exists). */
    fun markTried(ctx: Context) {
        if (applies) ctx.getSharedPreferences(SP, Context.MODE_PRIVATE).edit().putBoolean(TRIED, true).apply()
    }

    fun tried(ctx: Context) = ctx.getSharedPreferences(SP, Context.MODE_PRIVATE).getBoolean(TRIED, false)

    enum class Help {
        /** Nothing to say: allowed, already on, or an older Android. */
        NONE,
        /** Blocked, but not tried yet: try first (the ⋮ menu appears after that). */
        TRY_FIRST,
        /** Tried and still blocked (or still off): the App info steps. */
        UNBLOCK,
    }

    fun help(ctx: Context, granted: Boolean?): Help {
        if (!applies || granted != false) return Help.NONE
        val b = blocked(ctx)
        val tried = tried(ctx)
        return when {
            b == false -> Help.NONE
            b == true && !tried -> Help.TRY_FIRST
            tried -> Help.UNBLOCK // blocked, or unknown but still off after trying
            else -> Help.NONE
        }
    }

    fun steps(): String {
        val base = t(
            "Android blocks this for apps installed from a file. Open App info → ⋮ (top-right menu) → Allow restricted settings, confirm with your PIN, then come back and turn it on.",
            "Android blocks this for apps installed from a file. Open App info → ⋮ (top-right menu) → Allow restricted settings, confirm with your PIN, then come back and turn it on.",
        )
        val v15 = if (Build.VERSION.SDK_INT >= 35) " " + t(
            "On Android 15 the popup may say \"Restricted setting\" or \"For your security, this setting is currently unavailable\". Same fix. Some phones put the option under App info → ⋮ or the \"Restricted setting\" popup's own Settings link.",
            "On Android 15 the message may read \"Restricted setting\" or \"For your security, this setting is currently unavailable\". The fix is the same; on some phones the option is reached from the popup's Settings link instead.",
        ) else ""
        return base + v15
    }
}

/** The guided step shown on a permission card while Android blocks it. */
@Composable
fun RestrictedHelp(a: MainActivity, help: Restricted.Help, tryIt: () -> Unit) {
    when (help) {
        Restricted.Help.NONE -> Unit
        Restricted.Help.TRY_FIRST -> Text(
            t(
                "Heads up: Android will probably say \"App was denied access\" the first time. That's expected. Try once, come back, and we'll show you how to unblock it.",
                "Android will likely show \"App was denied access\" the first time. That is expected: try once, return here, and the steps to unblock it will appear.",
            ),
            Modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Warn,
        )
        Restricted.Help.UNBLOCK -> Column(Modifier.padding(top = 8.dp)) {
            Text(t("Blocked by Android", "Restricted by Android"), style = MaterialTheme.typography.titleSmall, color = NoCap.Warn)
            Text(Restricted.steps(), Modifier.padding(top = 2.dp), style = MaterialTheme.typography.bodySmall)
            Spacer(Modifier.height(8.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                RedButton(t("Open App info", "Open App info"), { Permissions.appSettings(a) }, Modifier.weight(1f))
                GhostButton(t("Try again", "Try again"), tryIt, Modifier.weight(1f))
            }
            Text(
                t(
                    "Updating NoCap OS from Settings → About → Updates usually lifts this block for good. Not guaranteed on every phone or on Android 15.",
                    "After NoCap OS updates itself (Settings → About → Updates), Android usually stops restricting it. This isn't guaranteed on all phones or on Android 15.",
                ),
                Modifier.padding(top = 6.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted,
            )
        }
    }
}
