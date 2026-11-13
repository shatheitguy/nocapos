package com.nocap.link.desktop

import android.annotation.SuppressLint
import android.app.Activity
import android.content.ActivityNotFoundException
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Color
import android.net.http.SslError
import android.net.Uri
import android.os.Bundle
import android.view.ViewGroup.LayoutParams.MATCH_PARENT
import android.webkit.CookieManager
import android.webkit.SslErrorHandler
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebStorage
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import android.widget.Toast as SystemToast
import androidx.activity.addCallback
import androidx.activity.ComponentActivity
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.Spacer
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.ComposeView
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.lifecycle.lifecycleScope
import com.nocap.link.BuildConfig
import com.nocap.link.data.Link
import com.nocap.link.data.Vault
import com.nocap.link.MainActivity
import com.nocap.link.net.ApiError
import com.nocap.link.net.CertChanged
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.Pins
import com.nocap.link.net.ServerTooOld
import com.nocap.link.R
import com.nocap.link.system.CrashLog
import com.nocap.link.ui.GhostButton
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.NoCapTheme
import com.nocap.link.ui.RedButton
import com.nocap.link.ui.Toast
import com.nocap.link.ui.Wallpaper
import java.lang.ref.WeakReference
import kotlinx.coroutines.delay
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private sealed interface DeskState {
    data object Loading : DeskState
    data object Ready : DeskState
    data class Failed(val why: String, val canRetry: Boolean = true) : DeskState
}

/**
 * "Open my NoCap desktop": the server's web OS, full screen, signed in with
 * a one-time ticket. Only the pinned certificate is accepted. The activity
 * lives in its own task, so the session stays put while you hop back to the
 * home screen; back at the top level just steps out of the way.
 */
class DesktopActivity : ComponentActivity() {
    private lateinit var web: WebView
    private lateinit var link: Link
    private lateinit var downloads: DownloadBridge
    private var state by mutableStateOf<DeskState>(DeskState.Loading)
    private var message by mutableStateOf<String?>(null)
    @Volatile private var pageOrigin: String? = null
    private var fileCallback: ValueCallback<Array<Uri>>? = null

    private val pickFiles = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { r ->
        val cb = fileCallback
        fileCallback = null
        cb?.onReceiveValue(picked(r.resultCode, r.data))
    }

    @SuppressLint("SetJavaScriptEnabled", "JavascriptInterface")
    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        link = runCatching { Vault.get(this).link }.getOrNull() ?: run { finish(); return }
        takeOpen(intent)
        current = WeakReference(this)

        // The system WebView can be missing, disabled or mid-update.
        web = try {
            WebView(this)
        } catch (e: Exception) {
            CrashLog.warn("desktop web view", e)
            SystemToast.makeText(applicationContext, MainActivity.friendlyWebView(), SystemToast.LENGTH_LONG).show()
            finish()
            return
        }
        web.setBackgroundColor(Color.parseColor("#07080B"))
        with(web.settings) {
            javaScriptEnabled = true
            domStorageEnabled = true
            mediaPlaybackRequiresUserGesture = false
            allowFileAccess = false
            allowContentAccess = false
            mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
            setSupportMultipleWindows(false)
            userAgentString = "$userAgentString NoCapLink/${BuildConfig.VERSION_NAME}"
        }
        CookieManager.getInstance().apply {
            setAcceptCookie(true)
            setAcceptThirdPartyCookies(web, false)
        }
        downloads = DownloadBridge(applicationContext, link, { pageOrigin }) { msg -> runOnUiThread { message = msg } }
        web.addJavascriptInterface(downloads, "NoCapDownloads")
        web.webViewClient = Client()
        web.webChromeClient = Chrome()
        web.setDownloadListener { url, ua, cd, mime, _ -> onDownload(url, ua, cd, mime) }

        val overlay = ComposeView(this).apply { setContent { NoCapTheme { Overlay() } } }
        val root = FrameLayout(this).apply {
            setBackgroundColor(Color.parseColor("#07080B"))
            addView(web, FrameLayout.LayoutParams(MATCH_PARENT, MATCH_PARENT))
            addView(overlay, FrameLayout.LayoutParams(MATCH_PARENT, MATCH_PARENT))
        }
        ViewCompat.setOnApplyWindowInsetsListener(root) { v, insets ->
            val b = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.ime())
            v.setPadding(b.left, b.top, b.right, b.bottom)
            WindowInsetsCompat.CONSUMED
        }
        setContentView(root)

        onBackPressedDispatcher.addCallback(this) {
            if (web.canGoBack()) web.goBack() else moveTaskToBack(true)
        }
        signIn()
    }

    private fun onDownload(url: String, ua: String?, cd: String?, mime: String?) {
        if (url.startsWith("blob:")) {
            web.evaluateJavascript(downloads.blobJs(url), null)
        } else if (url.startsWith("http")) {
            downloads.fetch(url, ua, cd, mime)
        } else {
            message = "Can't save that kind of download."
        }
    }

    /** Get a fresh one-time ticket and open the desktop with it. */
    private fun signIn() {
        state = DeskState.Loading
        lifecycleScope.launch {
            try {
                val url = withContext(Dispatchers.IO) { NoCapApi(link).webSession() }
                if (NoCapApi.origin(url) != com.nocap.link.net.Endpoint.server(link)) throw IllegalStateException("The server sent an address that isn't itself.")
                web.loadUrl(withOpen(url))
            } catch (e: ServerTooOld) {
                // An older NoCapOS without phone sign-in: say so, with its version.
                val v = runCatching { withContext(Dispatchers.IO) { NoCapApi(link).pairInfo().version } }.getOrNull()
                state = DeskState.Failed(MainActivity.needsUpdate(v), canRetry = true)
            } catch (e: CertChanged) {
                state = DeskState.Failed("Your server's certificate changed. Open NoCap OS on the home screen to check it before signing in.", canRetry = false)
            } catch (e: ApiError) {
                state = DeskState.Failed(
                    if (e.status == 401) "This phone isn't linked anymore. Link it again from the home screen." else e.message ?: "Server error",
                    canRetry = e.status != 401,
                )
            } catch (e: Exception) {
                state = DeskState.Failed(MainActivity.friendly(e))
            }
        }
    }

    override fun onPause() {
        super.onPause()
        if (::web.isInitialized) runCatching { CookieManager.getInstance().flush() }
    }

    override fun onDestroy() {
        if (current?.get() === this) current = null
        if (::web.isInitialized) {
            fileCallback?.onReceiveValue(null)
            web.destroy()
        }
        super.onDestroy()
    }

    private fun picked(code: Int, data: Intent?): Array<Uri>? {
        if (code != Activity.RESULT_OK || data == null) return null
        data.clipData?.let { c -> return Array(c.itemCount) { c.getItemAt(it).uri } }
        return data.data?.let { arrayOf(it) }
    }

    private fun pinOk(view: WebView): Boolean {
        val url = view.url ?: return true
        if (!url.startsWith("https://")) return true
        val pin = link.pin ?: return false
        val leaf = Pins.x509(view.certificate) ?: return false
        return Pins.sha256(leaf).equals(pin, ignoreCase = true)
    }

    private fun blockPage(view: WebView) {
        view.stopLoading()
        view.loadUrl("about:blank")
        state = DeskState.Failed("This server's certificate doesn't match the one this phone trusts. Not loading it.", canRetry = false)
    }

    private inner class Client : WebViewClient() {
        override fun onReceivedSslError(view: WebView, handler: SslErrorHandler, error: SslError) {
            val leaf = Pins.x509(error.certificate)
            val pin = link.pin
            if (pin != null && leaf != null && Pins.sha256(leaf).equals(pin, ignoreCase = true)) {
                handler.proceed() // the self-signed certificate we pinned
            } else {
                handler.cancel()
                state = DeskState.Failed("This server's certificate doesn't match the one this phone trusts. Not loading it.", canRetry = false)
            }
        }

        override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
            if (!request.isForMainFrame) return false
            val url = request.url.toString()
            if (NoCapApi.origin(url).let { it == link.server || it == com.nocap.link.net.Endpoint.server(link) }) return false
            // Anything else (server apps on other ports, outside links) opens in a browser.
            try {
                startActivity(Intent(Intent.ACTION_VIEW, request.url).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            } catch (_: ActivityNotFoundException) {
                message = "No browser on this phone for that link."
            }
            return true
        }

        override fun onPageStarted(view: WebView, url: String?, favicon: Bitmap?) {
            pageOrigin = url?.let { NoCapApi.origin(it) }
        }

        override fun onPageFinished(view: WebView, url: String?) {
            if (url == null || url == "about:blank") return
            if (!pinOk(view)) return blockPage(view)
            pageOrigin = NoCapApi.origin(url)
            if (pageOrigin == link.server || pageOrigin == com.nocap.link.net.Endpoint.server(link)) view.evaluateJavascript(downloads.hookJs, null)
            if (state is DeskState.Loading) state = DeskState.Ready
        }

        override fun onReceivedError(view: WebView, request: WebResourceRequest, error: WebResourceError) {
            if (request.isForMainFrame && state !is DeskState.Failed) {
                state = DeskState.Failed("Can't reach your server: ${error.description}")
            }
        }
    }

    private inner class Chrome : WebChromeClient() {
        override fun onShowFileChooser(view: WebView, callback: ValueCallback<Array<Uri>>, params: FileChooserParams): Boolean {
            fileCallback?.onReceiveValue(null)
            fileCallback = callback
            val intent = params.createIntent().apply {
                if (params.mode == FileChooserParams.MODE_OPEN_MULTIPLE) putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
            }
            return try {
                pickFiles.launch(intent)
                true
            } catch (_: ActivityNotFoundException) {
                fileCallback = null
                message = "No file picker on this phone."
                false
            }
        }
    }

    @androidx.compose.runtime.Composable
    private fun Overlay() {
        val s = state
        LaunchedEffect(message) { if (message != null) { delay(2800); message = null } }
        Box(Modifier.fillMaxSize()) {
            if (s !is DeskState.Ready) {
                Wallpaper {
                    Column(
                        Modifier.align(Alignment.Center).padding(28.dp),
                        horizontalAlignment = Alignment.CenterHorizontally,
                    ) {
                        Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(72.dp))
                        Spacer(Modifier.height(18.dp))
                        Text(link.serverName.ifBlank { "NOCAP DESKTOP" }.uppercase(), style = MaterialTheme.typography.headlineSmall, textAlign = TextAlign.Center)
                        Spacer(Modifier.height(12.dp))
                        when (s) {
                            is DeskState.Failed -> {
                                Text(s.why, style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted, textAlign = TextAlign.Center)
                                Spacer(Modifier.height(18.dp))
                                Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                                    GhostButton("Home", { moveTaskToBack(true) })
                                    if (s.canRetry) RedButton("Try again", { signIn() })
                                }
                            }
                            else -> {
                                CircularProgressIndicator(Modifier.size(28.dp), color = NoCap.Red, strokeWidth = 2.dp)
                                Spacer(Modifier.height(10.dp))
                                Text("Signing you in…", style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
                            }
                        }
                    }
                }
            }
            message?.let { Toast(it, Modifier.align(Alignment.BottomCenter).navigationBarsPadding().padding(bottom = 32.dp).fillMaxWidth()) }
        }
    }

    /** An app (and Settings page) to open once signed in, e.g. settings + update for Glow Up. */
    private var openApp: String? = null
    private var openSection: String? = null

    private fun takeOpen(i: Intent?) {
        val app = i?.getStringExtra(EXTRA_OPEN)?.takeIf { it.matches(Regex("[a-z0-9-]{1,32}")) } ?: return
        openApp = app
        openSection = i.getStringExtra(EXTRA_SECTION)?.takeIf { it.matches(Regex("[a-z0-9-]{1,32}")) }
    }

    private fun withOpen(url: String): String {
        val app = openApp ?: return url
        openApp = null
        val b = android.net.Uri.parse(url).buildUpon().appendQueryParameter("open", app)
        openSection?.let { b.appendQueryParameter("section", it) }
        openSection = null
        return b.build().toString()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        takeOpen(intent)
        // Already open: sign in again, straight into the page asked for.
        if (openApp != null && ::web.isInitialized) signIn()
    }

    companion object {
        const val EXTRA_OPEN = "open"
        const val EXTRA_SECTION = "section"
        private var current: WeakReference<DesktopActivity>? = null

        /** Sign out here for good (the link was forgotten). */
        fun forget() {
            runCatching {
                CookieManager.getInstance().removeAllCookies(null)
                WebStorage.getInstance().deleteAllData()
            }
            current?.get()?.finishAndRemoveTask()
            current = null
        }
    }
}
