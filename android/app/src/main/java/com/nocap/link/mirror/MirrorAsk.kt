package com.nocap.link.mirror

import androidx.activity.compose.setContent
import android.app.Activity
import android.app.AlertDialog
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.media.projection.MediaProjectionConfig
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.os.Bundle
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import com.nocap.link.R
import com.nocap.link.data.Link
import com.nocap.link.data.Opt
import com.nocap.link.data.Prefs
import com.nocap.link.data.Vault
import com.nocap.link.data.t
import com.nocap.link.net.Pins
import com.nocap.link.sync.Notices
import com.nocap.link.system.MirrorInput
import java.util.concurrent.Executors
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject

/**
 * "Mirror my screen" asks from NoCap OS. They arrive on the NoCap Link
 * long-poll; each one needs the person's OK here (and then Android's own
 * screen-capture prompt) before anything is shared.
 */
object MirrorAsk {
    const val NOTIF = com.nocap.link.sync.Requests.MIRROR_ID
    private val io = Executors.newSingleThreadExecutor()
    @Volatile private var lastAsk: String? = null

    fun serverName(link: Link?) = link?.serverName?.takeIf { it.isNotBlank() } ?: t("Your NoCap server", "Your NoCap server")

    /** A new ask came in on the long-poll. */
    fun asked(ctx: Context, id: String) {
        if (id.isBlank() || id == lastAsk) return
        lastAsk = id
        val app = ctx.applicationContext
        Prefs.init(app)
        if (MirrorService.running) return // already streaming: the viewer just joins
        if (!Opt.mirrorAsks.v) { postState(app, "denied", id); return }
        val link = runCatching { Vault.get(app).link }.getOrNull() ?: return
        // Heads-up with Accept / Decline (full screen when locked). When NoCap OS
        // is in front, the ask opens right away as well.
        val shown = com.nocap.link.sync.Requests.mirror(app, serverName(link), id)
        if (inFront() || !shown) {
            val open = Intent(app, MirrorConsentActivity::class.java).putExtra(MirrorConsentActivity.ASK, id)
                .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
            runCatching { app.startActivity(open) }
        }
        if (!shown) com.nocap.link.system.CrashLog.warn("mirror ask", IllegalStateException("request notification not shown (notifications off?)"))
    }

    private fun inFront(): Boolean {
        val info = android.app.ActivityManager.RunningAppProcessInfo()
        android.app.ActivityManager.getMyMemoryState(info)
        return info.importance <= android.app.ActivityManager.RunningAppProcessInfo.IMPORTANCE_FOREGROUND
    }

    /** Tells the server where an ask stands: asking, denied, unsupported or stopped. */
    fun postState(ctx: Context, state: String, ask: String?) {
        val link = runCatching { Vault.get(ctx.applicationContext).link }.getOrNull() ?: return
        io.execute {
            runCatching {
                val body = JSONObject().put("state", state).apply { if (!ask.isNullOrBlank()) put("ask", ask) }.toString()
                val req = Request.Builder().url(com.nocap.link.net.Endpoint.server(link) + "/api/v1/device/mirror/state")
                    .header("Authorization", "Device ${link.token}")
                    .post(body.toRequestBody("application/json".toMediaType()))
                    .build()
                Pins.client(link.pin).newCall(req).execute().close()
            }
        }
    }
}

/**
 * "<server> wants to mirror your screen": the in-app yes/no, then Android's
 * own screen-capture prompt. Asked again for every session.
 */
class MirrorConsentActivity : androidx.activity.ComponentActivity() {
    private var ask: String? = null
    private var decided = false
    /** The NoCap yes/no card is showing. */
    private val asking = androidx.compose.runtime.mutableStateOf(false)

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Prefs.init(this)
        ask = intent.getStringExtra(ASK)
        decided = savedInstanceState?.getBoolean(DECIDED) ?: false
        NotificationManagerCompat.from(this).cancel(MirrorAsk.NOTIF)
        // Over the lock screen, like an incoming call.
        if (Build.VERSION.SDK_INT >= 27) { setShowWhenLocked(true); setTurnScreenOn(true) }
        if (MirrorService.running || runCatching { Vault.get(this).link }.getOrNull() == null) { decided = true; finish(); return }
        setContent { com.nocap.link.ui.NoCapTheme { if (asking.value) AskCard() } }
        if (savedInstanceState != null) return // rotated: the choice (or Android's prompt) is already on its way
        MirrorAsk.postState(this, "asking", ask)
        // "Accept" on the notification: straight to Android's own prompt.
        if (intent.getBooleanExtra(ACCEPTED, false)) accept()
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        outState.putBoolean(DECIDED, decided)
    }

    override fun onResume() {
        super.onResume()
        if (!decided) asking.value = true
    }

    @androidx.compose.runtime.Composable
    private fun AskCard() {
        val link = runCatching { Vault.get(this).link }.getOrNull()
        val server = MirrorAsk.serverName(link)
        val who = link?.username?.takeIf { it.isNotBlank() }
        var msg = t(
            "Your screen shows up on NoCap OS${who?.let { " for $it" } ?: ""}. Android asks next. Stop any time from the notification.",
            "Your screen will be shown on NoCap OS${who?.let { " for $it" } ?: ""}. Android will ask next. You can stop any time from the notification.",
        )
        if (!MirrorInput.available()) {
            msg += "\n\n" + t(
                "View-only for now: to tap and type from NoCap OS, turn on NoCap OS in Accessibility (sideloaded? App info → ⋮ → Allow restricted settings first).",
                "View only: to control this phone from NoCap OS, turn on NoCap OS in Accessibility settings (for sideloaded installs, first choose App info → ⋮ → Allow restricted settings).",
            )
        }
        com.nocap.link.ui.NcDialog(
            t("$server wants to mirror your screen", "$server wants to mirror your screen"),
            msg,
            onDismiss = { decline() },
            confirm = t("Let it", "Accept") to { accept() },
            dismiss = t("Nah", "Decline") to { decline() },
            extra = if (MirrorInput.available()) emptyList() else listOf(t("Accessibility", "Accessibility settings") to { MirrorInput.accessibilitySettings(this) }),
        )
    }

    private fun accept() {
        decided = true
        asking.value = false
        // Locked: unlock first (Android asks for the PIN), then the capture prompt.
        val km = getSystemService(android.app.KeyguardManager::class.java)
        if (Build.VERSION.SDK_INT >= 26 && km?.isKeyguardLocked == true) {
            km.requestDismissKeyguard(this, object : android.app.KeyguardManager.KeyguardDismissCallback() {
                override fun onDismissSucceeded() { capture() }
                override fun onDismissCancelled() { MirrorAsk.postState(this@MirrorConsentActivity, "denied", ask); finish() }
                override fun onDismissError() { capture() }
            })
            return
        }
        capture()
    }

    private fun capture() {
        val mpm = getSystemService(MediaProjectionManager::class.java)
        if (mpm == null) { MirrorAsk.postState(this, "unsupported", ask); finish(); return }
        val i = if (Build.VERSION.SDK_INT >= 34) {
            // The whole screen, so taps land where they look like they do.
            mpm.createScreenCaptureIntent(MediaProjectionConfig.createConfigForDefaultDisplay())
        } else mpm.createScreenCaptureIntent()
        try {
            @Suppress("DEPRECATION")
            startActivityForResult(i, REQ)
        } catch (_: Exception) {
            MirrorAsk.postState(this, "unsupported", ask)
            finish()
        }
    }

    private fun decline() {
        if (decided) return
        decided = true
        asking.value = false
        MirrorAsk.postState(this, "denied", ask)
        finish()
    }

    @Deprecated("Deprecated in Java")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode != REQ) return
        if (resultCode == RESULT_OK && data != null) {
            val svc = Intent(this, MirrorService::class.java)
                .putExtra(MirrorService.CODE, resultCode)
                .putExtra(MirrorService.DATA, data)
                .putExtra(MirrorService.ASK, ask)
            runCatching { ContextCompat.startForegroundService(this, svc) }
                .onFailure { MirrorAsk.postState(this, "unsupported", ask) }
        } else {
            MirrorAsk.postState(this, "denied", ask)
        }
        finish()
    }

    override fun onDestroy() {
        if (!decided && isFinishing) MirrorAsk.postState(this, "denied", ask)
        super.onDestroy()
    }

    companion object {
        const val ASK = "ask"
        /** Accepted on the notification already: skip the in-app question. */
        const val ACCEPTED = "accepted"
        private const val DECIDED = "decided"
        private const val REQ = 42
    }
}
