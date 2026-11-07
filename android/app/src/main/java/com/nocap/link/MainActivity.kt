package com.nocap.link

import android.content.Intent
import android.graphics.BitmapFactory
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.lifecycle.lifecycleScope
import com.nocap.link.desktop.DesktopActivity
import com.nocap.link.net.ApiError
import com.nocap.link.net.CertChanged
import com.nocap.link.net.Me
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.ServerApp
import com.nocap.link.ui.HomeScreen
import com.nocap.link.ui.IntroScreen
import com.nocap.link.ui.LinkScreen
import com.nocap.link.ui.NoCapTheme
import com.nocap.link.ui.ScanScreen
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Where the person is in the app. */
sealed interface Screen {
    data object Home : Screen
    data object Intro : Screen
    data class LinkForm(val server: String = "", val code: String = "", val id: String = "", val auto: Boolean = false) : Screen
    data object Scan : Screen
}

/** How the linked server is doing. */
sealed interface ServerState {
    data object NotLinked : ServerState
    data object Checking : ServerState
    data class Online(val me: Me) : ServerState
    data class Offline(val why: String) : ServerState
    data class CertChanged(val seen: String) : ServerState
    data object Unlinked : ServerState
}

class MainActivity : ComponentActivity() {
    private val vault by lazy { Vault.get(this) }

    var screen by mutableStateOf<Screen>(Screen.Home)
    var server by mutableStateOf<ServerState>(ServerState.NotLinked)
    val serverApps = mutableStateListOf<ServerApp>()
    val serverIcons = mutableStateMapOf<String, ImageBitmap>()
    var phoneApps by mutableStateOf<List<PhoneApp>>(emptyList())
    var dock by mutableStateOf<List<String>>(emptyList())
    var opening by mutableStateOf(false)
    var toast by mutableStateOf<String?>(null)
    private var refreshJob: Job? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        if (!vault.introDone) screen = Screen.Intro
        handle(intent)
        setContent {
            NoCapTheme {
                when (val s = screen) {
                    Screen.Home -> HomeScreen(this)
                    Screen.Intro -> IntroScreen(onDone = {
                        vault.introDone = true
                        screen = Screen.Home
                    })
                    is Screen.LinkForm -> {
                        BackHandler { screen = Screen.Home }
                        LinkScreen(s, onScan = { screen = Screen.Scan }, onCancel = { screen = Screen.Home }, onLinked = { linked() })
                    }
                    Screen.Scan -> {
                        BackHandler { screen = Screen.LinkForm() }
                        ScanScreen(onResult = { uri -> if (!handle(Intent(Intent.ACTION_VIEW, uri))) toast = "That QR isn't a NoCap Link code." }, onCancel = { screen = Screen.LinkForm() })
                    }
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        if (!handle(intent) && intent.hasCategory(Intent.CATEGORY_HOME)) {
            // Home pressed while already home: back to the top.
            if (screen != Screen.Intro) screen = Screen.Home
        }
    }

    override fun onResume() {
        super.onResume()
        lifecycleScope.launch {
            val apps = withContext(Dispatchers.IO) { PhoneApps.load(this@MainActivity) }
            phoneApps = apps
            dock = vault.dock ?: PhoneApps.defaultDock(this@MainActivity, apps).also { vault.dock = it }
        }
        refresh()
    }

    /** nocap://pair?server=…&code=…&id=NC-… → the link form, filled in. */
    private fun handle(intent: Intent?): Boolean {
        val uri: Uri = intent?.data ?: return false
        if (uri.scheme != "nocap" || uri.host != "pair") return false
        val server = uri.getQueryParameter("server").orEmpty()
        val code = uri.getQueryParameter("code").orEmpty().filter(Char::isDigit).take(6)
        val id = uri.getQueryParameter("id").orEmpty()
        if (server.isBlank()) return false
        screen = Screen.LinkForm(server, code, id, auto = code.length == 6)
        return true
    }

    private fun linked() {
        HeartbeatWorker.schedule(this)
        HeartbeatWorker.now(this)
        screen = Screen.Home
        toast = "Bet — you're on NoCap Link."
        refresh()
    }

    fun refresh() {
        val link = vault.link
        if (link == null) {
            server = ServerState.NotLinked
            serverApps.clear()
            return
        }
        if (vault.unlinked) {
            server = ServerState.Unlinked
        } else if (server !is ServerState.Online) {
            server = ServerState.Checking
        }
        refreshJob?.cancel()
        refreshJob = lifecycleScope.launch {
            val api = NoCapApi(link)
            try {
                val me = withContext(Dispatchers.IO) { api.me() }
                val apps = withContext(Dispatchers.IO) { api.apps() }
                if (vault.unlinked) vault.unlinked = false
                server = ServerState.Online(me)
                serverApps.clear()
                serverApps.addAll(apps)
                for (a in apps) {
                    if (a.iconUrl.isBlank() || serverIcons.containsKey(a.id)) continue
                    val bmp = withContext(Dispatchers.IO) {
                        api.bytes(a.iconUrl)?.let { b -> BitmapFactory.decodeByteArray(b, 0, b.size)?.asImageBitmap() }
                    }
                    if (bmp != null) serverIcons[a.id] = bmp
                }
            } catch (e: CertChanged) {
                server = ServerState.CertChanged(e.seen)
            } catch (e: ApiError) {
                if (e.status == 401) {
                    vault.unlinked = true
                    server = ServerState.Unlinked
                } else {
                    server = ServerState.Offline(e.message ?: "Server error")
                }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                server = ServerState.Offline(friendly(e))
            }
        }
    }

    /** The person checked the new certificate and trusts it. */
    fun trustNewCert(pin: String) {
        vault.repin(pin)
        server = ServerState.Checking
        refresh()
    }

    /** Forget the link on this phone (the server side is unlinked from Settings → Devices). */
    fun forget() {
        vault.link = null
        HeartbeatWorker.cancel(this)
        DesktopActivity.forget()
        serverIcons.clear()
        refresh()
    }

    fun openDesktop() {
        if (vault.link == null || opening) return
        startActivity(Intent(this, DesktopActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }

    fun openServerApp(app: ServerApp) {
        if (app.id == "nocapos") return openDesktop()
        try {
            startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(app.url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        } catch (_: Exception) {
            toast = "No browser on this phone to open ${app.name}."
        }
    }

    fun toggleDock(app: PhoneApp) {
        val now = dock.toMutableList()
        if (!now.remove(app.key)) {
            if (now.size >= 5) { toast = "Dock's full — unpin something first."; return }
            now.add(app.key)
        }
        dock = now
        vault.dock = now
    }

    companion object {
        fun friendly(e: Exception): String {
            val m = e.message.orEmpty()
            return when {
                e is java.net.UnknownHostException -> "Can't find that server. Same Wi-Fi?"
                e is java.net.ConnectException || m.contains("ECONNREFUSED") -> "Server isn't answering. Is NoCapOS on?"
                e is java.net.SocketTimeoutException -> "Server took too long to answer."
                e is javax.net.ssl.SSLException -> "Secure connection failed."
                m.isNotBlank() -> m
                else -> "Can't reach your server right now."
            }
        }
    }
}
