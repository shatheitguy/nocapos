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
        Log.w(TAG, "$where failed", e)
        runCatching { write(null, "Caught in $where (the app kept running)", e) }
    }

    /** Something went wrong but was handled; kept only in the system log. */
    fun warn(where: String, e: Throwable) {
        Log.w(TAG, "$where failed", e)
    }

    @Synchronized
    private fun write(ctx: Context?, headline: String, e: Throwable) {
        val f = file(ctx) ?: return
        f.parentFile?.mkdirs()
        f.writeText(report(headline, e, Date()).take(MAX))
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
