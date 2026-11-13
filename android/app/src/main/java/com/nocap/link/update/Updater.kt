package com.nocap.link.update

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInfo
import android.content.pm.PackageInstaller
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.core.content.IntentCompat
import androidx.core.content.pm.PackageInfoCompat
import androidx.work.Constraints
import androidx.work.CoroutineWorker
import androidx.work.ExistingPeriodicWorkPolicy
import androidx.work.NetworkType
import androidx.work.PeriodicWorkRequestBuilder
import androidx.work.WorkManager
import androidx.work.WorkerParameters
import com.nocap.link.BuildConfig
import com.nocap.link.MainActivity
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.sync.Notices
import java.io.File
import java.io.IOException
import java.security.MessageDigest
import java.util.concurrent.TimeUnit
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request

/** Where the updater is, for Settings → About. */
sealed class UpdateState {
    data object Idle : UpdateState()
    data object Checking : UpdateState()
    data object UpToDate : UpdateState()
    data class Available(val release: Release, val apk: Asset, val version: AppVersion?, val sha256: String?) : UpdateState()
    data class NoApk(val release: Release) : UpdateState()
    data class Downloading(val done: Long, val total: Long) : UpdateState()
    data object Installing : UpdateState()
    data object WaitingForYou : UpdateState()
    data class Failed(val why: String) : UpdateState()
}

/**
 * The in-app updater: checks the latest NoCapOS release on GitHub, downloads
 * nocap-os.apk, checks it's signed with the same key as this app, and installs
 * it with Android's PackageInstaller session (so NoCap OS becomes its own
 * installer from then on). Nothing is contacted unless you check, or turn on
 * the daily check.
 */
object Updater {
    var state by mutableStateOf<UpdateState>(UpdateState.Idle)

    /** The newest release seen, and its app version (also when we're up to date). */
    var latest by mutableStateOf<Release?>(null)
    var latestVersion by mutableStateOf<AppVersion?>(null)
    private var lastCheck = 0L

    val busy get() = state is UpdateState.Checking || state is UpdateState.Downloading || state is UpdateState.Installing || state is UpdateState.WaitingForYou

    private const val ACTION_STATUS = "com.nocap.link.update.STATUS"
    private const val SP = "nocap_update"
    private const val MAX_APK = 250L * 1024 * 1024

    private val http by lazy {
        OkHttpClient.Builder().connectTimeout(15, TimeUnit.SECONDS).readTimeout(60, TimeUnit.SECONDS).followRedirects(true).build()
    }

    private fun get(url: String, json: Boolean = false): Request =
        Request.Builder().url(url)
            .header("User-Agent", "NoCapOS-app/${BuildConfig.VERSION_NAME}")
            .apply { if (json) header("Accept", "application/vnd.github+json") }
            .build()

    // ---- preferences (this phone only) ----

    private fun sp(ctx: Context) = ctx.getSharedPreferences(SP, Context.MODE_PRIVATE)

    /** Off until you turn it on: when Settings opens (at most every few hours) and once a day in the background. */
    fun autoOn(ctx: Context) = sp(ctx).getBoolean("auto", false)

    fun setAuto(ctx: Context, on: Boolean) {
        sp(ctx).edit().putBoolean("auto", on).apply()
        if (on) UpdateWorker.schedule(ctx) else UpdateWorker.cancel(ctx)
    }

    /** Checks now (force), or when the last check is a while ago. Call on the main thread. */
    suspend fun refresh(ctx: Context, force: Boolean) {
        val now = android.os.SystemClock.elapsedRealtime()
        if (busy) return
        if (!force && lastCheck != 0L && now - lastCheck < TimeUnit.HOURS.toMillis(6)) return
        lastCheck = now
        state = UpdateState.Checking
        state = check(ctx)
    }

    /** Is the linked NoCapOS server older than the latest release? */
    fun serverBehind(serverVersion: String?): Boolean {
        val tag = latest?.tag?.takeIf { it.isNotBlank() } ?: return false
        if (serverVersion.isNullOrBlank() || serverVersion == "?") return false
        return Releases.compareNames(tag, serverVersion) > 0
    }

    /** Set once an update was installed from inside the app. */
    fun updatedInApp(ctx: Context) = sp(ctx).getBoolean("updated_in_app", false)

    // ---- checking ----

    /** Fetches the latest release and works out whether it's newer. Runs on IO. */
    suspend fun check(ctx: Context): UpdateState = withContext(Dispatchers.IO) {
        try {
            val release = http.newCall(get(Releases.LATEST, json = true)).execute().use { r ->
                if (r.code == 404) throw IOException(t("No NoCapOS release published yet.", "No release has been published yet."))
                if (r.code == 403 || r.code == 429) throw IOException(t("GitHub says slow down. Try again in a bit.", "GitHub rate limit reached. Try again later."))
                if (!r.isSuccessful) throw IOException("GitHub: HTTP ${r.code}")
                Releases.parse(r.body?.string().orEmpty())
            }
            latest = release
            latestVersion = null
            val apk = Releases.pickApk(release.assets) ?: return@withContext UpdateState.NoApk(release)
            val meta = Releases.pickMeta(release.assets)?.let { m ->
                runCatching {
                    http.newCall(get(m.url)).execute().use { r -> if (r.isSuccessful) Releases.parseMeta(r.body?.string().orEmpty()) else null }
                }.getOrNull()
            }
            val version = Releases.versionOf(release, apk, meta)
            latestVersion = version
            when (Releases.isNewer(version, BuildConfig.VERSION_CODE.toLong(), BuildConfig.VERSION_NAME)) {
                false -> UpdateState.UpToDate
                else -> UpdateState.Available(release, apk, version, meta?.sha256)
            }
        } catch (e: Exception) {
            UpdateState.Failed(e.message ?: t("Couldn't reach GitHub.", "Couldn't reach GitHub."))
        }
    }

    // ---- downloading + checking the file ----

    private suspend fun download(ctx: Context, apk: Asset): File = withContext(Dispatchers.IO) {
        val dir = File(ctx.cacheDir, "updates").apply { mkdirs() }
        dir.listFiles()?.forEach { it.delete() }
        val out = File(dir, Releases.APK)
        http.newCall(get(apk.url)).execute().use { r ->
            if (!r.isSuccessful) throw IOException(t("Download failed (HTTP ${r.code}).", "Download failed (HTTP ${r.code})."))
            val body = r.body ?: throw IOException(t("Download came back empty.", "The download was empty."))
            val total = body.contentLength().takeIf { it > 0 } ?: apk.size
            if (total > MAX_APK) throw IOException(t("That file is way too big to be NoCap OS.", "The file is too large to be the NoCap OS app."))
            var done = 0L
            var shown = -1L
            body.byteStream().use { input ->
                out.outputStream().use { o ->
                    val buf = ByteArray(64 * 1024)
                    while (true) {
                        val n = input.read(buf)
                        if (n < 0) break
                        o.write(buf, 0, n)
                        done += n
                        if (done > MAX_APK) throw IOException(t("That file is way too big to be NoCap OS.", "The file is too large to be the NoCap OS app."))
                        val step = done / (256 * 1024)
                        if (step != shown) { shown = step; state = UpdateState.Downloading(done, total) }
                    }
                }
            }
            state = UpdateState.Downloading(done, total)
        }
        out
    }

    private fun sha256(f: File): String {
        val md = MessageDigest.getInstance("SHA-256")
        f.inputStream().use { input ->
            val buf = ByteArray(64 * 1024)
            while (true) { val n = input.read(buf); if (n < 0) break; md.update(buf, 0, n) }
        }
        return md.digest().joinToString("") { "%02x".format(it) }
    }

    private fun certHashes(sigs: Array<out android.content.pm.Signature>?): Set<String> =
        sigs.orEmpty().map { s -> MessageDigest.getInstance("SHA-256").digest(s.toByteArray()).joinToString("") { "%02x".format(it) } }.toSet()

    /** (current signers, signing history) of an installed package or an APK file. */
    @Suppress("DEPRECATION")
    private fun signers(pi: PackageInfo?): Pair<Set<String>, Set<String>> {
        if (pi == null) return emptySet<String>() to emptySet()
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) {
            val si = pi.signingInfo
            if (si != null) {
                return if (si.hasMultipleSigners()) certHashes(si.apkContentsSigners) to emptySet()
                else {
                    val history = certHashes(si.signingCertificateHistory)
                    // The current signer is the last one in the history.
                    val current = si.signingCertificateHistory?.lastOrNull()?.let { certHashes(arrayOf(it)) } ?: emptySet()
                    current to history
                }
            }
        }
        return certHashes(pi.signatures) to emptySet()
    }

    @Suppress("DEPRECATION")
    private fun flags() = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) PackageManager.GET_SIGNING_CERTIFICATES else PackageManager.GET_SIGNATURES

    /** Checks the downloaded APK is NoCap OS, newer, and signed like this app. Returns its version name. */
    @Suppress("DEPRECATION")
    private fun verify(ctx: Context, f: File, sha: String?): String {
        if (sha != null && sha256(f) != sha) throw IOException(t("The download doesn't match its checksum. Not installing it.", "Checksum mismatch. The download was not installed."))
        val pm = ctx.packageManager
        var archive = pm.getPackageArchiveInfo(f.path, flags())
        if (archive != null && signers(archive).first.isEmpty()) {
            // Some Android versions only fill the old field for files.
            archive = pm.getPackageArchiveInfo(f.path, PackageManager.GET_SIGNATURES) ?: archive
        }
        if (archive == null) throw IOException(t("That file isn't an app Android can read.", "The downloaded file isn't a valid APK."))
        if (archive.packageName != ctx.packageName) throw IOException(t("That's not the NoCap OS app (${archive.packageName}).", "The APK is a different app (${archive.packageName})."))
        val code = PackageInfoCompat.getLongVersionCode(archive)
        if (code <= BuildConfig.VERSION_CODE) {
            state = UpdateState.UpToDate
            throw UpToDate()
        }
        val mine = signers(pm.getPackageInfo(ctx.packageName, flags())).first
        val (theirs, history) = signers(archive)
        if (!Releases.signersMatch(mine, theirs, history)) {
            throw IOException(
                t(
                    "This build is signed with a different key than yours, so Android won't take it as an update. Grab the APK by hand if you want to switch.",
                    "The update is signed with a different key than the installed app, so Android would reject it. Install it manually if you intend to switch builds.",
                ),
            )
        }
        return archive.versionName ?: code.toString()
    }

    private class UpToDate : Exception()

    // ---- installing ----

    fun canInstall(ctx: Context) = ctx.packageManager.canRequestPackageInstalls()

    fun unknownAppsIntent(ctx: Context) =
        Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${ctx.packageName}"))

    /** Download, check and install the release. Call from a coroutine on the main thread. */
    suspend fun update(ctx: Context, a: UpdateState.Available) {
        if (!canInstall(ctx)) {
            runCatching { ctx.startActivity(unknownAppsIntent(ctx).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
            state = UpdateState.Failed(t("Allow \"Install unknown apps\" for NoCap OS, then tap update again.", "Allow \"Install unknown apps\" for NoCap OS, then try again."))
            return
        }
        try {
            state = UpdateState.Downloading(0, a.apk.size)
            val f = download(ctx, a.apk)
            withContext(Dispatchers.IO) { verify(ctx, f, a.sha256) }
            state = UpdateState.Installing
            withContext(Dispatchers.IO) { install(ctx, f) }
        } catch (_: UpToDate) {
            state = UpdateState.UpToDate
        } catch (e: Exception) {
            state = UpdateState.Failed(e.message ?: t("Update failed.", "The update failed."))
        }
    }

    private fun install(ctx: Context, f: File) {
        val pi = ctx.packageManager.packageInstaller
        val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL).apply {
            setAppPackageName(ctx.packageName)
            setSize(f.length())
            setInstallReason(PackageManager.INSTALL_REASON_USER)
            // Once NoCap OS installed itself, later updates may go through without a prompt.
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED)
        }
        val id = pi.createSession(params)
        try {
            pi.openSession(id).use { s ->
                s.openWrite(Releases.APK, 0, f.length()).use { out ->
                    f.inputStream().use { it.copyTo(out, 64 * 1024) }
                    s.fsync(out)
                }
                val i = Intent(ctx, UpdateReceiver::class.java).setAction(ACTION_STATUS).setPackage(ctx.packageName)
                // Android adds the result to this intent, so it has to be mutable.
                val flags = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_MUTABLE else PendingIntent.FLAG_UPDATE_CURRENT
                sp(ctx).edit().putBoolean("pending", true).apply()
                s.commit(PendingIntent.getBroadcast(ctx, id, i, flags).intentSender)
            }
        } catch (e: Exception) {
            runCatching { pi.abandonSession(id) }
            throw e
        }
    }

    internal fun onStatus(ctx: Context, i: Intent) {
        val status = i.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)
        when (status) {
            PackageInstaller.STATUS_PENDING_USER_ACTION -> {
                val confirm = IntentCompat.getParcelableExtra(i, Intent.EXTRA_INTENT, Intent::class.java)
                state = UpdateState.WaitingForYou
                if (confirm != null) runCatching { ctx.startActivity(confirm.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
                    .onFailure { state = UpdateState.Failed(t("Android didn't show the install prompt. Try again.", "Android didn't show the install prompt. Try again.")) }
            }
            PackageInstaller.STATUS_SUCCESS -> {
                sp(ctx).edit().putBoolean("updated_in_app", true).putBoolean("pending", false).apply()
                state = UpdateState.UpToDate
            }
            else -> {
                sp(ctx).edit().putBoolean("pending", false).apply()
                val msg = i.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE)
                state = if (status == PackageInstaller.STATUS_FAILURE_ABORTED) UpdateState.Idle
                else UpdateState.Failed(
                    when (status) {
                        PackageInstaller.STATUS_FAILURE_CONFLICT, PackageInstaller.STATUS_FAILURE_INCOMPATIBLE ->
                            t("Android refused the update (different signing key or older build).", "Android rejected the update (signing key or version conflict).")
                        PackageInstaller.STATUS_FAILURE_STORAGE -> t("Not enough space for the update.", "Not enough storage for the update.")
                        PackageInstaller.STATUS_FAILURE_BLOCKED -> t("Android blocked the install.", "The install was blocked by Android.")
                        else -> t("Install failed", "Install failed") + (msg?.let { ": $it" } ?: ".")
                    },
                )
            }
        }
    }

    /** After the app was replaced by an in-app update. */
    internal fun onReplaced(ctx: Context) {
        val s = sp(ctx)
        if (s.getBoolean("pending", false)) s.edit().putBoolean("updated_in_app", true).putBoolean("pending", false).apply()
    }

    // ---- daily check ----

    internal suspend fun dailyCheck(ctx: Context) {
        val r = check(ctx) as? UpdateState.Available ?: return
        val key = r.version?.let { "${it.code}/${it.name}" } ?: r.release.tag
        val s = sp(ctx)
        if (s.getString("notified", null) == key) return
        Prefs.init(ctx)
        val v = r.version?.name ?: r.release.tag
        Notices.show(
            ctx, 7, t("NoCap OS update dropped", "NoCap OS update available"),
            t("$v is out. Open Settings → About to install it.", "Version $v is available. Open Settings → About to install it."),
            Intent(ctx, MainActivity::class.java).setAction(MainActivity.ACTION_SETTINGS),
        )
        s.edit().putString("notified", key).apply()
    }
}

/** Gets the result of an update install from Android. */
class UpdateReceiver : BroadcastReceiver() {
    override fun onReceive(ctx: Context, i: Intent) {
        when (i.action) {
            Intent.ACTION_MY_PACKAGE_REPLACED -> Updater.onReplaced(ctx)
            else -> Updater.onStatus(ctx, i)
        }
    }
}

/** Optional: once a day, a notification when a new NoCap OS build is out. */
class UpdateWorker(ctx: Context, params: WorkerParameters) : CoroutineWorker(ctx, params) {
    override suspend fun doWork(): Result {
        runCatching { Updater.dailyCheck(applicationContext) }
        return Result.success()
    }

    companion object {
        private const val NAME = "nocap-update-check"

        fun schedule(ctx: Context) {
            val net = Constraints.Builder().setRequiredNetworkType(NetworkType.CONNECTED).build()
            WorkManager.getInstance(ctx).enqueueUniquePeriodicWork(
                NAME, ExistingPeriodicWorkPolicy.KEEP,
                PeriodicWorkRequestBuilder<UpdateWorker>(1, TimeUnit.DAYS).setConstraints(net).build(),
            )
        }

        fun cancel(ctx: Context) {
            WorkManager.getInstance(ctx).cancelUniqueWork(NAME)
        }
    }
}
