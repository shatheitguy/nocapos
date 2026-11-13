package com.nocap.link.mirror

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
    private const val CHANNEL = "nocap_mirror_ask"
    const val NOTIF = 4200
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
        val open = Intent(app, MirrorConsentActivity::class.java).putExtra(MirrorConsentActivity.ASK, id)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_CLEAR_TOP)
        notify(app, link, id, open)
        // Works when NoCap OS is in front (Android blocks it from the background; the notification covers that).
        runCatching { app.startActivity(open) }
    }

    private fun notify(ctx: Context, link: Link, id: String, open: Intent) {
        if (!Notices.allowed(ctx)) return
        ctx.getSystemService(NotificationManager::class.java)?.createNotificationChannel(
            NotificationChannel(CHANNEL, t("Screen mirroring asks", "Screen mirroring requests"), NotificationManager.IMPORTANCE_HIGH).apply {
                description = t("When NoCap OS asks to show this phone's screen", "When NoCap OS asks to mirror this phone's screen")
            },
        )
        val pi = PendingIntent.getActivity(ctx, NOTIF, open, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val n = NotificationCompat.Builder(ctx, CHANNEL)
            .setSmallIcon(R.drawable.ic_stat_nocap)
            .setContentTitle(t("${serverName(link)} wants to mirror your screen", "${serverName(link)} wants to mirror your screen"))
            .setContentText(t("Tap to say yes or nah.", "Tap to accept or decline."))
            .setCategory(NotificationCompat.CATEGORY_CALL)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .setContentIntent(pi)
            .setAutoCancel(true)
            .setTimeoutAfter(120_000)
            .build()
        try {
            NotificationManagerCompat.from(ctx).notify(NOTIF, n)
        } catch (_: SecurityException) {
            // Permission was pulled in the meantime.
        }
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
class MirrorConsentActivity : Activity() {
    private var ask: String? = null
    private var decided = false
    private var dialog: AlertDialog? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        Prefs.init(this)
        ask = intent.getStringExtra(ASK)
        NotificationManagerCompat.from(this).cancel(MirrorAsk.NOTIF)
        if (MirrorService.running || runCatching { Vault.get(this).link }.getOrNull() == null) { decided = true; finish(); return }
        MirrorAsk.postState(this, "asking", ask)
    }

    override fun onResume() {
        super.onResume()
        if (!decided && dialog?.isShowing != true) show()
    }

    private fun show() {
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
        val b = AlertDialog.Builder(this)
            .setTitle(t("$server wants to mirror your screen", "$server wants to mirror your screen"))
            .setMessage(msg)
            .setPositiveButton(t("Let it", "Accept")) { _, _ -> accept() }
            .setNegativeButton(t("Nah", "Decline")) { _, _ -> decline() }
            .setOnCancelListener { decline() }
        if (!MirrorInput.available()) {
            b.setNeutralButton(t("Accessibility", "Accessibility settings")) { _, _ -> MirrorInput.accessibilitySettings(this) }
        }
        dialog = b.show()
    }

    private fun accept() {
        decided = true
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
        dialog?.setOnCancelListener(null)
        dialog?.dismiss()
        if (!decided && isFinishing) MirrorAsk.postState(this, "denied", ask)
        super.onDestroy()
    }

    companion object {
        const val ASK = "ask"
        private const val REQ = 42
    }
}
