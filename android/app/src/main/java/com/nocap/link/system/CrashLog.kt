package com.nocap.link.system

import android.content.Context
import android.os.Build
import android.util.Log
import com.nocap.link.BuildConfig
import java.io.File
import java.io.PrintWriter
import java.io.StringWriter
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlinx.coroutines.CoroutineExceptionHandler

/**
 * Keeps the last crash on this phone, so Settings → About can show it and
 * offer to send it to your own linked NoCap server. Nothing goes anywhere
 * else, and nothing is sent unless you say so.
 */
object CrashLog {
    private const val TAG = "NoCapOS"
    private const val MAX = 48 * 1024
    @Volatile private var dir: File? = null

    private fun file(ctx: Context?) = (dir ?: ctx?.let { File(it.applicationContext.filesDir, "crash") })?.let { File(it, "last.txt") }
    private fun sentFile(ctx: Context) = File(File(ctx.applicationContext.filesDir, "crash"), "sent")

    /** Wraps Android's crash handler: write the report, then let the app die as usual. */
    fun install(ctx: Context) {
        val d = File(ctx.applicationContext.filesDir, "crash")
        dir = d
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        if (previous is Handler) return
        Thread.setDefaultUncaughtExceptionHandler(Handler(previous))
    }

    private class Handler(val previous: Thread.UncaughtExceptionHandler?) : Thread.UncaughtExceptionHandler {
        override fun uncaughtException(t: Thread, e: Throwable) {
            runCatching { write(null, "Crash on thread \"${t.name}\"", e) }
            previous?.uncaughtException(t, e)
        }
    }

    /**
     * For background work that must not take the app down (the NoCap Link
     * long-poll, sync): log it, keep it as the last problem, carry on.
     */
    fun handler(where: String) = CoroutineExceptionHandler { _, e ->
        failure(where, e)
    }

    /** where → times it failed in the current window, and when that window started. */
    private val failures = HashMap<String, Pair<Int, Long>>()
    private const val REPEAT = 5
    private const val WINDOW_MS = 30 * 60_000L

    /**
     * Something that failed but was handled (sync, the NoCap Link poll). One
     * failure is normal (offline); the same thing failing again and again is
     * kept as a report, once per window.
     */
    fun failure(where: String, e: Throwable) {
        Log.w(TAG, "$where failed", e)
        val now = System.currentTimeMillis()
        val n = synchronized(failures) {
            val (count, since) = failures[where]?.takeIf { now - it.second < WINDOW_MS } ?: (0 to now)
            (count + 1).also { failures[where] = it to since }
        }
        if (n == REPEAT) runCatching { write(null, "Keeps failing: $where ($REPEAT times in ${WINDOW_MS / 60_000} minutes; the app kept running)", e) }
    }

    /**
     * Android 11+: why the app's process ended last time. Picks up ANRs ("app
     * isn't responding") and native crashes, which the Java crash handler
     * never sees. Each exit is reported once. Call off the main thread.
     */
    fun checkExits(ctx: Context) {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.R) return
        val sp = ctx.getSharedPreferences("nocap_crash", Context.MODE_PRIVATE)
        val seen = sp.getLong("exit_seen", 0L)
        val am = ctx.getSystemService(android.app.ActivityManager::class.java) ?: return
        val exits = runCatching { am.getHistoricalProcessExitReasons(ctx.packageName, 0, 5) }.getOrNull().orEmpty()
        val newest = exits.maxOfOrNull { it.timestamp } ?: return
        if (seen == 0L) { sp.edit().putLong("exit_seen", newest).apply(); return } // first run: don't dig up old history
        val hit = exits.filter { it.timestamp > seen }
            .firstOrNull { it.reason == android.app.ApplicationExitInfo.REASON_ANR || it.reason == android.app.ApplicationExitInfo.REASON_CRASH_NATIVE }
        sp.edit().putLong("exit_seen", maxOf(seen, newest)).apply()
        if (hit == null) return
        val what = if (hit.reason == android.app.ApplicationExitInfo.REASON_ANR) "App not responding (ANR)" else "Native crash"
        val trace = runCatching { hit.traceInputStream?.bufferedReader()?.use { it.readText() } }.getOrNull()
            ?.lineSequence()?.take(120)?.joinToString("\n").orEmpty()
        writeText(ctx, exitReport(what, hit.description, trace, Date(hit.timestamp)))
    }

    fun exitReport(what: String, description: String?, trace: String, at: Date): String {
        val time = SimpleDateFormat("yyyy-MM-dd HH:mm:ss Z", Locale.US).format(at)
        return buildString {
            appendLine("NoCap OS crash report")
            appendLine("Time: $time")
            appendLine("App: ${BuildConfig.APPLICATION_ID} ${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE}, ${BuildConfig.BUILD_TYPE})")
            appendLine("Android: ${Build.VERSION.RELEASE} (API ${Build.VERSION.SDK_INT})")
            appendLine("Phone: ${Build.MANUFACTURER} ${Build.MODEL}")
            appendLine(what + (description?.takeIf { it.isNotBlank() }?.let { ": $it" } ?: ""))
            if (trace.isNotBlank()) { appendLine(); append(trace) }
        }
    }

    private val urls = Regex("""\b(?:https?|wss?|content|file)://\S+""", RegexOption.IGNORE_CASE)
    private val emails = Regex("""[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}""")
    private val ipv4 = Regex("""\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b""")
    private val ipv6 = Regex("""\b(?:[0-9a-fA-F]{1,4}:){3,7}[0-9a-fA-F]{1,4}\b""")
    // Up to a quote, a bracket, ": " or the end of the line (file names can have spaces).
    private val paths = Regex("""(?:/storage|/sdcard|/data/user|/data/data|/data/media|/mnt|/Users|/home)/(?:[^\n"'()\[\]:]|:(?! ))*""")
    private val tokens = Regex("""(?i)\b(token|ticket|key|password|passwd|secret|code|auth|authorization|cookie|session)(["']?\s*[:=]\s*["']?)[^\s"',;&]+""")
    private val bearer = Regex("""(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}""")
    // Long runs of letters and digits (both): keys, tickets, device ids.
    private val secrets = Regex("""\b(?=[A-Za-z0-9_-]*\d)(?=[A-Za-z0-9_-]*[A-Za-z])[A-Za-z0-9_-]{32,}\b""")
    // "Unable to resolve host "my.server"", "Hostname x not verified": your server's name.
    private val hosts = Regex("""(?i)\b(host(?:name)?)(\s*[=:]\s*["']?|\s+["']?)[A-Za-z0-9][A-Za-z0-9.-]*""")

    /**
     * Blanks out what could be personal before a report leaves the phone:
     * addresses, emails, IPs, file paths and anything that looks like a
     * token. Stack frames, versions and the phone model stay.
     */
    fun sanitize(text: String): String {
        var s = text
        s = urls.replace(s, "<url>")
        s = emails.replace(s, "<email>")
        s = paths.replace(s, "<path>")
        s = bearer.replace(s) { it.groupValues[1] + " <hidden>" }
        s = tokens.replace(s) { it.groupValues[1] + it.groupValues[2] + "<hidden>" }
        s = hosts.replace(s) { it.groupValues[1] + it.groupValues[2] + "<host>" }
        s = secrets.replace(s, "<hidden>")
        s = ipv6.replace(s, "<ip>")
        s = ipv4.replace(s, "<ip>")
        return s
    }

    /** A short title for an issue: the error class and what happened. */
    fun headline(report: String): String {
        val lines = report.lines()
        val what = lines.getOrNull(5)?.takeIf { it.isNotBlank() } ?: "Crash"
        val err = lines.drop(6).firstOrNull { it.isNotBlank() && !it.startsWith("\t") }
        val cls = err?.substringBefore(':')?.substringAfterLast('.')?.takeIf { it.isNotBlank() && !it.contains(' ') }
        return ("Android: " + (cls?.let { "$it, " } ?: "") + what).take(120)
    }

    fun bugTitle(what: String) = "Android: " + sanitize(what.lineSequence().firstOrNull().orEmpty().trim()).ifBlank { "Bug report" }.take(100)

    /** A bug report: what you wrote, the app and phone, and the last crash if there is one. */
    fun bugReport(what: String, lastCrash: String?): String = buildString {
        appendLine("NoCap OS bug report")
        appendLine("App: ${BuildConfig.APPLICATION_ID} ${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE}, ${BuildConfig.BUILD_TYPE})")
        appendLine("Android: ${Build.VERSION.RELEASE} (API ${Build.VERSION.SDK_INT})")
        appendLine("Phone: ${Build.MANUFACTURER} ${Build.MODEL}")
        appendLine()
        appendLine(what.trim().ifBlank { "(no description)" })
        if (!lastCrash.isNullOrBlank()) {
            appendLine()
            appendLine("Last crash on this phone:")
            append(lastCrash.take(6000))
        }
    }

    const val ISSUES = "https://github.com/shatheitguy/nocapos/issues/new"

    /** GitHub (and browsers) choke on longer links. */
    const val MAX_URL = 7500

    /** A prefilled "new issue" link. GitHub caps URL length, so long reports are cut to fit. */
    fun issueUrl(title: String, body: String): String {
        val enc = { v: String -> java.net.URLEncoder.encode(v, "UTF-8").replace("+", "%20") }
        val fence = "```"
        val head = "$ISSUES?title=${enc(title.take(200))}&body="
        fun build(b: String) = head + enc("$fence\n$b\n$fence\n")
        var url = build(body)
        var n = body.length
        while (url.length > MAX_URL && n > 0) {
            n = n * 3 / 4
            url = build(body.take(n) + "\n…(cut to fit the link)")
        }
        return url
    }

    /** Something went wrong but was handled; kept only in the system log. */
    fun warn(where: String, e: Throwable) {
        Log.w(TAG, "$where failed", e)
    }

    @Synchronized
    private fun write(ctx: Context?, headline: String, e: Throwable) {
        writeText(ctx, report(headline, e, Date()))
    }

    @Synchronized
    private fun writeText(ctx: Context?, text: String) {
        val f = file(ctx) ?: return
        f.parentFile?.mkdirs()
        f.writeText(text.take(MAX))
        File(f.parentFile, "sent").delete()
    }

    /** The report as text: when, which app and phone, what happened. */
    fun report(headline: String, e: Throwable, at: Date): String {
        val sw = StringWriter()
        e.printStackTrace(PrintWriter(sw))
        val time = SimpleDateFormat("yyyy-MM-dd HH:mm:ss Z", Locale.US).format(at)
        return buildString {
            appendLine("NoCap OS crash report")
            appendLine("Time: $time")
            appendLine("App: ${BuildConfig.APPLICATION_ID} ${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE}, ${BuildConfig.BUILD_TYPE})")
            appendLine("Android: ${Build.VERSION.RELEASE} (API ${Build.VERSION.SDK_INT})")
            appendLine("Phone: ${Build.MANUFACTURER} ${Build.MODEL}")
            appendLine(headline)
            appendLine()
            append(sw.toString())
        }
    }

    /** The last report, or null when there's none. */
    fun last(ctx: Context): String? = runCatching { file(ctx)?.takeIf { it.exists() }?.readText() }.getOrNull()?.takeIf { it.isNotBlank() }

    /** A report that hasn't been sent or dismissed yet (asked about once on the next start). */
    fun pending(ctx: Context): String? = last(ctx)?.takeIf { !sentFile(ctx).exists() }

    /** Sent to the server, or the person said no: don't ask again for this one. */
    fun settle(ctx: Context) {
        runCatching { sentFile(ctx).apply { parentFile?.mkdirs() }.writeText("1") }
    }

    fun clear(ctx: Context) {
        runCatching { file(ctx)?.delete(); sentFile(ctx).delete() }
    }
}
