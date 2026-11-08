package com.nocap.link

import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProviderInfo
import android.content.Intent
import android.graphics.BitmapFactory
import android.net.Uri
import android.os.Bundle
import android.provider.OpenableColumns
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.core.content.FileProvider
import androidx.lifecycle.lifecycleScope
import com.nocap.link.ai.AiActivity
import com.nocap.link.desktop.DesktopActivity
import com.nocap.link.launcher.Cell
import com.nocap.link.launcher.Home
import com.nocap.link.launcher.HomeScreen
import com.nocap.link.launcher.Layout
import com.nocap.link.launcher.NoCapItem
import com.nocap.link.launcher.NoCapKinds
import com.nocap.link.launcher.Page
import com.nocap.link.launcher.WidgetItem
import com.nocap.link.launcher.Widgets
import com.nocap.link.launcher.newId
import com.nocap.link.net.ApiError
import com.nocap.link.net.CertChanged
import com.nocap.link.net.FileEntry
import com.nocap.link.net.Me
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.ServerApp
import com.nocap.link.net.ServerTooOld
import com.nocap.link.net.Summary
import com.nocap.link.ui.IntroScreen
import com.nocap.link.ui.LinkScreen
import com.nocap.link.ui.NoCapTheme
import com.nocap.link.ui.ScanScreen
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.io.File

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

/** Full-screen layers over the home screen. */
enum class Layer { NONE, DRAWER, SEARCH, SETTINGS, HUB, WIDGETS }

class MainActivity : ComponentActivity() {
    private val vault by lazy { Vault.get(this) }

    var screen by mutableStateOf<Screen>(Screen.Home)
    var server by mutableStateOf<ServerState>(ServerState.NotLinked)
    /** Set (to the server's version) when the server is too old for the v2 features. */
    var outdated by mutableStateOf<String?>(null)
    val serverApps = mutableStateListOf<ServerApp>()
    val serverIcons = mutableStateMapOf<String, ImageBitmap>()
    var phoneApps by mutableStateOf<List<PhoneApp>>(emptyList())
    var summary by mutableStateOf<Summary?>(null)
    var recentFiles by mutableStateOf<List<FileEntry>>(emptyList())
    var layer by mutableStateOf(Layer.NONE)
    var searchStart by mutableStateOf("")
    var toast by mutableStateOf<String?>(null)
    var busy by mutableStateOf<String?>(null)
    /** Grid cell size in dp, measured by the home screen (for widget spans). */
    var cellW = 80f
    var cellH = 100f
    private var refreshJob: Job? = null
    private var liveJob: Job? = null
    private var pendingWidget: Int = -1
    private var pendingPage: Int = 0

    private val bindWidget = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { r ->
        val id = pendingWidget
        if (r.resultCode == RESULT_OK && id != -1) configureOrPlace(id) else dropPending()
    }

    private val sendPicker = registerForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isNotEmpty()) sendUris(uris)
    }

    private val customWallpaper = registerForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        if (uri != null) lifecycleScope.launch {
            toast = if (Wallpapers.setCustom(this@MainActivity, uri)) t("New aesthetic unlocked.", "Wallpaper set.") else t("Couldn't read that pic.", "Couldn't open that picture.")
        }
    }

    private val exportLayout = registerForActivityResult(ActivityResultContracts.CreateDocument("application/json")) { uri ->
        if (uri != null) runCatching {
            contentResolver.openOutputStream(uri)?.use { it.write(backupJson().toString(2).toByteArray()) }
            toast = t("Layout saved. Safe and sound.", "Layout exported.")
        }.onFailure { toast = t("Couldn't save that file.", "Export failed.") }
    }

    private val importLayout = registerForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) runCatching {
            val text = contentResolver.openInputStream(uri)?.use { it.readBytes().toString(Charsets.UTF_8) } ?: error("empty")
            restore(JSONObject(text))
        }.onFailure { toast = t("That file isn't a NoCap OS layout.", "That isn't a NoCap OS layout file.") }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        Prefs.init(this)
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
                        ScanScreen(onResult = { uri -> if (!handle(Intent(Intent.ACTION_VIEW, uri))) toast = t("That QR isn't a NoCap Link code.", "That QR code isn't a NoCap Link code.") }, onCancel = { screen = Screen.LinkForm() })
                    }
                }
            }
        }
    }

    override fun onStart() {
        super.onStart()
        runCatching { Widgets.host(this).startListening() }
    }

    override fun onStop() {
        super.onStop()
        runCatching { Widgets.host(this).stopListening() }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        if (!handle(intent) && intent.hasCategory(Intent.CATEGORY_HOME)) {
            // Home pressed while already home: close whatever's open.
            if (screen != Screen.Intro) screen = Screen.Home
            layer = Layer.NONE
        }
    }

    override fun onResume() {
        super.onResume()
        lifecycleScope.launch {
            val apps = withContext(Dispatchers.IO) { PhoneApps.load(this@MainActivity) }
            phoneApps = apps
            Home.load(this@MainActivity) { firstRun(apps) }
            Home.prune(apps.map { it.key }.toSet())
        }
        refresh()
        liveJob?.cancel()
        liveJob = lifecycleScope.launch {
            while (isActive) {
                delay(30_000)
                refreshLive()
            }
        }
    }

    override fun onPause() {
        super.onPause()
        liveJob?.cancel()
    }

    private fun firstRun(apps: List<PhoneApp>): Layout {
        val dock = vault.dock ?: PhoneApps.defaultDock(this, apps)
        val first = Page(
            listOf(
                NoCapItem(newId(), Cell(0, 0, 4, 2), NoCapKinds.CLOCK),
                NoCapItem(newId(), Cell(0, 2, 4, 2), NoCapKinds.SERVER),
                NoCapItem(newId(), Cell(0, 4, 4, 1), NoCapKinds.AI),
            ),
        )
        return Layout(listOf(first, Page()), dock)
    }

    /** nocap://pair?server=…&code=…&id=NC-… → the link form, filled in. Also the AI and hub shortcuts. */
    private fun handle(intent: Intent?): Boolean {
        when (intent?.action) {
            ACTION_HUB -> { screen = Screen.Home; layer = Layer.HUB; return true }
            ACTION_SEARCH -> { screen = Screen.Home; layer = Layer.SEARCH; return true }
        }
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
        toast = t("Bet — you're on NoCap Link.", "Linked with NoCap Link.")
        refresh()
    }

    val link: Link? get() = vault.link
    fun api(): NoCapApi? = vault.link?.let { NoCapApi(it) }

    fun refresh() {
        val link = vault.link
        if (link == null) {
            server = ServerState.NotLinked
            serverApps.clear()
            summary = null
            recentFiles = emptyList()
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
                refreshLive()
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
                server = ServerState.Offline(say(e))
            }
        }
    }

    /** Widgets' data: summary + recent files (every 30 s while you're home). */
    private suspend fun refreshLive() {
        val s = server as? ServerState.Online ?: return
        val api = api() ?: return
        try {
            summary = withContext(Dispatchers.IO) { api.summary() }
            outdated = null
            recentFiles = withContext(Dispatchers.IO) { runCatching { api.recent(6) }.getOrDefault(recentFiles) }
        } catch (e: ServerTooOld) {
            outdated = s.me.server.version.ifBlank { "?" }
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (_: Exception) {
            // keep the last numbers; the status chip already says if it's offline
        }
    }

    /** The person checked the new certificate and trusts it. */
    fun trustNewCert(pin: String) {
        vault.repin(pin)
        server = ServerState.Checking
        refresh()
    }

    /** Forget the link on this phone (and, when it can, on the server too). */
    fun forget(alsoServer: Boolean = false) {
        val api = api()
        if (alsoServer && api != null) {
            lifecycleScope.launch(Dispatchers.IO) { runCatching { api.unlink() } }
        }
        vault.link = null
        HeartbeatWorker.cancel(this)
        DesktopActivity.forget()
        serverIcons.clear()
        outdated = null
        refresh()
    }

    fun openDesktop() {
        if (vault.link == null) { screen = Screen.LinkForm(); return }
        startActivity(Intent(this, DesktopActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }

    fun openAi(prompt: String? = null) {
        val i = Intent(this, AiActivity::class.java)
        if (!prompt.isNullOrBlank()) i.putExtra(AiActivity.EXTRA_PROMPT, prompt)
        startActivity(i)
    }

    fun openServerApp(app: ServerApp) {
        if (app.id == "nocapos") return openDesktop()
        try {
            startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(app.url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        } catch (_: Exception) {
            toast = t("No browser on this phone to open ${app.name}.", "No browser available to open ${app.name}.")
        }
    }

    fun launch(app: PhoneApp) = PhoneApps.launch(this, app)

    // ---------- send, files ----------

    fun pickToSend() {
        if (vault.link == null) { toast = t("Link your NoCap server first.", "Link a NoCap server first."); return }
        sendPicker.launch(arrayOf("*/*"))
    }

    private fun sendUris(uris: List<Uri>) {
        val api = api() ?: return
        busy = t("Sending to NoCap…", "Sending…")
        lifecycleScope.launch {
            try {
                val ups = uris.map { u -> upload(u) }
                val paths = withContext(Dispatchers.IO) { api.send(ups) }
                toast = t("Sent ${paths.size} to NoCap Link in your home. W.", "Sent ${paths.size} file(s) to NoCap Link in your home folder.")
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                toast = say(e)
            } finally {
                busy = null
            }
        }
    }

    fun upload(u: Uri): NoCapApi.Upload {
        var name = "file"
        var size = -1L
        contentResolver.query(u, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
            if (c.moveToFirst()) {
                c.getString(0)?.let { name = it }
                if (!c.isNull(1)) size = c.getLong(1)
            }
        }
        return NoCapApi.Upload(name, contentResolver.getType(u), size) { contentResolver.openInputStream(u) ?: error("can't read $name") }
    }

    /** Downloads a server file and opens it in whatever app handles it. */
    fun openFile(f: FileEntry) {
        if (f.dir) return
        val api = api() ?: return
        busy = t("Grabbing ${f.name}…", "Downloading ${f.name}…")
        lifecycleScope.launch {
            try {
                val dest = File(File(cacheDir, "open"), f.name.replace('/', '_'))
                withContext(Dispatchers.IO) {
                    dest.parentFile?.listFiles()?.filter { System.currentTimeMillis() - it.lastModified() > 86_400_000 }?.forEach { it.delete() }
                    api.download(f, dest)
                }
                val uri = FileProvider.getUriForFile(this@MainActivity, "$packageName.files", dest)
                val type = contentResolver.getType(uri) ?: "*/*"
                startActivity(Intent.createChooser(Intent(Intent.ACTION_VIEW).setDataAndType(uri, type).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION), f.name))
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                toast = say(e)
            } finally {
                busy = null
            }
        }
    }

    // ---------- wallpaper ----------

    fun pickWallpaper() = customWallpaper.launch(androidx.activity.result.PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))

    fun setPhoneWallpaper(home: Boolean, lock: Boolean) {
        lifecycleScope.launch {
            toast = if (Wallpapers.setSystem(this@MainActivity, home, lock)) t("Phone wallpaper set. Looking fresh.", "Phone wallpaper set.") else t("Your phone said no to that wallpaper.", "Couldn't set the phone wallpaper.")
        }
    }

    /** Takes the wallpaper (and wording) you picked on your NoCap desktop. */
    fun syncFromServer() {
        val api = api() ?: run { toast = t("Link your NoCap server first.", "Link a NoCap server first."); return }
        lifecycleScope.launch {
            try {
                val p = withContext(Dispatchers.IO) { api.prefs() }
                val w = p.wallpaper.ifBlank { "tide" }
                toast = if (Wallpapers.builtIn.any { it.id == w }) {
                    Prefs.setWallpaper(w)
                    t("Synced the $w aesthetic from your server.", "Wallpaper synced from your server ($w).")
                } else {
                    t("Your desktop uses your own pic, and that lives in your browser. Pick one here instead.", "Your desktop uses a custom picture stored in the browser; choose one here instead.")
                }
                if (p.lingo == "genz" || p.lingo == "standard") Prefs.setGenz(p.lingo == "genz")
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                toast = say(e)
            }
        }
    }

    // ---------- backup ----------

    fun backupJson(): JSONObject = JSONObject().put("app", "nocap-os").put("layout", Home.toJson(portable = true)).put("settings", JSONObject(Prefs.export()))

    fun restore(o: JSONObject) {
        val l = o.optJSONObject("layout") ?: error("no layout")
        o.optJSONObject("settings")?.let { Prefs.import(it) }
        // Android widgets belong to this phone; keep the ones already here.
        val widgets = Home.layout.pages.flatMap { it.items.filterIsInstance<WidgetItem>() }
        Home.set(Home.fromJson(l))
        for (w in widgets) Home.add({ c -> w.copy(cell = c) }, w.cell.w, w.cell.h)
        Home.prune(phoneApps.map { it.key }.toSet())
        Home.refit()
        toast = t("Layout's back. Like it never left.", "Layout restored.")
    }

    fun exportToFile() = exportLayout.launch("nocap-os-layout.json")
    fun importFromFile() = importLayout.launch(arrayOf("application/json", "text/plain", "*/*"))

    fun backupToServer() {
        val api = api() ?: return
        lifecycleScope.launch {
            toast = try {
                withContext(Dispatchers.IO) { api.saveLayout(backupJson()) }
                t("Layout backed up to your NoCap server.", "Layout backed up to your NoCap server.")
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                say(e)
            }
        }
    }

    fun restoreFromServer() {
        val api = api() ?: return
        lifecycleScope.launch {
            try {
                val o = withContext(Dispatchers.IO) { api.layout() }
                if (o == null) toast = t("No backup on your server yet.", "There's no backup on your server yet.") else restore(o)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                toast = say(e)
            }
        }
    }

    // ---------- Android widgets ----------

    fun addWidget(info: AppWidgetProviderInfo, page: Int) {
        val host = Widgets.host(this)
        val id = host.allocateAppWidgetId()
        pendingWidget = id
        pendingPage = page
        if (Widgets.manager.bindAppWidgetIdIfAllowed(id, info.provider)) {
            configureOrPlace(id)
        } else {
            bindWidget.launch(
                Intent(AppWidgetManager.ACTION_APPWIDGET_BIND)
                    .putExtra(AppWidgetManager.EXTRA_APPWIDGET_ID, id)
                    .putExtra(AppWidgetManager.EXTRA_APPWIDGET_PROVIDER, info.provider),
            )
        }
    }

    private fun configureOrPlace(id: Int) {
        val info = Widgets.info(this, id) ?: return dropPending()
        if (info.configure != null) {
            try {
                Widgets.host(this).startAppWidgetConfigureActivityForResult(this, id, 0, REQ_CONFIGURE, null)
                return
            } catch (_: Exception) {
                // Some widgets don't let launchers configure them: place it as-is.
            }
        }
        place(id)
    }

    @Deprecated("Widget configuration still reports here")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == REQ_CONFIGURE) {
            if (resultCode == RESULT_OK && pendingWidget != -1) place(pendingWidget) else dropPending()
        }
    }

    private fun place(id: Int) {
        val info = Widgets.info(this, id) ?: return dropPending()
        val (w, h) = Widgets.span(this, info, cellW, cellH)
        val p = Home.add({ c -> WidgetItem(newId(), c, id) }, w, h, pendingPage)
        pendingWidget = -1
        layer = Layer.NONE
        toast = if (p == pendingPage) t("Widget added.", "Widget added.") else t("No room there, so it went to page ${p + 1}.", "Added to page ${p + 1}.")
    }

    private fun dropPending() {
        if (pendingWidget != -1) Widgets.delete(this, pendingWidget)
        pendingWidget = -1
    }

    fun addNoCapWidget(kind: String, page: Int) {
        val (w, h) = NoCapKinds.size(kind)
        Home.add({ c -> NoCapItem(newId(), c, kind) }, w.coerceAtMost(Prefs.columns), h, page)
        layer = Layer.NONE
    }

    /** A friendly line for anything that went wrong talking to the server. */
    fun say(e: Throwable): String = when (e) {
        is ServerTooOld -> needsUpdate((server as? ServerState.Online)?.me?.server?.version)
        else -> friendly(e)
    }

    companion object {
        const val ACTION_HUB = "com.nocap.link.HUB"
        const val ACTION_SEARCH = "com.nocap.link.SEARCH"
        private const val REQ_CONFIGURE = 4242

        fun needsUpdate(version: String?): String {
            val v = version?.takeIf { it.isNotBlank() && it != "?" }
            return t(
                "Your NoCap server needs a Glow Up for this (Settings → Glow Up)." + (v?.let { " It's on v$it." } ?: ""),
                "Your NoCap server needs an update for this (Settings → Software Update)." + (v?.let { " It's running v$it." } ?: ""),
            )
        }

        fun friendly(e: Throwable): String {
            val m = e.message.orEmpty()
            return when {
                e is ServerTooOld -> needsUpdate(null)
                e is CertChanged -> t("Your server's certificate changed. Check it on the home screen.", "The server's certificate changed. Review it on the home screen.")
                e is java.net.UnknownHostException -> t("Can't find that server. Same Wi-Fi?", "Can't find the server. Are you on the same network?")
                e is java.net.ConnectException || m.contains("ECONNREFUSED") -> t("Server isn't answering. Is NoCapOS on?", "The server isn't responding. Is NoCapOS running?")
                e is java.net.SocketTimeoutException -> t("Server took too long to answer.", "The server took too long to respond.")
                e is javax.net.ssl.SSLException -> t("Secure connection failed.", "Secure connection failed.")
                m.isNotBlank() -> m
                else -> t("Can't reach your server right now.", "Can't reach your server right now.")
            }
        }
    }
}
