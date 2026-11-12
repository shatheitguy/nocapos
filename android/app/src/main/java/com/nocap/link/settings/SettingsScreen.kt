package com.nocap.link.settings

import android.Manifest
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.Image
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.Icons
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.nocap.link.BuildConfig
import com.nocap.link.data.Act
import com.nocap.link.data.AppFont
import com.nocap.link.data.BarStyle
import com.nocap.link.data.DockStyle
import com.nocap.link.data.DrawerSort
import com.nocap.link.data.DrawerStyle
import com.nocap.link.data.FolderStyle
import com.nocap.link.data.Gesture
import com.nocap.link.data.IconShape
import com.nocap.link.data.IconTheme
import com.nocap.link.data.LabelColor
import com.nocap.link.data.LabelFont
import com.nocap.link.data.Opt
import com.nocap.link.data.PageFx
import com.nocap.link.data.PhoneApps
import com.nocap.link.data.Prefs
import com.nocap.link.data.Rotation
import com.nocap.link.data.t
import com.nocap.link.data.ThemeMode
import com.nocap.link.drawer.AppIcon
import com.nocap.link.drawer.AppTile
import com.nocap.link.drawer.Drawer
import com.nocap.link.drawer.iconShape
import com.nocap.link.drawer.labelShadow
import com.nocap.link.drawer.matches
import com.nocap.link.drawer.Squircle
import com.nocap.link.HeartbeatWorker
import com.nocap.link.home.actName
import com.nocap.link.home.actOf
import com.nocap.link.home.bindingName
import com.nocap.link.home.From
import com.nocap.link.home.gestureName
import com.nocap.link.home.Home
import com.nocap.link.home.Layout
import com.nocap.link.home.Page
import com.nocap.link.home.Wallpapers
import com.nocap.link.Layer
import com.nocap.link.MainActivity
import com.nocap.link.Screen
import com.nocap.link.ServerState
import com.nocap.link.sync.Notices
import com.nocap.link.sync.Sync
import com.nocap.link.system.DotsService
import com.nocap.link.system.Phone
import com.nocap.link.ui.glass
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.SearchField
import com.nocap.link.ui.Wallpaper
import com.nocap.link.widget.Widgets

/** One searchable block of settings. */
private class Sec(val cat: String, val title: String, val sub: String?, val keys: String, val content: @Composable () -> Unit)

private fun cats() = listOf(
    "all" to t("All", "All"), "look" to t("Look", "Appearance"), "home" to t("Home", "Home screen"), "icons" to t("Icons", "Icons"),
    "dock" to t("Dock", "Dock"), "drawer" to t("Roster", "App drawer"), "gestures" to t("Gestures", "Gestures"),
    "widgets" to t("Widgets", "Widgets"), "link" to "NoCap Link", "notify" to t("Notifs & clipboard", "Notifications & clipboard"),
    "privacy" to t("Privacy & permissions", "Privacy & permissions"), "backup" to t("Save Point", "Backup"), "about" to t("About", "About"),
)

/** Launcher settings, laid out like NoCapOS's own Settings app, with search. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun SettingsScreen(a: MainActivity) {
    var tick by remember { mutableIntStateOf(0) } // re-check system grants
    LaunchedEffect(a.layer, a.resumeTick) { tick++ }
    var q by remember { mutableStateOf(a.settingsQuery) }
    LaunchedEffect(a.settingsQuery) { if (a.settingsQuery.isNotBlank()) { q = a.settingsQuery; a.settingsQuery = "" } }
    var cat by remember { mutableStateOf("all") }
    var forget by remember { mutableStateOf(false) }
    var phoneWall by remember { mutableStateOf(false) }
    var lockSetup by remember { mutableStateOf(false) }
    var bindFor by remember { mutableStateOf<Gesture?>(null) }
    var resetAsk by remember { mutableStateOf(false) }
    var allowList by remember { mutableStateOf(false) }
    var viewCrash by remember { mutableStateOf<String?>(null) }
    val askNotif = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
        if (ok) { Prefs.setServerNotices(true); HeartbeatWorker.now(a) } else a.toast = t("Notifications are off for NoCap OS, so no tea.", "Notifications are blocked for NoCap OS.")
    }
    val linked = a.server !is ServerState.NotLinked

    val secs = listOf(
        Sec("look", t("The Lingo", "Language"), t("How NoCap OS words things", "Wording style"), "language lingo gen z standard words wording server same") {
            LanguagePicker(a)
        },
        Sec("look", t("Theme & accent", "Theme and accent color"), t("Dark, light, or follow the phone", "Dark, light or automatic"), "theme dark light auto mode accent color colour red picker font typeface haptics vibration") {
            Label(t("Mode", "Mode"))
            Choice(listOf(t("Dark", "Dark") to ThemeMode.DARK, t("Light", "Light") to ThemeMode.LIGHT, t("Auto", "Automatic") to ThemeMode.AUTO), Opt.theme.v) { Opt.theme.set(it) }
            Spacer(Modifier.height(10.dp))
            Label(t("Accent", "Accent color"))
            AccentPicker(a, linked)
            Spacer(Modifier.height(10.dp))
            Label(t("Font", "Font"))
            Choice(listOf("NoCap" to AppFont.NOCAP, t("System", "System") to AppFont.SYSTEM, "Serif" to AppFont.SERIF, "Mono" to AppFont.MONO), Opt.font.v) { Opt.font.set(it) }
        },
        Sec("look", t("Wall Aesthetic", "Wallpaper"), t("The NoCapOS wallpapers, or your own pic", "Built-in wallpapers or your own picture"), "wallpaper background aesthetic picture dim blur parallax wall") {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Wallpapers.builtIn.forEach { w ->
                    Thumb(Prefs.wallpaper == w.id, Modifier.weight(1f), { Prefs.setWallpaper(w.id) }) {
                        Image(painterResource(w.thumb), w.genz, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
                    }
                }
                Thumb(Prefs.wallpaper == Wallpapers.GRID, Modifier.weight(1f), { Prefs.setWallpaper(Wallpapers.GRID) }) { Wallpaper {} }
                Thumb(Prefs.wallpaper == Wallpapers.CUSTOM, Modifier.weight(1f), { a.pickWallpaper() }) {
                    Box(Modifier.fillMaxSize().background(NoCap.Field), contentAlignment = Alignment.Center) { Text("+", style = MaterialTheme.typography.headlineSmall) }
                }
            }
            Hint(t("Tide · Nebula · Midnight · Classic grid · From your gallery", "Tide, Nebula, Midnight, Classic grid, or a picture from your gallery"))
            SliderRow(t("Dim", "Dim"), Opt.dim.v.toFloat(), 0f..80f, "${Opt.dim.v}%") { Opt.dim.set(it.toInt()) }
            SliderRow(t("Blur", "Blur"), Opt.blur.v.toFloat(), 0f..30f, "${Opt.blur.v}") { Opt.blur.set(it.toInt()) }
            Toggle(t("Parallax", "Parallax scrolling"), t("Wallpaper drifts as you swipe pages.", "The wallpaper moves slightly as you change pages."), Opt.parallax.v) { Opt.parallax.set(it) }
            Action(t("Set as my phone's wallpaper too", "Also set as phone wallpaper")) { phoneWall = true }
            if (linked) Action(t("Sync wallpaper & accent from my NoCap server", "Sync wallpaper and accent from my NoCap server")) { a.syncFromServer() }
        },
        Sec("home", t("The Hub grid", "Home screen grid"), t("Rows, columns, pages", "Grid size, page effects and layout lock"), "grid rows columns page transition effect cube fade zoom stack carousel search bar dots folder lock layout") {
            SliderRow(t("Columns", "Columns"), Opt.columns.v.toFloat(), 3f..7f, "${Opt.columns.v}", steps = 3) { Opt.columns.set(it.toInt()) }
            SliderRow(t("Rows", "Rows"), Opt.rows.v.toFloat(), 4f..9f, "${Opt.rows.v}", steps = 4) { Opt.rows.set(it.toInt()) }
            Label(t("Page flip", "Page transition"))
            Chips(listOf(t("Slide", "Slide") to PageFx.SLIDE, t("Fade", "Fade") to PageFx.FADE, t("Zoom", "Zoom") to PageFx.ZOOM, t("Cube", "Cube") to PageFx.CUBE, t("Stack", "Stack") to PageFx.STACK, t("Carousel", "Carousel") to PageFx.CAROUSEL), Opt.pageFx.v) { Opt.pageFx.set(it) }
            Spacer(Modifier.height(8.dp))
            Label(t("Folders", "Folder style"))
            Choice(listOf(t("Grid", "Grid") to FolderStyle.GRID, t("Stack", "Stack") to FolderStyle.STACK), Opt.folderStyle.v) { Opt.folderStyle.set(it) }
            Toggle(t("Search bar up top", "Show search bar"), null, Opt.searchBar.v) { Opt.searchBar.set(it) }
            Toggle(t("Page dots", "Show page indicator"), null, Opt.pageDots.v) { Opt.pageDots.set(it) }
            Toggle(t("Lock the layout", "Lock home screen layout"), t("No accidental moves, adds or yeets.", "Prevents moving, adding or removing items."), Opt.lockLayout.v) { Opt.lockLayout.set(it) }
        },
        Sec("icons", t("Icons", "Icons"), t("NoCap tiles, shapes, themed icons", "Style, shape and size"), "icon theme nocap tile shape circle squircle rounded square original themed monochrome tinted size glyph") {
            Label(t("Style", "Icon style"))
            Chips(
                listOf("NoCap" to IconTheme.NOCAP, t("Original", "Original") to IconTheme.ORIGINAL, t("Themed", "Themed") to IconTheme.THEMED, t("NoCap Red", "Tinted") to IconTheme.TINTED, t("Mono", "Monochrome") to IconTheme.MONO),
                Opt.iconTheme.v,
            ) { Opt.iconTheme.set(it) }
            if (Opt.iconTheme.v == IconTheme.NOCAP) Toggle(t("Glyph mode", "Monochrome glyphs"), t("Simple white glyphs on the NoCap tiles (Android 13+ icons; others get a mono look).", "Draws a single-color glyph on each tile where apps provide one."), Opt.nocapGlyph.v) { Opt.nocapGlyph.set(it) }
            Spacer(Modifier.height(8.dp))
            Label(t("Shape", "Shape"))
            Chips(listOf(t("Original", "Original") to IconShape.ORIGINAL, t("Circle", "Circle") to IconShape.CIRCLE, t("Squircle", "Squircle") to IconShape.SQUIRCLE, t("Rounded", "Rounded square") to IconShape.ROUNDED), Opt.iconShape.v) { Opt.iconShape.set(it) }
            IconPreview(a)
            SliderRow(t("Size", "Icon size"), Opt.iconSize.v.toFloat(), 36f..72f, "${Opt.iconSize.v}") { Opt.iconSize.set(it.toInt()) }
            Toggle(t("Notification dots", "Notification dots"), null, Opt.dots.v && DotsService.allowed(a)) { on -> Opt.dots.set(on); if (on && !DotsService.allowed(a)) DotsService.openSettings(a) }
        },
        Sec("icons", t("App names", "Icon labels"), t("Font, size, color", "Label font, size and color"), "label labels name font size color colour shadow text") {
            Toggle(t("Show app names", "Show labels"), null, Opt.labels.v) { Opt.labels.set(it) }
            Label(t("Font", "Font"))
            Chips(listOf("NoCap" to LabelFont.NOCAP, t("System", "System") to LabelFont.SYSTEM, "Orbitron" to LabelFont.ORBITRON, "Serif" to LabelFont.SERIF, "Mono" to LabelFont.MONO), Opt.labelFont.v) { Opt.labelFont.set(it) }
            SliderRow(t("Size", "Size"), Opt.labelSize.v.toFloat(), 9f..16f, "${Opt.labelSize.v}", steps = 6) { Opt.labelSize.set(it.toInt()) }
            Label(t("Color", "Color"))
            Chips(listOf(t("White", "White") to LabelColor.WHITE, t("Accent", "Accent") to LabelColor.ACCENT, t("Soft", "Soft white") to LabelColor.SOFT, t("Dark", "Dark") to LabelColor.DARK), Opt.labelColor.v) { Opt.labelColor.set(it) }
            Toggle(t("Shadow", "Text shadow"), null, Opt.labelShadow.v) { Opt.labelShadow.set(it) }
        },
        Sec("dock", t("Dock", "Dock"), t("Like the NoCap desktop's", "Style and number of apps"), "dock style glass floating none count apps buttons roster labels") {
            Label(t("Style", "Style"))
            Choice(listOf(t("Glass bar", "Glass bar") to DockStyle.GLASS, t("Floating", "Floating") to DockStyle.FLOATING, t("None", "None") to DockStyle.NONE), Opt.dockStyle.v) { Opt.dockStyle.set(it) }
            SliderRow(t("Apps", "Apps"), Opt.dockCount.v.toFloat(), 3f..7f, "${Opt.dockCount.v}", steps = 3) { Opt.dockCount.set(it.toInt()); Home.setDock(Home.layout.dock) }
            Toggle(t("Roster + NoCap buttons", "Show app drawer and NoCap Link buttons"), null, Opt.dockButtons.v) { Opt.dockButtons.set(it) }
            Toggle(t("Names under dock apps", "Dock labels"), null, Opt.dockLabels.v) { Opt.dockLabels.set(it) }
        },
        Sec("drawer", t("The Roster", "App drawer"), t("Six styles, pick your vibe", "Drawer style, sort and look"), "drawer roster launchpad style paged grid list categories classic home pages library sort columns opacity blur recent") {
            DrawerStylePicker()
            Spacer(Modifier.height(8.dp))
            SliderRow(t("Columns", "Columns"), Opt.drawerColumns.v.toFloat(), 3f..7f, "${Opt.drawerColumns.v}", steps = 3) { Opt.drawerColumns.set(it.toInt()) }
            Label(t("Sort", "Sort"))
            Choice(listOf("A–Z" to DrawerSort.AZ, t("Most used", "Most used") to DrawerSort.MOST_USED, t("Newest", "Recently installed") to DrawerSort.RECENT), Opt.drawerSort.v) { Opt.drawerSort.set(it) }
            SliderRow(t("Background", "Background"), Opt.drawerOpacity.v.toFloat(), 30f..100f, "${Opt.drawerOpacity.v}%") { Opt.drawerOpacity.set(it.toInt()) }
            Toggle(t("Blur behind", "Blur the home screen behind"), if (Build.VERSION.SDK_INT < 31) t("Needs Android 12+.", "Requires Android 12 or later.") else null, Opt.drawerBlur.v) { Opt.drawerBlur.set(it) }
            Toggle(t("Recent apps row", "Show recent apps"), null, Opt.drawerRecents.v) { Opt.drawerRecents.set(it) }
        },
        Sec("drawer", t("Hidden apps", "Hidden apps"), t("Hidden from the Roster and search", "Hidden from the app drawer and search"), "hide hidden unhide") {
            val hidden = a.phoneApps.filter { it.key in Prefs.hidden }
            if (hidden.isEmpty()) Hint(t("Nothing hidden. Long-press an app → Hide.", "No hidden apps. Long-press an app and choose Hide."))
            hidden.forEach { app ->
                Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                    AppIcon(app, 30.dp)
                    Text(app.label, Modifier.weight(1f).padding(start = 10.dp), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text(t("Unhide", "Unhide"), Modifier.clip(RoundedCornerShape(8.dp)).clickable { Prefs.hide(app.key, false) }.padding(8.dp), style = MaterialTheme.typography.labelLarge, color = NoCap.Red)
                }
            }
        },
        Sec("gestures", t("Gestures", "Gestures"), t("Bind swipes, taps and pinches", "Choose what each gesture does"), "gesture swipe up down double tap pinch two finger home button lock notifications shade quick settings bind action") {
            Gesture.entries.forEach { g ->
                Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { bindFor = g }.padding(vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(gestureName(g), Modifier.weight(1f), style = MaterialTheme.typography.titleSmall)
                    Text(bindingName(a, Opt.binding(g)), style = MaterialTheme.typography.labelLarge, color = NoCap.Red, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.widthIn(max = 180.dp))
                }
            }
            val canLock = remember(tick) { Phone.canLock(a) }
            if (Opt.gestures.v.values.any { it == Act.LOCK.name } && !canLock) {
                Action(t("Lock needs the lock helper — set it up", "Set up the lock helper")) { lockSetup = true }
            }
            Hint(t("Lock uses an accessibility helper that only locks the screen. Off unless you bind it.", "Locking uses an accessibility service that only locks the screen."))
        },
        Sec("widgets", t("Widgets", "Widgets"), t("NoCap widgets + every app's", "Weather place and units"), "widget widgets weather celsius fahrenheit place city clock") {
            Action(t("Add widgets", "Add widgets")) { a.layer = Layer.WIDGETS }
            Hint(t("Long-press a NoCap widget → Customize for faces, sizes and more.", "Long-press a NoCap widget and choose Customize to change it."))
            Toggle(t("Fahrenheit", "Use °F"), null, Opt.fahrenheit.v) { Opt.fahrenheit.set(it) }
            val place = com.nocap.link.widget.Weather.Place.decode(Opt.weatherPlace.v)
            Hint(t("Shared weather place: ${place?.name ?: "none yet"}", "Shared weather location: ${place?.name ?: "not set"}"))
        },
        Sec("notify", t("Notifications & clipboard", "Notifications and clipboard"), t("Between this phone and NoCap OS", "Sync with your NoCap server"), "sync notifications forward mirror clipboard copy paste server tea alerts live") {
            if (!linked) {
                Hint(t("Link your NoCap server first.", "Link a NoCap server first."))
                Action(t("Link with NoCap Link", "Link with NoCap Link")) { a.layer = Layer.NONE; a.screen = Screen.LinkForm() }
                return@Sec
            }
            Toggle(
                t("Server tea on this phone", "Server notifications"),
                t("NoCap OS notifications show up here — instantly while NoCap OS runs, otherwise about every 15 min.", "Show NoCap OS notifications on this phone: live while the app runs, otherwise about every 15 minutes."),
                Prefs.serverNotices,
            ) { on ->
                if (on && Build.VERSION.SDK_INT >= 33 && !Notices.allowed(a)) askNotif.launch(Manifest.permission.POST_NOTIFICATIONS)
                else { Prefs.setServerNotices(on); Opt.liveNotices.set(on); if (on) HeartbeatWorker.now(a); Sync.start(a) }
            }
            val access = remember(tick) { DotsService.allowed(a) }
            Toggle(
                t("Send this phone's notifications to NoCap OS", "Forward phone notifications"),
                if (!access) t("Needs notification access. Only apps you pick, only to your own server.", "Requires notification access. Only the apps you choose, only to your server.")
                else t("${Opt.forwardApps.v.size} apps picked. They show in NoCap OS as “From ${Build.MODEL}”.", "${Opt.forwardApps.v.size} apps selected."),
                Opt.forward.v && access,
            ) { on ->
                Opt.forward.set(on)
                if (on && !access) DotsService.openSettings(a)
                if (on && Opt.forwardApps.v.isEmpty()) allowList = true
                Sync.start(a)
            }
            if (Opt.forward.v) Action(t("Pick which apps", "Choose apps")) { allowList = true }
            Toggle(
                t("Shared clipboard", "Clipboard sync"),
                t("Copy on NoCap OS → it lands here. Copy here → sent when NoCap OS opens, or use the “Send clipboard to NoCap” quick-settings tile (Android only lets apps in front read it).", "Clipboard from NoCap OS is copied here. Phone clipboard is sent when NoCap OS is opened, or from the quick-settings tile (Android only allows the app in front to read it)."),
                Opt.clipSync.v,
            ) { Opt.clipSync.set(it); Sync.start(a) }
            Action(t("Send my clipboard now", "Send clipboard now")) { Sync.sendClipboard(a, force = true) { _, m -> a.toast = m } }
        },
        Sec("link", "NoCap Link", t("Your server, this phone", "Server connection"), "link server unlink pair desktop status online offline update") {
            ServerStatus(a)
            if (linked) {
                Action(t("Open NoCap Link", "Open NoCap Link")) { a.layer = Layer.HUB }
                Action(t("Unlink this phone", "Unlink this phone"), danger = true) { forget = true }
            } else {
                Action(t("Link with NoCap Link", "Link with NoCap Link")) { a.layer = Layer.NONE; a.screen = Screen.LinkForm() }
            }
        },
        Sec("look", t("Bars & screen", "Status bar, navigation and rotation"), t("Transparency, hide, rotation, haptics", "System bars, rotation and haptics"), "status bar navigation nav transparent hide hidden fullscreen rotation landscape portrait haptics vibration") {
            Label(t("Status bar", "Status bar"))
            Choice(listOf(t("Clear", "Transparent") to BarStyle.CLEAR, t("Tinted", "Tinted") to BarStyle.TINTED, t("Hidden", "Hidden") to BarStyle.HIDDEN), Opt.statusBar.v) { Opt.statusBar.set(it) }
            Spacer(Modifier.height(8.dp))
            Label(t("Nav bar", "Navigation bar"))
            Choice(listOf(t("Clear", "Transparent") to BarStyle.CLEAR, t("Tinted", "Tinted") to BarStyle.TINTED), Opt.navBar.v) { Opt.navBar.set(it) }
            Spacer(Modifier.height(8.dp))
            Label(t("Rotation", "Rotation"))
            Choice(listOf(t("Auto", "Automatic") to Rotation.AUTO, t("Portrait", "Portrait") to Rotation.PORTRAIT, t("Landscape", "Landscape") to Rotation.LANDSCAPE), Opt.rotation.v) { Opt.rotation.set(it) }
            Toggle(t("Haptics", "Haptic feedback"), null, Opt.haptics.v) { Opt.haptics.set(it) }
        },
        Sec("home", t("Home app", "Default home app"), null, "default home launcher") {
            val isHome = remember(tick) { PhoneApps.isDefaultHome(a) }
            Hint(if (isHome) t("NoCap OS is your home screen. W.", "NoCap OS is your default home app.") else t("NoCap OS isn't your home screen yet.", "NoCap OS isn't the default home app."))
            Action(if (isHome) t("Change home app", "Change default home app") else t("Make NoCap OS my home screen", "Set as default home app")) { PhoneApps.askToBeHome(a) }
        },
        Sec("backup", t("Save Point", "Export & restore"), t("Pages, folders, dock and every setting", "Layout and all settings"), "backup restore layout export import reset defaults") {
            if (linked) {
                Action(t("Back up to my NoCap server", "Back up to my NoCap server")) { a.backupToServer() }
                Action(t("Restore from my NoCap server", "Restore from my NoCap server")) { a.restoreFromServer() }
            }
            Action(t("Save to a file", "Export to a file")) { a.exportToFile() }
            Action(t("Load from a file", "Import from a file")) { a.importFromFile() }
            Action(t("Reset settings to default", "Reset all settings"), danger = true) { resetAsk = true }
            Hint(t("Android widgets and custom icons stay put — they belong to this phone.", "Android widgets and custom icons aren't included; they belong to this phone."))
        },
        Sec("privacy", t("Permissions", "Permissions"), t("What NoCap OS can use, and why", "What each permission is for"), "permissions privacy camera notifications location calendar accessibility device admin notification access do not disturb lock home") {
            val missing = remember(tick) { com.nocap.link.settings.Permissions.missingCore(a) }
            Hint(
                if (missing == 0) t("The basics are all on. Extras only if you use them.", "All recommended permissions are allowed.")
                else t("$missing of the basics are off.", "$missing recommended permission(s) not allowed."),
            )
            Action(t("Open permissions", "Manage permissions")) { a.layer = Layer.PERMISSIONS }
            Hint(t("Talks only to the NoCap server you link (and the weather service for forecasts). Your key stays encrypted on this phone.", "Connects only to your linked NoCap server and the weather service. The device key is encrypted on this phone."))
        },
        Sec("privacy", t("Lock helper", "Lock helper"), null, "lock accessibility device admin double tap turn off") {
            val canLock = remember(tick) { Phone.canLock(a) }
            Hint(if (canLock) t("On. It can only lock the screen.", "Enabled. It can only lock the screen.") else t("Off. Bind Lock to a gesture to use it.", "Off. Assign Lock to a gesture to use it."))
            if (canLock) Action(t("Turn the lock helper off", "Turn off the lock helper")) { Phone.turnOffLockHelpers(a); runCatching { a.startActivity(Phone.lockSetupIntent(a)) } }
            else Action(t("Set up the lock helper", "Set up the lock helper")) { lockSetup = true }
        },
        Sec("about", t("About", "About"), null, "about version licenses fonts setup crash report") {
            Hint("NoCap OS ${BuildConfig.VERSION_NAME} (${BuildConfig.VERSION_CODE})")
            Action(t("Run setup again", "Run setup again")) { a.layer = Layer.NONE; a.screen = Screen.Setup() }
            val crash = remember(tick) { com.nocap.link.system.CrashLog.last(a) }
            if (crash == null) {
                Hint(t("No crashes on record. Clean.", "No crash reports."))
            } else {
                Hint(crash.lineSequence().drop(1).firstOrNull().orEmpty())
                Action(t("View the last crash report", "View last crash report")) { viewCrash = crash }
                if (linked) Action(t("Send it to my NoCap server", "Send to my NoCap server")) { a.sendCrashReport(crash) }
                Action(t("Clear it", "Delete crash report"), danger = true) { com.nocap.link.system.CrashLog.clear(a); tick++ }
            }
            Hint(t("Orbitron & Rajdhani fonts under the SIL Open Font License · Weather by Open-Meteo", "Orbitron and Rajdhani fonts: SIL Open Font License. Weather data: Open-Meteo."))
        },
    )
    val term = q.trim().lowercase()
    val shown = secs.filter { s ->
        (cat == "all" || s.cat == cat) && (term.isEmpty() || term in s.title.lowercase() || term in (s.sub ?: "").lowercase() || s.keys.split(' ').any { it.startsWith(term) } || term in s.keys)
    }

    Column(
        Modifier.fillMaxSize().background(NoCap.Sheet).clickable(remember { MutableInteractionSource() }, null) {}
            .statusBarsPadding().navigationBarsPadding(),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(Modifier.widthIn(max = 680.dp).fillMaxWidth()) {
            Row(Modifier.fillMaxWidth().padding(start = 18.dp, end = 8.dp, top = 10.dp, bottom = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(t("SETTINGS", "SETTINGS"), style = MaterialTheme.typography.headlineSmall, modifier = Modifier.weight(1f))
                IconButton(onClick = { a.layer = Layer.NONE }) { Icon(Icons.Filled.Close, t("Close", "Close"), tint = NoCap.Text) }
            }
            SearchField(q, { q = it }, t("Search settings…", "Search settings"), Modifier.padding(horizontal = 14.dp))
            Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 14.dp, vertical = 10.dp), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                cats().forEach { (id, name) ->
                    Text(
                        name, Modifier.clip(RoundedCornerShape(50)).background(if (cat == id) NoCap.Red else NoCap.Field).clickable { cat = id }.padding(horizontal = 12.dp, vertical = 6.dp),
                        style = MaterialTheme.typography.labelLarge, color = if (cat == id) Color.White else NoCap.Text, maxLines = 1,
                    )
                }
            }
            LazyColumn(contentPadding = PaddingValues(start = 14.dp, end = 14.dp, bottom = 28.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
                if (shown.isEmpty()) item { Text(t("Nothing matches “$q”.", "No settings match “$q”."), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted) }
                items(shown, key = { it.title }) { s -> Group(s.title, s.sub) { s.content() } }
            }
        }
    }

    bindFor?.let { g -> BindDialog(a, g, onPicked = { act -> if (act == Act.LOCK.name && !Phone.canLock(a)) lockSetup = true }) { bindFor = null } }
    if (allowList) ForwardAppsDialog(a) { allowList = false }
    viewCrash?.let { r -> CrashViewer(r) { viewCrash = null } }
    if (resetAsk) {
        AlertDialog(
            onDismissRequest = { resetAsk = false },
            containerColor = NoCap.GlassStrong,
            title = { Text(t("Back to factory vibes?", "Reset all settings?"), style = MaterialTheme.typography.titleMedium) },
            text = { Text(t("Every setting goes back to default. Your pages, apps and link stay.", "All settings return to their defaults. Your layout and link are kept."), style = MaterialTheme.typography.bodyMedium) },
            confirmButton = { TextButton(onClick = { resetAsk = false; Opt.resetAll(); a.reloadApps() }) { Text(t("Reset", "Reset"), color = NoCap.Red) } },
            dismissButton = { TextButton(onClick = { resetAsk = false }) { Text(t("Nah", "Cancel"), color = NoCap.Text) } },
        )
    }
    if (phoneWall) {
        AlertDialog(
            onDismissRequest = { phoneWall = false },
            containerColor = NoCap.GlassStrong,
            title = { Text(t("Where should it go?", "Set wallpaper on"), style = MaterialTheme.typography.titleMedium) },
            text = { Text(t("Puts the current NoCap wallpaper on your phone itself.", "Applies the current wallpaper to your phone."), style = MaterialTheme.typography.bodyMedium) },
            confirmButton = {
                Row {
                    TextButton(onClick = { phoneWall = false; a.setPhoneWallpaper(true, false) }) { Text(t("Home", "Home screen"), color = NoCap.Red) }
                    TextButton(onClick = { phoneWall = false; a.setPhoneWallpaper(false, true) }) { Text(t("Lock", "Lock screen"), color = NoCap.Red) }
                    TextButton(onClick = { phoneWall = false; a.setPhoneWallpaper(true, true) }) { Text(t("Both", "Both"), color = NoCap.Red) }
                }
            },
        )
    }
    if (lockSetup) {
        AlertDialog(
            onDismissRequest = { lockSetup = false },
            containerColor = NoCap.GlassStrong,
            title = { Text(t("Switch on the lock helper", "Enable the lock helper"), style = MaterialTheme.typography.titleMedium) },
            text = {
                Text(
                    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) t("In Accessibility, find NoCap OS lock and turn it on. It can only lock your screen — nothing else.", "In Accessibility settings, turn on NoCap OS lock. It only locks the screen.")
                    else t("Allow NoCap OS as a device admin. It can only lock the screen.", "Allow NoCap OS as a device admin. It can only lock the screen."),
                    style = MaterialTheme.typography.bodyMedium,
                )
            },
            confirmButton = { TextButton(onClick = { lockSetup = false; runCatching { a.startActivity(Phone.lockSetupIntent(a)) } }) { Text(t("Take me there", "Open settings"), color = NoCap.Red) } },
            dismissButton = { TextButton(onClick = { lockSetup = false }) { Text(t("Nah", "Cancel"), color = NoCap.Text) } },
        )
    }
    if (forget) {
        AlertDialog(
            onDismissRequest = { forget = false },
            containerColor = NoCap.GlassStrong,
            title = { Text(t("Unlink this phone?", "Unlink this phone?"), style = MaterialTheme.typography.titleMedium) },
            text = { Text(t("This phone drops its key, signs out of your NoCap desktop, and your server forgets it.", "This phone deletes its key, signs out of the NoCap desktop and is removed from your server's devices."), style = MaterialTheme.typography.bodyMedium) },
            confirmButton = { TextButton(onClick = { forget = false; a.layer = Layer.NONE; a.forget(alsoServer = true) }) { Text(t("Unlink", "Unlink"), color = NoCap.Red) } },
            dismissButton = { TextButton(onClick = { forget = false }) { Text(t("Nah", "Cancel"), color = NoCap.Text) } },
        )
    }
}

/** Online, offline, certificate changed, or the server needs an update. */
@Composable
private fun ServerStatus(a: MainActivity) {
    val (text, color) = when (val s = a.server) {
        is ServerState.Online -> (if (a.outdated != null) MainActivity.needsUpdate(a.outdated) else t("Online: ${s.me.server.name.ifBlank { "NoCapOS" }} v${s.me.server.version}", "Connected to ${s.me.server.name.ifBlank { "NoCapOS" }} (v${s.me.server.version})")) to (if (a.outdated != null) NoCap.Warn else NoCap.Ok)
        ServerState.Checking -> t("Checking in…", "Connecting…") to NoCap.Muted
        is ServerState.Offline -> t("Can't reach it: ${s.why}", "Offline: ${s.why}") to NoCap.Warn
        is ServerState.CertChanged -> t("Its certificate changed. Check it on the home screen.", "The server certificate changed. Review it on the home screen.") to NoCap.Bad
        ServerState.Unlinked -> t("Your server forgot this phone. Link it again.", "This phone is no longer linked. Link it again.") to NoCap.Bad
        ServerState.NotLinked -> t("Not linked yet.", "Not linked.") to NoCap.Muted
    }
    Text(text, Modifier.padding(vertical = 4.dp), style = MaterialTheme.typography.titleSmall, color = color)
    if (a.server is ServerState.Offline) Action(t("Try again", "Retry")) { a.refresh() }
}

/** NoCap red, presets, or any color (hue + brightness). */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun AccentPicker(a: MainActivity, linked: Boolean) {
    val cur = NoCap.Red
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        NoCap.presets.forEach { c ->
            val col = Color(c)
            val on = NoCap.hex(col).equals(Opt.accent.v, ignoreCase = true)
            Box(
                Modifier.size(34.dp).clip(CircleShape).background(col).border(if (on) 3.dp else 1.dp, if (on) NoCap.Text else NoCap.GlassEdge, CircleShape)
                    .clickable { Opt.accent.set(NoCap.hex(col)) },
            )
        }
    }
    val hsv = FloatArray(3).also { android.graphics.Color.colorToHSV(android.graphics.Color.rgb((cur.red * 255).toInt(), (cur.green * 255).toInt(), (cur.blue * 255).toInt()), it) }
    fun set(h: Float, s: Float, v: Float) { Opt.accent.set(NoCap.hex(Color(android.graphics.Color.HSVToColor(floatArrayOf(h, s, v))))) }
    Spacer(Modifier.height(8.dp))
    Box(Modifier.fillMaxWidth().height(14.dp).clip(RoundedCornerShape(7.dp)).background(Brush.horizontalGradient((0..6).map { Color(android.graphics.Color.HSVToColor(floatArrayOf(it * 60f, 1f, 1f))) })))
    Slider(hsv[0], { set(it, hsv[1].coerceAtLeast(0.5f), hsv[2].coerceAtLeast(0.6f)) }, valueRange = 0f..360f, colors = SliderDefaults.colors(thumbColor = cur, activeTrackColor = Color.Transparent, inactiveTrackColor = Color.Transparent))
    SliderRow(t("Punch", "Saturation"), hsv[1], 0.2f..1f, "${(hsv[1] * 100).toInt()}%") { set(hsv[0], it, hsv[2]) }
    SliderRow(t("Glow", "Brightness"), hsv[2], 0.4f..1f, "${(hsv[2] * 100).toInt()}%") { set(hsv[0], hsv[1], it) }
    Row {
        Text(t("Back to NoCap red", "Reset to NoCap red"), Modifier.clip(RoundedCornerShape(10.dp)).clickable { Opt.accent.reset() }.padding(vertical = 8.dp), style = MaterialTheme.typography.titleSmall, color = NoCap.Red)
    }
    if (linked) Hint(t("“Sync from my NoCap server” in Wallpaper also takes your desktop's accent.", "Syncing from your NoCap server also applies its accent color."))
}

/** A row of the current icon look, with a few real apps. */
@Composable
private fun IconPreview(a: MainActivity) {
    val apps = remember(a.phoneApps) { a.phoneApps.take(5) }
    if (apps.isEmpty()) return
    Row(Modifier.fillMaxWidth().padding(vertical = 8.dp).clip(RoundedCornerShape(14.dp)).background(Color(0xFF14161C)).padding(10.dp), horizontalArrangement = Arrangement.SpaceEvenly) {
        apps.forEach { AppTile(it, size = 44.dp, label = Opt.labels.v, labelWidth = 60.dp) }
    }
}

/** The drawer styles as little live sketches. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun DrawerStylePicker() {
    val styles = listOf(
        DrawerStyle.PAGED to t("Launchpad", "Paged"), DrawerStyle.GRID to t("Scroll", "Grid"), DrawerStyle.LIST to "A–Z",
        DrawerStyle.CATEGORIES to t("Vibes", "Categories"), DrawerStyle.CLASSIC to t("Classic", "Classic drawer"), DrawerStyle.HOME_PAGES to t("All on home", "All apps on home"),
    )
    val accent = NoCap.Red
    val ink = NoCap.Text
    FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp), maxItemsInEachRow = 3) {
        styles.forEach { (s, name) ->
            val on = Opt.drawer.v == s
            Column(
                Modifier.weight(1f).clip(RoundedCornerShape(14.dp)).border(if (on) 2.dp else 1.dp, if (on) accent else NoCap.GlassEdge, RoundedCornerShape(14.dp))
                    .clickable {
                        Opt.drawer.set(s)
                        // With every app on the home pages, swipe down is for search.
                        if (s == DrawerStyle.HOME_PAGES && Opt.binding(Gesture.SWIPE_DOWN) == Act.NOTIFICATIONS.name) Opt.bind(Gesture.SWIPE_DOWN, Act.SEARCH.name)
                    }.padding(8.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Canvas(Modifier.fillMaxWidth().aspectRatio(0.75f).clip(RoundedCornerShape(8.dp)).background(Color(0xFF0E1015))) {
                    val w = size.width; val h = size.height
                    val dot = ink.copy(alpha = 0.55f)
                    fun tile(x: Float, y: Float, r: Float, c: Color = dot) = drawRoundRect(c, Offset(x - r, y - r), Size(r * 2, r * 2), CornerRadius(r * 0.5f))
                    when (s) {
                        DrawerStyle.PAGED -> {
                            for (i in 0 until 4) for (j in 0 until 4) tile(w * (i + 0.5f) / 4, h * (j + 0.7f) / 5.2f, w * 0.07f)
                            for (i in 0 until 3) drawCircle(if (i == 0) accent else dot, w * 0.025f, Offset(w * (0.42f + i * 0.08f), h * 0.93f))
                        }
                        DrawerStyle.GRID -> {
                            for (i in 0 until 4) for (j in 0 until 6) tile(w * (i + 0.5f) / 4, h * (j + 0.6f) / 6f, w * 0.06f)
                            drawRect(accent, Offset(w * 0.95f, h * 0.1f), Size(w * 0.02f, h * 0.25f))
                        }
                        DrawerStyle.LIST -> for (j in 0 until 7) {
                            tile(w * 0.15f, h * (j + 0.6f) / 7f, w * 0.05f)
                            drawRect(dot, Offset(w * 0.28f, h * (j + 0.55f) / 7f), Size(w * 0.45f, h * 0.02f))
                            drawRect(if (j == 2) accent else dot.copy(alpha = 0.3f), Offset(w * 0.9f, h * (j + 0.5f) / 7f), Size(w * 0.04f, h * 0.04f))
                        }
                        DrawerStyle.CATEGORIES -> for (j in 0 until 3) {
                            drawRoundRect(dot.copy(alpha = 0.18f), Offset(w * 0.08f, h * (0.06f + j * 0.31f)), Size(w * 0.84f, h * 0.27f), CornerRadius(w * 0.05f))
                            for (i in 0 until 4) tile(w * (0.2f + i * 0.2f), h * (0.22f + j * 0.31f), w * 0.05f, if (j == 0 && i == 0) accent else dot)
                        }
                        DrawerStyle.CLASSIC -> {
                            drawRoundRect(dot.copy(alpha = 0.3f), Offset(w * 0.08f, h * 0.05f), Size(w * 0.84f, h * 0.08f), CornerRadius(h * 0.04f))
                            for (i in 0 until 3) drawRoundRect(if (i == 0) accent else dot.copy(alpha = 0.3f), Offset(w * (0.08f + i * 0.26f), h * 0.17f), Size(w * 0.22f, h * 0.05f), CornerRadius(h * 0.025f))
                            for (i in 0 until 4) tile(w * (i + 0.5f) / 4, h * 0.32f, w * 0.06f, accent.copy(alpha = 0.8f))
                            for (j in 0 until 4) { tile(w * 0.15f, h * (0.48f + j * 0.13f), w * 0.045f); drawRect(dot, Offset(w * 0.27f, h * (0.47f + j * 0.13f)), Size(w * 0.45f, h * 0.02f)) }
                        }
                        DrawerStyle.HOME_PAGES -> {
                            for (i in 0 until 4) for (j in 0 until 4) tile(w * (i + 0.5f) / 4 * 0.6f, h * (j + 0.7f) / 5f, w * 0.045f)
                            drawRect(dot.copy(alpha = 0.3f), Offset(w * 0.62f, 0f), Size(w * 0.01f, h))
                            for (i in 0 until 2) for (j in 0 until 3) drawRoundRect(if (i == 0 && j == 0) accent.copy(alpha = 0.7f) else dot.copy(alpha = 0.35f), Offset(w * (0.67f + i * 0.16f), h * (0.12f + j * 0.28f)), Size(w * 0.13f, w * 0.13f), CornerRadius(w * 0.03f))
                        }
                    }
                }
                Text(name, Modifier.padding(top = 6.dp), style = MaterialTheme.typography.labelSmall, color = if (on) accent else NoCap.Text, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
    }
    Hint(
        when (Opt.drawer.v) {
            DrawerStyle.HOME_PAGES -> t("Every app lives on your pages; new ones land on the last page. Swipe to the end for The Stash, your apps grouped by vibe.", "All apps are on the home pages; new apps go to the last page. The last page groups apps by type.")
            DrawerStyle.CLASSIC -> t("Swipe up: search, suggestions, then everything A–Z, with All / Fresh / Hidden tabs.", "Swipe up for a scrolling drawer with search, suggestions, A–Z and tabs.")
            else -> t("Swipe up opens it.", "Swipe up to open it.")
        },
    )
}

@Composable
private fun BindDialog(a: MainActivity, g: Gesture, onPicked: (String) -> Unit, onClose: () -> Unit) {
    var pickApp by remember { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = onClose,
        containerColor = NoCap.GlassStrong,
        title = { Text(gestureName(g), style = MaterialTheme.typography.titleMedium) },
        text = {
            Column(Modifier.heightIn(max = 460.dp).verticalScroll(rememberScrollState())) {
                if (!pickApp) {
                    Act.entries.filter { it != Act.FIRST_PAGE || g == Gesture.HOME || true }.forEach { act ->
                        val on = com.nocap.link.home.actOf(Opt.binding(g)) == act
                        Text(
                            actName(act), Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable {
                                if (act == Act.APP) pickApp = true else { Opt.bind(g, act.name); onPicked(act.name); onClose() }
                            }.padding(vertical = 10.dp, horizontal = 6.dp),
                            style = MaterialTheme.typography.titleSmall, color = if (on) NoCap.Red else NoCap.Text,
                        )
                    }
                } else {
                    a.phoneApps.filter { it.key !in Prefs.hidden }.forEach { app ->
                        Row(
                            Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { Opt.bind(g, "APP:" + app.key); onClose() }.padding(vertical = 6.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            AppIcon(app, 30.dp)
                            Text(app.label, Modifier.padding(start = 10.dp), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onClose) { Text(t("Done", "Done"), color = NoCap.Red) } },
    )
}

@Composable
private fun ForwardAppsDialog(a: MainActivity, onClose: () -> Unit) {
    var q by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onClose,
        containerColor = NoCap.GlassStrong,
        title = { Text(t("Which apps can send to NoCap OS?", "Apps to forward"), style = MaterialTheme.typography.titleMedium) },
        text = {
            Column {
                SearchField(q, { q = it }, t("Find an app", "Search apps"))
                Spacer(Modifier.height(8.dp))
                Column(Modifier.heightIn(max = 400.dp).verticalScroll(rememberScrollState())) {
                    a.phoneApps.distinctBy { it.pkg }.filter { q.isBlank() || q.trim().lowercase() in it.label.lowercase() }.forEach { app ->
                        val on = app.pkg in Opt.forwardApps.v
                        Row(
                            Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable {
                                Opt.forwardApps.set(if (on) Opt.forwardApps.v - app.pkg else Opt.forwardApps.v + app.pkg)
                            }.padding(vertical = 5.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            AppIcon(app, 30.dp)
                            Text(app.label, Modifier.weight(1f).padding(start = 10.dp), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Switch(on, null, colors = SwitchDefaults.colors(checkedTrackColor = NoCap.Red, checkedThumbColor = Color.White))
                        }
                    }
                }
            }
        },
        confirmButton = { TextButton(onClick = onClose) { Text(t("Done", "Done"), color = NoCap.Red) } },
    )
}

@Composable
fun Group(title: String, sub: String?, content: @Composable () -> Unit) {
    Column(Modifier.fillMaxWidth().glass(RoundedCornerShape(18.dp)).padding(14.dp)) {
        Text(title, style = MaterialTheme.typography.titleMedium)
        if (!sub.isNullOrBlank()) Text(sub, style = MaterialTheme.typography.bodySmall)
        Spacer(Modifier.height(10.dp))
        content()
    }
}

@Composable
fun Hint(text: String) {
    if (text.isNotBlank()) Text(text, Modifier.padding(vertical = 4.dp), style = MaterialTheme.typography.bodySmall)
}

/** A segmented control; wraps to chips when the labels wouldn't fit. */
@Composable
fun <T> Choice(options: List<Pair<String, T>>, selected: T, onPick: (T) -> Unit) {
    if (options.size > 4 || options.sumOf { it.first.length } > 34) { Chips(options, selected, onPick); return }
    Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(NoCap.Field).padding(3.dp)) {
        options.forEach { (label, v) ->
            val on = v == selected
            Box(
                Modifier.weight(1f).clip(RoundedCornerShape(10.dp)).background(if (on) NoCap.Red else Color.Transparent).clickable { onPick(v) }.padding(vertical = 8.dp, horizontal = 2.dp),
                contentAlignment = Alignment.Center,
            ) { Text(label, style = MaterialTheme.typography.labelLarge, color = if (on) Color.White else NoCap.Text, maxLines = 1, overflow = TextOverflow.Ellipsis) }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun <T> Chips(options: List<Pair<String, T>>, selected: T, onPick: (T) -> Unit) {
    FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        options.forEach { (label, v) ->
            val on = v == selected
            Text(
                label, Modifier.clip(RoundedCornerShape(50)).background(if (on) NoCap.Red else NoCap.Field).clickable { onPick(v) }.padding(horizontal = 12.dp, vertical = 7.dp),
                style = MaterialTheme.typography.labelLarge, color = if (on) Color.White else NoCap.Text, maxLines = 1,
            )
        }
    }
}

@Composable
fun Toggle(title: String, sub: String?, on: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { onChange(!on) }.padding(vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f).padding(end = 8.dp)) {
            Text(title, style = MaterialTheme.typography.titleSmall)
            if (!sub.isNullOrBlank()) Text(sub, style = MaterialTheme.typography.bodySmall)
        }
        Switch(on, onChange, colors = SwitchDefaults.colors(checkedTrackColor = NoCap.Red, checkedThumbColor = Color.White))
    }
}

@Composable
fun Action(title: String, danger: Boolean = false, onClick: () -> Unit) {
    Text(
        title, Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable(onClick = onClick).padding(vertical = 10.dp),
        style = MaterialTheme.typography.titleSmall, color = if (danger) NoCap.Red else NoCap.Text,
    )
}

@Composable
private fun SliderRow(title: String, value: Float, range: ClosedFloatingPointRange<Float>, shown: String, steps: Int = 0, onChange: (Float) -> Unit) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Text(title, Modifier.width(92.dp), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Slider(value, onChange, Modifier.weight(1f), valueRange = range, steps = steps, colors = SliderDefaults.colors(thumbColor = NoCap.Red, activeTrackColor = NoCap.Red))
        Text(shown, Modifier.width(44.dp).padding(start = 6.dp), style = MaterialTheme.typography.labelLarge, color = NoCap.Muted, maxLines = 1)
    }
}

@Composable
private fun Thumb(selected: Boolean, modifier: Modifier, onClick: () -> Unit, content: @Composable () -> Unit) {
    Box(
        modifier.aspectRatio(0.62f).clip(RoundedCornerShape(10.dp))
            .border(if (selected) 2.dp else 1.dp, if (selected) NoCap.Red else NoCap.GlassEdge, RoundedCornerShape(10.dp))
            .clickable(onClick = onClick),
    ) { content() }
}
