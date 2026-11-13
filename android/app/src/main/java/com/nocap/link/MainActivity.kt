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
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.unit.dp
import androidx.core.content.FileProvider
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.lifecycle.lifecycleScope
import com.nocap.link.ai.AiActivity
import com.nocap.link.data.BarStyle
import com.nocap.link.data.CustomIcons
import com.nocap.link.data.DrawerStyle
import com.nocap.link.data.Link
import com.nocap.link.data.Opt
import com.nocap.link.data.PhoneApp
import com.nocap.link.data.PhoneApps
import com.nocap.link.data.Prefs
import com.nocap.link.data.Rotation
import com.nocap.link.data.t
import com.nocap.link.data.Vault
import com.nocap.link.desktop.DesktopActivity
import com.nocap.link.home.Cell
import com.nocap.link.home.Home
import com.nocap.link.home.HomeScreen
import com.nocap.link.home.Layout
import com.nocap.link.home.newId
import com.nocap.link.home.NoCapItem
import com.nocap.link.home.NoCapKinds
import com.nocap.link.home.Page
import com.nocap.link.home.Wallpapers
import com.nocap.link.home.WidgetItem
import com.nocap.link.net.ApiError
import com.nocap.link.net.CertChanged
import com.nocap.link.net.FileEntry
import com.nocap.link.net.Me
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.ServerApp
import com.nocap.link.net.ServerTooOld
import com.nocap.link.net.Summary
import com.nocap.link.settings.SetupScreen
import com.nocap.link.settings.SetupStep
import com.nocap.link.sync.LinkScreen
import com.nocap.link.sync.ScanScreen
import com.nocap.link.sync.Sync
import com.nocap.link.system.CrashLog
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.NoCapTheme
import com.nocap.link.widget.Widgets
import java.io.File
import kotlinx.coroutines.delay
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.isActive
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject

/** Where the person is in the app. */
sealed interface Screen {
    data object Home : Screen
    /** First run: link the server, then permissions, step by step. */
    data class Setup(val step: SetupStep = SetupStep.WELCOME) : Screen
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
enum class Layer { NONE, DRAWER, SEARCH, SETTINGS, HUB, WIDGETS, PERMISSIONS, CONTROL }

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
    /** Where the account is signed in (null = unknown, or the server is too old). */
    var signedIn by mutableStateOf<List<com.nocap.link.net.SignedIn>?>(null)
    var layer by mutableStateOf(Layer.NONE)
    var searchStart by mutableStateOf("")
    var toast by mutableStateOf<String?>(null)
    var busy by mutableStateOf<String?>(null)
    /** A crash report from last time that hasn't been sent or dismissed (asked about once). */
    var crashReport by mutableStateOf<String?>(null)
    /** Grid cell size in dp, measured by the home screen (for widget spans). */
    var cellW = 80f
    var cellH = 100f
    /** The NoCap widget whose edit sheet is open. */
    var editing by mutableStateOf<NoCapItem?>(null)
    /** The weather widget whose forecast sheet is open. */
    var weatherFor by mutableStateOf<NoCapItem?>(null)
    /** The app whose name/icon is being customized. */
    var customizing by mutableStateOf<PhoneApp?>(null)
    /** Opens Settings already searching for this. */
    var settingsQuery by mutableStateOf("")
    /** Bumped when Home is pressed on the home screen / the Stash page is asked for / we come back. */
    var homeTick by mutableIntStateOf(0)
    var libraryTick by mutableIntStateOf(0)
    var resumeTick by mutableIntStateOf(0)
    /** Server CPU % and network (bytes/s) history for sparklines, newest last. */
    val cpuHist = mutableStateListOf<Float>()
    val netHist = mutableStateListOf<Float>()
    private var permCallback: ((Boolean) -> Unit)? = null
    private var photoFor: String? = null
    private var iconFor: String? = null

    private val askPerm = registerForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        resumeTick++
        permCallback?.invoke(ok)
        permCallback = null
    }

    private val photoPicker = registerForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(20)) { uris ->
        val id = photoFor ?: return@registerForActivityResult
        photoFor = null
        if (uris.isEmpty()) return@registerForActivityResult
        busy = t("Framing your pics…", "Adding photos…")
        lifecycleScope.launch {
            val names = withContext(Dispatchers.IO) { uris.mapNotNull { com.nocap.link.widget.Photos.save(this@MainActivity, it) } }
            busy = null
            val cur = Home.find(id)?.second as? NoCapItem ?: return@launch
            val all = (cur.opt("photos").split('|').filter { it.isNotBlank() } + names).takeLast(20)
            Home.replace(cur.with("photos" to all.joinToString("|")))
            prunePhotos()
        }
    }

    private val iconPicker = registerForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        val key = iconFor ?: return@registerForActivityResult
        iconFor = null
        if (uri == null) return@registerForActivityResult
        lifecycleScope.launch {
            val name = withContext(Dispatchers.IO) { CustomIcons.save(this@MainActivity, key, uri) }
            if (name == null) { toast = t("Couldn't read that pic.", "Couldn't open that picture."); return@launch }
            Opt.customIcons.put(key, name)
            withContext(Dispatchers.IO) { CustomIcons.prune(this@MainActivity) }
            customizing = null
            reloadApps()
        }
    }

    private val voice = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { r ->
        val text = r.data?.getStringArrayListExtra(android.speech.RecognizerIntent.EXTRA_RESULTS)?.firstOrNull()
        if (r.resultCode == RESULT_OK && !text.isNullOrBlank()) { searchStart = text; layer = Layer.SEARCH }
    }
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
        if (!vault.introDone) {
            // Back where setup was, after Android stopped the app in the background.
            val step = savedInstanceState?.getString(STATE_SETUP)?.let { s -> SetupStep.entries.firstOrNull { it.name == s } }
            screen = Screen.Setup(step ?: com.nocap.link.settings.Guide.savedStep(this) ?: SetupStep.WELCOME)
        }
        crashReport = CrashLog.pending(this)
        // An ANR or native crash last time (Android 11+): offered like a crash, never sent by itself.
        if (crashReport == null) lifecycleScope.launch(Dispatchers.IO) {
            runCatching { CrashLog.checkExits(this@MainActivity) }
            val r = CrashLog.pending(this@MainActivity)
            withContext(Dispatchers.Main) { if (crashReport == null) crashReport = r }
        }
        handle(intent)
        setContent {
            // Rotation, and the status bar on the home screen, from Settings.
            val rot = Opt.rotation.v
            LaunchedEffect(rot) {
                requestedOrientation = when (rot) {
                    Rotation.AUTO -> android.content.pm.ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED
                    Rotation.PORTRAIT -> android.content.pm.ActivityInfo.SCREEN_ORIENTATION_PORTRAIT
                    Rotation.LANDSCAPE -> android.content.pm.ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE
                }
            }
            val hideBar = Opt.statusBar.v == BarStyle.HIDDEN && screen == Screen.Home && layer == Layer.NONE
            val lightBars = NoCap.light && (layer != Layer.NONE || screen != Screen.Home)
            LaunchedEffect(hideBar, lightBars) {
                val c = WindowCompat.getInsetsController(window, window.decorView)
                c.systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
                if (hideBar) c.hide(WindowInsetsCompat.Type.statusBars()) else c.show(WindowInsetsCompat.Type.statusBars())
                c.isAppearanceLightStatusBars = lightBars
                c.isAppearanceLightNavigationBars = lightBars
            }
            // "All apps on home pages": new apps land on the last page.
            val apps = phoneApps
            LaunchedEffect(Opt.drawer.v, apps, Opt.hidden.v) {
                if (Opt.drawer.v == DrawerStyle.HOME_PAGES && Home.loaded && apps.isNotEmpty()) Home.placeAll(apps.filter { it.key !in Opt.hidden.v }.map { it.key })
            }
            // Icon layers depend on the icon style: reload when it changes.
            LaunchedEffect(Opt.iconTheme.v, Opt.iconShape.v, Opt.nocapGlyph.v) { if (phoneApps.isNotEmpty()) reloadApps() }
            NoCapTheme {
                when (val s = screen) {
                    Screen.Home -> HomeScreen(this)
                    is Screen.Setup -> SetupScreen(this, s.step)
                    is Screen.LinkForm -> {
                        BackHandler { screen = afterLinkForm() }
                        LinkScreen(s, onScan = { screen = Screen.Scan }, onCancel = { screen = afterLinkForm() }, onLinked = { linked() })
                    }
                    Screen.Scan -> {
                        BackHandler { screen = Screen.LinkForm() }
                        ScanScreen(onResult = { uri -> if (!handle(Intent(Intent.ACTION_VIEW, uri))) toast = t("That QR isn't a NoCap Link code.", "That QR code isn't a NoCap Link code.") }, onCancel = { screen = Screen.LinkForm() })
                    }
                }
                // The home screen shows its own toasts; the other screens get them here.
                if (screen != Screen.Home) {
                    toast?.let { msg ->
                        LaunchedEffect(msg) { delay(2800); if (toast == msg) toast = null }
                        androidx.compose.foundation.layout.Box(androidx.compose.ui.Modifier.fillMaxSize(), contentAlignment = androidx.compose.ui.Alignment.BottomCenter) {
                            com.nocap.link.ui.Toast(msg, androidx.compose.ui.Modifier.navigationBarsPadding().padding(bottom = 32.dp))
                        }
                    }
                }
                crashReport?.let { report -> com.nocap.link.settings.CrashPrompt(this, report) { crashReport = null } }
                com.nocap.link.settings.ExtrasLayer(this)
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
            // Home pressed while already home: close whatever's open, or run the Home gesture.
            val clean = screen == Screen.Home && layer == Layer.NONE && editing == null && weatherFor == null && customizing == null
            if (screen !is Screen.Setup) screen = Screen.Home
            layer = Layer.NONE
            editing = null; weatherFor = null; customizing = null
            if (clean) homeTick++
        }
    }

    override fun onResume() {
        super.onResume()
        resumeTick++
        reloadApps()
        refresh()
        com.nocap.link.widget.Media.start(this)
        Sync.start(this)
        liveJob?.cancel()
        liveJob = lifecycleScope.launch {
            while (isActive) {
                val fast = Home.layout.pages.any { p -> p.items.any { it is NoCapItem && (it.kind == NoCapKinds.STATUS || it.kind == NoCapKinds.SERVER) } }
                delay(if (fast) 10_000 else 30_000)
                refreshLive()
            }
        }
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        // Android only lets the app in front read the clipboard: this is that moment.
        if (hasFocus && Opt.clipSync.v && vault.link != null) Sync.sendClipboard(this, force = false)
    }

    /** Reloads the app list (after installs, custom icons/names, or a new icon style). */
    fun reloadApps() {
        lifecycleScope.launch {
            val apps = withContext(Dispatchers.IO) { PhoneApps.load(this@MainActivity) }
            phoneApps = apps
            Home.load(this@MainActivity) { firstRun(apps) }
            Home.prune(apps.map { it.key }.toSet())
            prunePhotos()
        }
    }

    private fun prunePhotos() {
        if (!Home.loaded) return
        val keep = Home.layout.pages.flatMap { p -> p.items.filterIsInstance<NoCapItem>().flatMap { it.opt("photos").split('|') } }.filter { it.isNotBlank() }.toSet()
        lifecycleScope.launch(Dispatchers.IO) { com.nocap.link.widget.Photos.prune(this@MainActivity, keep) }
    }

    fun askPermission(perm: String, done: (Boolean) -> Unit) {
        if (androidx.core.content.ContextCompat.checkSelfPermission(this, perm) == android.content.pm.PackageManager.PERMISSION_GRANTED) { done(true); return }
        permCallback = done
        runCatching { askPerm.launch(perm) }.onFailure { permCallback = null; done(false) }
    }

    fun pickPhotos(item: NoCapItem) {
        photoFor = item.id
        runCatching { photoPicker.launch(androidx.activity.result.PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) }
            .onFailure { photoFor = null; toast = t("No photo picker on this phone.", "No photo picker is available.") }
    }

    fun pickIcon(app: PhoneApp) {
        iconFor = app.key
        runCatching { iconPicker.launch(androidx.activity.result.PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) }
            .onFailure { iconFor = null; toast = t("No photo picker on this phone.", "No photo picker is available.") }
    }

    fun voiceSearch() {
        val i = Intent(android.speech.RecognizerIntent.ACTION_RECOGNIZE_SPEECH)
            .putExtra(android.speech.RecognizerIntent.EXTRA_LANGUAGE_MODEL, android.speech.RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
            .putExtra(android.speech.RecognizerIntent.EXTRA_PROMPT, t("Spill it…", "Speak now"))
        runCatching { voice.launch(i) }.onFailure { toast = t("No voice input on this phone.", "Voice input isn't available.") }
    }

    /** Starts a timer in the phone's clock app (rings even when asleep). */
    fun clockTimer(seconds: Int, title: String) {
        val i = Intent(android.provider.AlarmClock.ACTION_SET_TIMER)
            .putExtra(android.provider.AlarmClock.EXTRA_LENGTH, seconds.coerceAtLeast(1))
            .putExtra(android.provider.AlarmClock.EXTRA_MESSAGE, title)
            .putExtra(android.provider.AlarmClock.EXTRA_SKIP_UI, true)
        runCatching { startActivity(i) }.onFailure { toast = t("No clock app took the timer.", "No clock app could start the timer.") }
    }

    fun openSystem(action: String) {
        runCatching { startActivity(Intent(action).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
            .onFailure { runCatching { startActivity(Intent(android.provider.Settings.ACTION_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) } }
    }

    /** The drawer, or (with all apps on home pages) the Stash page (app groups). */
    fun openDrawer() {
        if (Opt.drawer.v == DrawerStyle.HOME_PAGES) libraryTick++ else layer = Layer.DRAWER
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
                NoCapItem(newId(), Cell(0, 2, 4, 2), NoCapKinds.STATUS),
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
            ACTION_SEND_FILE -> { screen = Screen.Home; layer = Layer.NONE; window.decorView.post { pickToSend() }; return true }
            ACTION_SETTINGS -> { screen = Screen.Home; layer = Layer.SETTINGS; return true }
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

    /** Where "back" from the link form goes: setup (first run) or home. */
    private fun afterLinkForm(): Screen = if (vault.introDone) Screen.Home else Screen.Setup(SetupStep.LINK)

    /** Setup finished or skipped: straight to the home screen. */
    fun finishSetup() {
        vault.introDone = true
        screen = Screen.Home
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        (screen as? Screen.Setup)?.let { outState.putString(STATE_SETUP, it.step.name) }
    }

    /** Sends the last crash report to your own server (saved in NoCap Link in your home folder). */
    fun sendCrashReport(report: String) {
        val api = api() ?: run { toast = t("Link your NoCap server first.", "Link a NoCap server first."); return }
        lifecycleScope.launch {
            toast = try {
                withContext(Dispatchers.IO) { api.sendText(report, "NoCap OS crash report") }
                CrashLog.settle(this@MainActivity)
                t("Crash report sent to your server's NoCap Link folder.", "Crash report saved to NoCap Link on your server.")
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                say(e)
            }
        }
    }

    private fun linked() {
        HeartbeatWorker.schedule(this)
        HeartbeatWorker.now(this)
        screen = if (vault.introDone) Screen.Home else Screen.Setup(SetupStep.GUIDE)
        toast = t("Bet — you're on NoCap Link.", "Linked with NoCap Link.")
        refresh()
    }

    val link: Link? get() = vault.link
    /** The server API, or null when not linked (or the stored link is unusable). */
    fun api(): NoCapApi? = runCatching { vault.link?.let { NoCapApi(it) } }.getOrNull()

    fun refresh() {
        val link = vault.link
        if (link == null) {
            server = ServerState.NotLinked
            serverApps.clear()
            summary = null
            recentFiles = emptyList()
            signedIn = null
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
                launch { com.nocap.link.data.Lingo.refresh(api) }
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
            val sum = withContext(Dispatchers.IO) { api.summary() }
            summary = sum
            outdated = null
            if (sum.ready) {
                cpuHist.add(sum.cpuPercent.toFloat()); while (cpuHist.size > 40) cpuHist.removeAt(0)
                sum.net?.let { (rx, tx) -> netHist.add((rx + tx).toFloat()); while (netHist.size > 40) netHist.removeAt(0) }
            }
            com.nocap.link.widget.AppWidgets.saveServer(this, sum, s.me.server.name.ifBlank { link?.serverName ?: "" })
            recentFiles = withContext(Dispatchers.IO) { runCatching { api.recent(6) }.getOrDefault(recentFiles) }
            withContext(Dispatchers.IO) { runCatching { api.sessions() } }.onSuccess { (list, alerts) ->
                signedIn = list
                com.nocap.link.sync.Signins.deliver(this, alerts, live = false)
            }.onFailure { if (it is ServerTooOld) signedIn = null }
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
        Sync.start(this) // not linked anymore: stops the link service
        DesktopActivity.forget()
        serverIcons.clear()
        outdated = null
        refresh()
    }

    fun openDesktop(app: String? = null, section: String? = null) {
        if (vault.link == null) { screen = Screen.LinkForm(); return }
        val i = Intent(this, DesktopActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        if (app != null) i.putExtra(DesktopActivity.EXTRA_OPEN, app)
        if (section != null) i.putExtra(DesktopActivity.EXTRA_SECTION, section)
        runCatching { startActivity(i) }.onFailure { toast = friendly(it) }
    }

    /** Opens NoCap AI once, however fast it's tapped. */
    private var aiOpenedAt = 0L

    fun openAi(prompt: String? = null) {
        val now = android.os.SystemClock.elapsedRealtime()
        if (prompt.isNullOrBlank() && now - aiOpenedAt < 800) return
        aiOpenedAt = now
        val i = Intent(this, AiActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP or Intent.FLAG_ACTIVITY_REORDER_TO_FRONT)
        if (!prompt.isNullOrBlank()) i.putExtra(AiActivity.EXTRA_PROMPT, prompt)
        runCatching { startActivity(i) }.onFailure { toast = friendly(it) }
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
                com.nocap.link.data.Lingo.fromServer(p.lingo)
                if (Regex("^#[0-9a-fA-F]{6}$").matches(p.accent)) Opt.accent.set(p.accent)
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                toast = say(e)
            }
        }
    }

    // ---------- backup ----------

    fun backupJson(): JSONObject = JSONObject().put("app", "nocap-os").put("layout", Home.toJson(portable = true)).put("settings", Prefs.export())

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
        val id = runCatching { Widgets.host(this).allocateAppWidgetId() }.getOrElse {
            toast = t("Couldn't add that widget.", "That widget couldn't be added.")
            return
        }
        pendingWidget = id
        pendingPage = page
        // The widget's app can be uninstalled while the picker is open.
        val bound = runCatching { Widgets.bind(this, id, info.provider) }.getOrDefault(false)
        if (bound) {
            configureOrPlace(id)
        } else {
            runCatching {
                bindWidget.launch(
                    Intent(AppWidgetManager.ACTION_APPWIDGET_BIND)
                        .putExtra(AppWidgetManager.EXTRA_APPWIDGET_ID, id)
                        .putExtra(AppWidgetManager.EXTRA_APPWIDGET_PROVIDER, info.provider),
                )
            }.onFailure {
                dropPending()
                toast = t("Couldn't add that widget.", "That widget couldn't be added.")
            }
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

    fun addNoCapWidget(kind: String, page: Int, w: Int = NoCapKinds.size(kind).first, h: Int = NoCapKinds.size(kind).second) {
        if (Opt.lockLayout.v) { toast = com.nocap.link.home.lockedMsg(); return }
        val id = newId()
        val p = Home.add({ c -> NoCapItem(id, c, kind) }, w.coerceAtMost(Prefs.columns), h.coerceAtMost(Prefs.rows), page)
        layer = Layer.NONE
        if (p != page) toast = t("No room there, so it went to page ${p + 1}.", "Added to page ${p + 1}.")
        // Widgets that need a choice first open their edit sheet.
        if (kind in setOf(NoCapKinds.APPS, NoCapKinds.WEATHER, NoCapKinds.NOTES, NoCapKinds.TIMER)) editing = Home.find(id)?.second as? NoCapItem
        if (kind == NoCapKinds.PHOTO) (Home.find(id)?.second as? NoCapItem)?.let(::pickPhotos)
    }

    /** A friendly line for anything that went wrong talking to the server. */
    fun say(e: Throwable): String = when (e) {
        is ServerTooOld -> needsUpdate((server as? ServerState.Online)?.me?.server?.version)
        else -> friendly(e)
    }

    companion object {
        const val ACTION_HUB = "com.nocap.link.HUB"
        const val ACTION_SEARCH = "com.nocap.link.SEARCH"
        const val ACTION_SEND_FILE = "com.nocap.link.SEND_FILE"
        const val ACTION_SETTINGS = "com.nocap.link.SETTINGS"
        private const val REQ_CONFIGURE = 4242
        private const val STATE_SETUP = "setup_step"

        fun friendlyWebView() = t(
            "This phone's web view is missing or updating. Try again in a minute.",
            "Android System WebView isn't available right now. Try again shortly.",
        )

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
