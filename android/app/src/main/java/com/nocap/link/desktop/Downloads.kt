package com.nocap.link.desktop

import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import android.util.Base64
import android.webkit.CookieManager
import android.webkit.JavascriptInterface
import android.webkit.MimeTypeMap
import android.webkit.URLUtil
import com.nocap.link.data.Link
import com.nocap.link.net.NoCapApi
import com.nocap.link.sync.Notices
import java.io.File
import java.io.IOException
import java.io.OutputStream
import okhttp3.Request
import org.json.JSONObject

/**
 * Downloads from the web desktop. Normal links are fetched with the pinned
 * client (and the web view's cookies); blob: links are read in the page and
 * handed over through this bridge. Files land in Downloads/NoCap.
 */
class DownloadBridge(
    private val ctx: Context,
    private val link: Link,
    private val pageOrigin: () -> String?,
    private val onMessage: (String) -> Unit,
) {
    private val api = NoCapApi(link)

    /** Runs in the page: remembers <a download="name"> and keeps blob URLs alive long enough to read. */
    val hookJs = """
        (function(){if(window.__nocapLink)return;window.__nocapLink=1;
        document.addEventListener('click',function(e){var a=e.target&&e.target.closest&&e.target.closest('a[download]');if(a){window.__nocapDlName=a.getAttribute('download')||'';}},true);
        var rv=URL.revokeObjectURL.bind(URL);URL.revokeObjectURL=function(u){setTimeout(function(){rv(u)},120000)};})();
    """.trimIndent()

    fun blobJs(url: String): String = """
        (function(u){var n=window.__nocapDlName||'';window.__nocapDlName='';
        fetch(u).then(function(r){return r.blob()}).then(function(b){var f=new FileReader();
        f.onloadend=function(){var s=String(f.result);NoCapDownloads.save(n,b.type||'',s.substring(s.indexOf(',')+1))};
        f.readAsDataURL(b)}).catch(function(e){NoCapDownloads.failed(String(e))})})(${JSONObject.quote(url)})
    """.trimIndent()

    private fun trusted() = pageOrigin() == link.server || pageOrigin() == api.server

    @JavascriptInterface
    fun save(name: String, mime: String, base64: String) {
        if (!trusted()) return
        val bytes = try { Base64.decode(base64, Base64.DEFAULT) } catch (_: IllegalArgumentException) { return onMessage("That download came through broken.") }
        val file = name.ifBlank {
            val ext = MimeTypeMap.getSingleton().getExtensionFromMimeType(mime)
            if (ext.isNullOrBlank()) "download" else "download.$ext"
        }
        Thread { finish(file, mime) { it.write(bytes) } }.start()
    }

    @JavascriptInterface
    fun failed(why: String) {
        onMessage("Download didn't work: $why")
    }

    /** A normal http(s) link from the server, fetched with the pinned client. */
    fun fetch(url: String, userAgent: String?, contentDisposition: String?, mime: String?) {
        if (!api.sameServer(url)) return onMessage("Download blocked: it's not from your server.")
        onMessage("Downloading…")
        Thread {
            try {
                val req = Request.Builder().url(url)
                CookieManager.getInstance().getCookie(url)?.let { req.header("Cookie", it) }
                if (!userAgent.isNullOrBlank()) req.header("User-Agent", userAgent)
                api.http.newCall(req.build()).execute().use { r ->
                    if (!r.isSuccessful) throw IOException("HTTP ${r.code}")
                    val cd = r.header("Content-Disposition") ?: contentDisposition
                    val type = r.header("Content-Type")?.substringBefore(';')?.trim() ?: mime
                    val name = URLUtil.guessFileName(url, cd, type)
                    val body = r.body ?: throw IOException("empty")
                    finish(name, type ?: "") { out -> body.byteStream().use { it.copyTo(out) } }
                }
            } catch (e: Exception) {
                onMessage("Download didn't work: ${e.message ?: "network error"}")
            }
        }.start()
    }

    private fun finish(name: String, mime: String, write: (OutputStream) -> Unit) {
        try {
            val uri = saveToDownloads(ctx, safeName(name), mime, write)
            onMessage("Saved to Downloads/NoCap: $name")
            val open = uri?.let {
                Intent(Intent.ACTION_VIEW).setDataAndType(it, mime.ifBlank { "*/*" }).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
            }
            Notices.show(ctx, 1000 + (name.hashCode() and 0xfff), "Download saved", "$name is in Downloads/NoCap.", open)
        } catch (e: Exception) {
            onMessage("Couldn't save $name: ${e.message}")
        }
    }

    companion object {
        private fun safeName(n: String): String = n.replace(Regex("[\\\\/:*?\"<>|\\p{Cntrl}]"), "_").trim().ifBlank { "download" }.take(120)

        /** Writes into Downloads/NoCap; returns a content URI where one exists. */
        fun saveToDownloads(ctx: Context, name: String, mime: String, write: (OutputStream) -> Unit): Uri? {
            val type = mime.ifBlank { MimeTypeMap.getSingleton().getMimeTypeFromExtension(name.substringAfterLast('.', "")) ?: "application/octet-stream" }
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                val cr = ctx.contentResolver
                val values = ContentValues().apply {
                    put(MediaStore.MediaColumns.DISPLAY_NAME, name)
                    put(MediaStore.MediaColumns.MIME_TYPE, type)
                    put(MediaStore.MediaColumns.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS + "/NoCap")
                    put(MediaStore.MediaColumns.IS_PENDING, 1)
                }
                val uri = cr.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values) ?: throw IOException("no space for it")
                try {
                    cr.openOutputStream(uri)?.use(write) ?: throw IOException("can't write")
                } catch (e: Exception) {
                    cr.delete(uri, null, null)
                    throw e
                }
                cr.update(uri, ContentValues().apply { put(MediaStore.MediaColumns.IS_PENDING, 0) }, null, null)
                return uri
            }
            // Android 8–9: the app's own Downloads folder (no storage permission needed).
            val dir = File(ctx.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS), "NoCap").apply { mkdirs() }
            var f = File(dir, name)
            var i = 1
            while (f.exists()) f = File(dir, name.substringBeforeLast('.') + " (${i++})" + name.substringAfterLast('.', "").let { if (it.isEmpty()) "" else ".$it" })
            f.outputStream().use(write)
            return null
        }
    }
}
