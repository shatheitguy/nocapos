package com.nocap.link.drawer

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.unit.sp
import com.nocap.link.data.RosterFolder
import com.nocap.link.data.RosterFolders
import com.nocap.link.ui.GlassLevel
import com.nocap.link.ui.NcDialog
import com.nocap.link.ui.NcIcon
import com.nocap.link.ui.PillButton
import com.nocap.link.ui.RosterSearch
import com.nocap.link.ui.ShellDots
import com.nocap.link.ui.ShellIconButton
import com.nocap.link.ui.canBlur
import com.nocap.link.ui.reducedMotion
import com.nocap.link.ui.shellGlass
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.Icons
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.boundsInRoot
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.nocap.link.data.DrawerSort
import com.nocap.link.data.DrawerStyle
import com.nocap.link.data.Opt
import com.nocap.link.data.PhoneApp
import com.nocap.link.data.PhoneApps
import com.nocap.link.data.t
import com.nocap.link.data.Usage
import com.nocap.link.home.From
import com.nocap.link.home.HomeUi
import com.nocap.link.home.Menu
import com.nocap.link.Layer
import com.nocap.link.MainActivity
import com.nocap.link.ui.glass
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.SearchField
import kotlinx.coroutines.launch

/** Apps for the drawer: not hidden, sorted the way Settings says. */
@Composable
fun drawerApps(a: MainActivity): List<PhoneApp> {
    val ctx = LocalContext.current
    val tick = Usage.tick
    val visible = a.phoneApps.filter { it.key !in Opt.hidden.v }
    return remember(visible, Opt.drawerSort.v, tick) {
        when (Opt.drawerSort.v) {
            DrawerSort.AZ -> visible
            DrawerSort.MOST_USED -> visible.sortedWith(compareByDescending<PhoneApp> { Usage.count(ctx, it.key) }.thenBy { it.label.lowercase() })
            DrawerSort.RECENT -> visible.sortedByDescending { it.installed }
        }
    }
}

fun matches(app: PhoneApp, q: String) = q.isBlank() || q.trim().lowercase().let { s -> s in app.label.lowercase() || s in app.pkg.lowercase() }

/** The app drawer, in the style picked in Settings. The default (the roster) looks like the web OS launcher. */
@Composable
fun Drawer(a: MainActivity, ui: HomeUi, page: Int) {
    var q by remember { mutableStateOf("") }
    val all = drawerApps(a)
    val shown = all.filter { matches(it, q) }
    val open: (PhoneApp) -> Unit = { a.launch(it); a.layer = Layer.NONE }
    val menu: (PhoneApp, Rect) -> Unit = { app, r -> ui.menu = Menu(app, null, r, From.DRAWER) }
    val style = Opt.drawer.v
    val closeOnPull = Modifier.pointerInput(Unit) {
        // Swipe down from the top closes it.
        var dy = 0f
        detectVerticalDragGestures(onDragStart = { dy = 0f }, onDragEnd = { if (dy > 220f) a.layer = Layer.NONE }) { _, d -> dy += d }
    }
    if (style == DrawerStyle.PAGED) {
        Roster(a, ui, all, q, { q = it }, open, closeOnPull)
        return
    }
    Column(
        Modifier.fillMaxSize().background(NoCap.Bg.copy(alpha = Opt.drawerOpacity.v / 100f)).statusBarsPadding().navigationBarsPadding().padding(horizontal = 14.dp)
            .then(closeOnPull),
    ) {
        Row(Modifier.fillMaxWidth().padding(top = 12.dp, bottom = 10.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(if (style == DrawerStyle.CLASSIC) t("ALL APPS", "ALL APPS") else t("THE ROSTER", "APPS"), style = MaterialTheme.typography.headlineSmall, modifier = Modifier.weight(1f))
            ShellIconButton("close", t("Close", "Close")) { a.layer = Layer.NONE }
        }
        RosterSearch(q, { q = it }, t("Search the roster (${all.size})", "Search ${all.size} apps"))
        Spacer(Modifier.height(10.dp))
        Box(Modifier.weight(1f).fillMaxWidth()) {
            when {
                style == DrawerStyle.CLASSIC -> ClassicStyle(a, q, open, menu)
                q.isNotBlank() -> GridStyle(shown, open, menu, recents = emptyList())
                style == DrawerStyle.LIST -> ListStyle(shown, open, menu)
                style == DrawerStyle.CATEGORIES -> CategoryStyle(a, shown, open, menu)
                else -> GridStyle(shown, open, menu, recents = if (Opt.drawerRecents.v) Usage.recent(LocalContext.current, shown, Opt.drawerColumns.v) else emptyList())
            }
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun DrawerTile(app: PhoneApp, open: (PhoneApp) -> Unit, menu: (PhoneApp, Rect) -> Unit, size: Dp = (Opt.iconSize.v - 4).dp, label: Boolean = Opt.labels.v, labelWidth: Dp = size + 22.dp, onWallpaper: Boolean = false) {
    var r by remember { mutableStateOf(Rect.Zero) }
    Box(
        Modifier.onGloballyPositioned { r = it.boundsInRoot() }.clip(RoundedCornerShape(14.dp))
            .combinedClickable(onClick = { open(app) }, onLongClick = { menu(app, r) }),
        contentAlignment = Alignment.Center,
    ) { AppTile(app, size = size, label = label, onWallpaper = onWallpaper, labelWidth = labelWidth) }
}

/** Icon size that fits [cols] columns in [width]. */
private fun fitIcon(width: Dp, cols: Int): Dp = minOf(Opt.iconSize.v.dp - 4.dp, width / cols - 18.dp).coerceAtLeast(34.dp)

/** Something in the roster grid: a folder or an app. */
private sealed interface RosterEntry {
    val key: String
    data class Folder(val folder: RosterFolder, val apps: List<PhoneApp>) : RosterEntry { override val key get() = "folder:" + folder.id }
    data class App(val app: PhoneApp) : RosterEntry { override val key get() = app.key }
}

/**
 * The roster, like the web OS launcher: a dimmed backdrop over the home
 * screen, the "Search the roster" pill, pages of icons with dots, folders
 * first, and the tip line at the bottom. Searching lists every match, folders
 * or not.
 */
@Composable
private fun Roster(
    a: MainActivity, ui: HomeUi, all: List<PhoneApp>, q: String, setQ: (String) -> Unit,
    open: (PhoneApp) -> Unit, closeOnPull: Modifier,
) {
    val menu: (PhoneApp, Rect) -> Unit = { app, r -> ui.menu = Menu(app, null, r, From.DRAWER) }
    val folders = remember(Opt.rosterFolders.v) { RosterFolders.all }
    val byKey = remember(all) { all.associateBy { it.key } }
    // Apps that are gone (uninstalled or hidden) fall out of their folders.
    LaunchedEffect(byKey.keys, folders) {
        if (all.isEmpty()) return@LaunchedEffect
        val pruned = RosterFolders.prune(folders, byKey.keys)
        if (pruned != folders) RosterFolders.save(pruned)
    }
    val entries: List<RosterEntry> = remember(all, folders) {
        val inside = RosterFolders.inFolders(folders)
        folders.mapNotNull { f -> f.keys.mapNotNull { byKey[it] }.takeIf { it.isNotEmpty() }?.let { RosterEntry.Folder(f, it) } } +
            all.filter { it.key !in inside }.map { RosterEntry.App(it) }
    }
    val blurOn = Opt.drawerBlur.v && canBlur()
    val scrim = Color(0xFF080C18).copy(alpha = (Opt.drawerOpacity.v / 100f) * (if (blurOn) 0.42f else 0.8f))
    val motion = !reducedMotion()
    Box(Modifier.fillMaxSize().background(scrim)) {
        Column(
            Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().imePadding().padding(start = 16.dp, end = 16.dp, top = 14.dp, bottom = 10.dp).then(closeOnPull),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.Center) {
                RosterSearch(q, setQ, t("Search the roster", "Search apps"), Modifier.weight(1f, fill = false))
                Spacer(Modifier.width(8.dp))
                ShellIconButton("close", t("Close", "Close"), size = 40.dp, tint = Color.White) { a.layer = Layer.NONE }
            }
            Spacer(Modifier.height(18.dp))
            BoxWithConstraints(Modifier.weight(1f).fillMaxWidth()) {
                val cols = Opt.drawerColumns.v
                val icon = fitIcon(maxWidth, cols)
                val lw = maxWidth / cols - 6.dp
                if (q.isNotBlank()) {
                    val hits = all.filter { matches(it, q) }
                    if (hits.isEmpty()) {
                        Text(t("Nothing in the roster called “$q”.", "No apps match “$q”."), Modifier.fillMaxWidth().padding(top = 24.dp), style = MaterialTheme.typography.bodyMedium, color = Color.White.copy(alpha = 0.7f), textAlign = TextAlign.Center)
                    } else {
                        LazyVerticalGrid(GridCells.Fixed(cols), contentPadding = PaddingValues(bottom = 16.dp)) {
                            items(hits, key = { it.key }) { app -> RosterCell { DrawerTile(app, open, menu, icon, labelWidth = lw, onWallpaper = true) } }
                        }
                    }
                    return@BoxWithConstraints
                }
                val rowH = icon + if (Opt.labels.v) (Opt.labelSize.v + 30).dp else 22.dp
                val rows = (maxHeight / rowH).toInt().coerceIn(2, 7)
                val pages = entries.chunked(cols * rows).ifEmpty { listOf(emptyList()) }
                val pager = rememberPagerState { pages.size }
                val scope = rememberCoroutineScope()
                Column(Modifier.fillMaxSize()) {
                    HorizontalPager(pager, Modifier.weight(1f), key = { it }) { p ->
                        Column(Modifier.fillMaxSize()) {
                            // getOrElse: the list can shrink (uninstall) while the pager still shows an old page.
                            pages.getOrElse(p) { emptyList() }.chunked(cols).forEach { row ->
                                Row(Modifier.fillMaxWidth().height(rowH)) {
                                    row.forEach { e ->
                                        Box(Modifier.weight(1f).fillMaxHeight(), contentAlignment = Alignment.Center) {
                                            when (e) {
                                                is RosterEntry.App -> DrawerTile(e.app, open, menu, icon, labelWidth = lw, onWallpaper = true)
                                                is RosterEntry.Folder -> RosterFolderTile(e, icon, lw) { ui.rosterFolder = e.folder.id }
                                            }
                                        }
                                    }
                                    repeat(cols - row.size) { Spacer(Modifier.weight(1f)) }
                                }
                            }
                        }
                    }
                    if (pages.size > 1) {
                        ShellDots(pages.size, pager.currentPage, Modifier.padding(top = 4.dp)) { i ->
                            scope.launch { if (motion) pager.animateScrollToPage(i) else pager.scrollToPage(i) }
                        }
                    }
                }
            }
            Text(
                t(
                    "Tip: hold an app to pin it, put it in a folder or yeet it · swipe down to close",
                    "Tip: touch and hold an app to pin it, put it in a folder or remove it · swipe down to close",
                ),
                Modifier.fillMaxWidth().padding(top = 8.dp, start = 8.dp, end = 8.dp),
                style = MaterialTheme.typography.bodySmall.copy(fontSize = 12.sp), color = Color.White.copy(alpha = 0.6f), textAlign = TextAlign.Center,
            )
        }
        ui.rosterFolder?.let { id ->
            val f = folders.firstOrNull { it.id == id }
            if (f == null) LaunchedEffect(id) { ui.rosterFolder = null } else RosterFolderView(a, ui, f, byKey, open)
        }
    }
}

@Composable
private fun RosterCell(content: @Composable () -> Unit) {
    Box(Modifier.fillMaxWidth().padding(vertical = 6.dp), contentAlignment = Alignment.Center) { content() }
}

/** A folder in the roster: the web OS folder icon (frosted square, four little icons) and its name. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun RosterFolderTile(e: RosterEntry.Folder, size: Dp, labelWidth: Dp, onOpen: () -> Unit) {
    Box(
        Modifier.clip(RoundedCornerShape(16.dp)).combinedClickable(onClick = onOpen, onLongClick = onOpen),
        contentAlignment = Alignment.Center,
    ) { FolderTile(e.folder.name, e.apps, size = size, labelWidth = labelWidth, webLook = true) }
}

/**
 * An open roster folder, like the web OS folder view: a frosted card with
 * the name (tap to rename) and its apps; "Ungroup" puts them back.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun RosterFolderView(a: MainActivity, ui: HomeUi, f: RosterFolder, byKey: Map<String, PhoneApp>, open: (PhoneApp) -> Unit) {
    var name by remember(f.id) { mutableStateOf(f.name) }
    val apps = f.keys.mapNotNull { byKey[it] }
    fun close() {
        if (name.isNotBlank() && name.trim() != f.name) RosterFolders.save(RosterFolders.rename(RosterFolders.all, f.id, name))
        ui.rosterFolder = null
    }
    BackHandler { close() }
    Box(
        Modifier.fillMaxSize().background(Color(0x99080C18)).clickable(remember { MutableInteractionSource() }, null) { close() }.padding(16.dp),
        contentAlignment = Alignment.Center,
    ) {
        Column(
            Modifier.widthIn(max = 460.dp).fillMaxWidth().shellGlass(RoundedCornerShape(24.dp), GlassLevel.SHEET, edge = Color.White.copy(alpha = 0.18f))
                .clickable(remember { MutableInteractionSource() }, null) {}.padding(start = 18.dp, end = 18.dp, top = 16.dp, bottom = 14.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            BasicTextField(
                name, { name = it.take(RosterFolders.MAX_NAME) }, singleLine = true,
                modifier = Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).padding(vertical = 4.dp, horizontal = 8.dp),
                textStyle = MaterialTheme.typography.titleMedium.copy(fontSize = 20.sp, color = NoCap.Text, textAlign = TextAlign.Center),
                cursorBrush = SolidColor(NoCap.Red),
            )
            Spacer(Modifier.height(10.dp))
            LazyVerticalGrid(GridCells.Fixed(4), Modifier.fillMaxWidth().heightIn(max = 380.dp)) {
                items(apps, key = { it.key }) { app ->
                    var r by remember { mutableStateOf(Rect.Zero) }
                    Box(
                        Modifier.onGloballyPositioned { r = it.boundsInRoot() }.clip(RoundedCornerShape(14.dp))
                            .combinedClickable(onClick = { ui.rosterFolder = null; open(app) }, onLongClick = { ui.menu = Menu(app, null, r, From.ROSTER_FOLDER, rosterFolder = f.id) }),
                        contentAlignment = Alignment.Center,
                    ) { AppTile(app, size = 50.dp, onWallpaper = false, labelWidth = 72.dp) }
                }
            }
            Spacer(Modifier.height(8.dp))
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.Center) {
                PillButton(t("Ungroup", "Remove folder"), {
                    RosterFolders.save(RosterFolders.delete(RosterFolders.all, f.id))
                    ui.rosterFolder = null
                    a.toast = t("Folder's gone. Apps are back in the roster.", "Folder removed. Its apps are back in the list.")
                }, primary = false, icon = "folder")
            }
        }
    }
}

/** "Add to a folder" from an app's menu in the roster: an existing folder, or a new one. */
@Composable
fun RosterFolderPicker(a: MainActivity, ui: HomeUi, app: PhoneApp) {
    val folders = RosterFolders.all
    var name by remember(app.key) { mutableStateOf("") }
    fun done() { ui.folderPick = null }
    NcDialog(
        title = t("Add to a folder", "Add to folder"),
        text = null,
        onDismiss = ::done,
        dismiss = t("Nah", "Cancel") to ::done,
        confirm = t("New folder", "Create folder") to {
            val n = name.trim().ifBlank { t("New folder", "New folder") }
            val list = RosterFolders.create(RosterFolders.all, n, listOf(app.key))
            RosterFolders.save(list)
            ui.rosterFolder = list.last().id
            done()
        },
    ) {
        if (folders.isNotEmpty()) {
            Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(NoCap.Field)) {
                folders.forEach { f ->
                    Row(
                        Modifier.fillMaxWidth().clickable {
                            RosterFolders.save(RosterFolders.add(RosterFolders.all, f.id, app.key))
                            a.toast = t("${app.label} slid into ${f.name}.", "${app.label} added to ${f.name}.")
                            done()
                        }.padding(horizontal = 14.dp, vertical = 12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        NcIcon("folder", NoCap.Muted, 18.dp)
                        Text(f.name, Modifier.padding(start = 10.dp).weight(1f), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text("${f.keys.size}", style = MaterialTheme.typography.labelMedium, color = NoCap.Muted)
                    }
                }
            }
        }
        com.nocap.link.widget.Field(name, { name = it.take(RosterFolders.MAX_NAME) }, t("New folder name", "New folder name"))
    }
}

@Composable
private fun GridStyle(apps: List<PhoneApp>, open: (PhoneApp) -> Unit, menu: (PhoneApp, Rect) -> Unit, recents: List<PhoneApp>) {
    BoxWithConstraints(Modifier.fillMaxSize()) {
        val cols = Opt.drawerColumns.v
        val icon = fitIcon(maxWidth, cols)
        LazyVerticalGrid(GridCells.Fixed(cols), contentPadding = PaddingValues(bottom = 24.dp)) {
            if (recents.isNotEmpty()) {
                item(span = { GridItemSpan(maxLineSpan) }) { Label(t("Lately", "Recent"), Modifier.padding(4.dp), color = NoCap.Red) }
                items(recents, key = { "r:" + it.key }) { app -> DrawerTile(app, open, menu, icon, labelWidth = maxWidth / cols - 4.dp) }
                item(span = { GridItemSpan(maxLineSpan) }) { Box(Modifier.fillMaxWidth().padding(vertical = 8.dp).height(1.dp).background(NoCap.GlassEdge)) }
            }
            items(apps, key = { it.key }) { app -> DrawerTile(app, open, menu, icon, labelWidth = maxWidth / cols - 4.dp) }
        }
    }
}

/** A–Z list with a letter rail for fast scrolling. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ListStyle(apps: List<PhoneApp>, open: (PhoneApp) -> Unit, menu: (PhoneApp, Rect) -> Unit) {
    LetterList(apps.sortedBy { it.label.lowercase() }, open, menu, header = null)
}

private fun letter(app: PhoneApp) = app.label.firstOrNull()?.uppercaseChar()?.takeIf { it.isLetter() }?.toString() ?: "#"

/** Shared A–Z list: sticky letters, a rail, and an optional header (recents). */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun LetterList(apps: List<PhoneApp>, open: (PhoneApp) -> Unit, menu: (PhoneApp, Rect) -> Unit, header: (@Composable () -> Unit)?) {
    val groups = apps.groupBy(::letter).toSortedMap(compareBy { if (it == "#") "~" else it })
    val index = mutableMapOf<String, Int>()
    var i = if (header != null) 1 else 0
    groups.forEach { (l, list) -> index[l] = i; i += 1 + list.size }
    val state = rememberLazyListState()
    val scope = rememberCoroutineScope()
    var active by remember { mutableStateOf<String?>(null) }
    val letters = groups.keys.toList()
    val sheet = NoCap.Sheet
    Row(Modifier.fillMaxSize()) {
        LazyColumn(state = state, modifier = Modifier.weight(1f), contentPadding = PaddingValues(bottom = 24.dp)) {
            if (header != null) item(key = "header") { header() }
            groups.forEach { (l, list) ->
                stickyHeader(key = "h$l") {
                    Text(l, Modifier.fillMaxWidth().background(sheet).padding(vertical = 4.dp, horizontal = 6.dp), style = MaterialTheme.typography.headlineSmall, color = NoCap.Red)
                }
                items(list, key = { it.key }) { app ->
                    var r by remember { mutableStateOf(Rect.Zero) }
                    Row(
                        Modifier.fillMaxWidth().onGloballyPositioned { r = it.boundsInRoot() }.clip(RoundedCornerShape(12.dp))
                            .combinedClickable(onClick = { open(app) }, onLongClick = { menu(app, r) }).padding(horizontal = 6.dp, vertical = 6.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        AppTile(app, size = 40.dp, label = false)
                        Text(app.label, Modifier.padding(start = 14.dp), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
        }
        if (letters.size > 1) Column(
            Modifier.width(26.dp).fillMaxHeight().padding(vertical = 8.dp).glass(RoundedCornerShape(13.dp))
                .pointerInput(letters) {
                    fun pick(y: Float) {
                        if (letters.isEmpty()) return
                        val l = letters[((y / size.height) * letters.size).toInt().coerceIn(0, letters.size - 1)]
                        if (l != active) { active = l; scope.launch { state.scrollToItem(index[l] ?: 0) } }
                    }
                    detectVerticalDragGestures(onDragEnd = { active = null }, onDragCancel = { active = null }) { ch, _ -> pick(ch.position.y) }
                }
                .pointerInput(letters) { detectTapGestures { o -> if (letters.isNotEmpty()) { val l = letters[((o.y / size.height) * letters.size).toInt().coerceIn(0, letters.size - 1)]; scope.launch { state.scrollToItem(index[l] ?: 0) } } } },
            verticalArrangement = Arrangement.SpaceEvenly, horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            letters.forEach { l ->
                Text(l, style = MaterialTheme.typography.labelSmall, color = if (l == active) NoCap.Red else NoCap.Muted, textAlign = TextAlign.Center)
            }
        }
    }
}

/**
 * Classic drawer: tabs (All / Fresh installs / Hidden), a row of suggested
 * apps, then everything A–Z with a letter rail.
 */
@Composable
private fun ClassicStyle(a: MainActivity, q: String, open: (PhoneApp) -> Unit, menu: (PhoneApp, Rect) -> Unit) {
    val ctx = LocalContext.current
    var tab by remember { mutableStateOf(0) }
    val visible = a.phoneApps.filter { it.key !in Opt.hidden.v && matches(it, q) }
    Column(Modifier.fillMaxSize()) {
        Row(Modifier.fillMaxWidth().padding(bottom = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            listOf(t("All", "All"), t("Fresh", "Recently installed"), t("Hidden", "Hidden")).forEachIndexed { i, s ->
                Text(
                    s, Modifier.clip(RoundedCornerShape(50)).background(if (tab == i) NoCap.Red else NoCap.Field).clickable { tab = i }.padding(horizontal = 14.dp, vertical = 7.dp),
                    style = MaterialTheme.typography.labelLarge, color = if (tab == i) Color.White else NoCap.Text, maxLines = 1,
                )
            }
        }
        when (tab) {
            1 -> GridStyle(visible.sortedByDescending { it.installed }.take(40), open, menu, emptyList())
            2 -> {
                val hidden = a.phoneApps.filter { it.key in Opt.hidden.v && matches(it, q) }
                if (hidden.isEmpty()) Text(t("Nothing hidden. Long-press an app → Hide.", "No hidden apps. Long-press an app and choose Hide."), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
                else GridStyle(hidden, open, menu, emptyList())
            }
            else -> {
                val suggested = if (Opt.drawerRecents.v && q.isBlank()) Usage.recent(ctx, visible, Opt.drawerColumns.v) else emptyList()
                LetterList(visible.sortedBy { it.label.lowercase() }, open, menu, header = if (suggested.isEmpty()) null else ({
                    Column(Modifier.fillMaxWidth().padding(bottom = 8.dp).glass(RoundedCornerShape(18.dp)).padding(10.dp)) {
                        Label(t("Suggested", "Suggested"), color = NoCap.Red)
                        Spacer(Modifier.height(6.dp))
                        BoxWithConstraints {
                            val cols = suggested.size.coerceAtLeast(1)
                            val mw = maxWidth
                            Row(Modifier.fillMaxWidth()) {
                                suggested.forEach { app -> Box(Modifier.weight(1f), contentAlignment = Alignment.Center) { DrawerTile(app, open, menu, fitIcon(mw, cols), labelWidth = mw / cols - 4.dp) } }
                            }
                        }
                    }
                }))
            }
        }
    }
}

/** Smart folders by kind of app, plus your NoCap server's apps. */
@Composable
private fun CategoryStyle(a: MainActivity, apps: List<PhoneApp>, open: (PhoneApp) -> Unit, menu: (PhoneApp, Rect) -> Unit) {
    val groups = PhoneApps.groupedByCategory(apps)
    var expanded by remember { mutableStateOf<String?>(null) }
    val cols = Opt.drawerColumns.v
    LazyColumn(contentPadding = PaddingValues(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        if (a.serverApps.any { it.id != "nocapos" }) {
            item(key = "nocap") {
                Column(Modifier.fillMaxWidth().glass(RoundedCornerShape(18.dp), edge = NoCap.Red.copy(alpha = 0.4f)).padding(12.dp)) {
                    Label(t("On your NoCap server", "NoCap server apps"), color = NoCap.Red)
                    Spacer(Modifier.height(8.dp))
                    Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        a.serverApps.filter { it.id != "nocapos" }.take(5).forEach { s ->
                            Column(Modifier.width(60.dp).clickable { a.openServerApp(s) }, horizontalAlignment = Alignment.CenterHorizontally) {
                                a.serverIcons[s.id]?.let { Image(it, null, Modifier.size(40.dp)) } ?: Text(s.name.take(1), style = MaterialTheme.typography.headlineSmall, color = NoCap.Red)
                                Text(s.name, style = MaterialTheme.typography.labelSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            }
                        }
                    }
                }
            }
        }
        groups.forEach { (name, list) ->
            item(key = name) {
                val isOpen = expanded == name
                BoxWithConstraints {
                    val icon = fitIcon(maxWidth - 24.dp, cols)
                    val lw = (maxWidth - 24.dp) / cols - 4.dp
                    Column(Modifier.fillMaxWidth().glass(RoundedCornerShape(18.dp)).clickable { expanded = if (isOpen) null else name }.padding(12.dp)) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Text(name, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                            Text("${list.size}", style = MaterialTheme.typography.labelLarge, color = NoCap.Muted)
                        }
                        Spacer(Modifier.height(8.dp))
                        val showing = if (isOpen) list else list.take(cols)
                        showing.chunked(cols).forEach { row ->
                            Row(Modifier.fillMaxWidth()) {
                                row.forEach { app -> Box(Modifier.weight(1f), contentAlignment = Alignment.Center) { DrawerTile(app, open, menu, icon, labelWidth = lw) } }
                                repeat(cols - row.size) { Spacer(Modifier.weight(1f)) }
                            }
                        }
                        if (!isOpen && list.size > cols) Text(t("+${list.size - cols} more — tap to open", "${list.size - cols} more"), style = MaterialTheme.typography.bodySmall)
                    }
                }
            }
        }
    }
}

/**
 * The Stash (app groups): the last home page when every app lives on the home
 * pages. Apps grouped into smart folders, with search on top.
 */
@Composable
fun AppLibrary(a: MainActivity, ui: HomeUi) {
    var q by remember { mutableStateOf("") }
    var openGroup by remember { mutableStateOf<String?>(null) }
    val apps = a.phoneApps.filter { it.key !in Opt.hidden.v }
    val open: (PhoneApp) -> Unit = { a.launch(it) }
    val menu: (PhoneApp, Rect) -> Unit = { app, r -> ui.menu = Menu(app, null, r, From.DRAWER) }
    Column(Modifier.fillMaxSize().padding(horizontal = 6.dp)) {
        Text(t("THE STASH", "APP GROUPS"), Modifier.padding(start = 4.dp, bottom = 8.dp), style = MaterialTheme.typography.headlineSmall, color = androidx.compose.ui.graphics.Color.White)
        SearchField(q, { q = it }, t("Search the library", "Search apps"))
        Spacer(Modifier.height(10.dp))
        if (q.isNotBlank()) {
            Box(Modifier.weight(1f).glass(RoundedCornerShape(20.dp)).padding(8.dp)) { GridStyle(apps.filter { matches(it, q) }, open, menu, emptyList()) }
            return@Column
        }
        val groups = PhoneApps.groupedByCategory(apps)
        val sel = openGroup
        if (sel != null) {
            val list = groups.firstOrNull { it.first == sel }?.second.orEmpty()
            Column(Modifier.weight(1f).glass(RoundedCornerShape(20.dp)).padding(10.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(sel, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                    Text(t("Back", "Back"), Modifier.clip(RoundedCornerShape(10.dp)).clickable { openGroup = null }.padding(8.dp), style = MaterialTheme.typography.labelLarge, color = NoCap.Red)
                }
                GridStyle(list, open, menu, emptyList())
            }
            return@Column
        }
        LazyVerticalGrid(GridCells.Adaptive(150.dp), Modifier.weight(1f), contentPadding = PaddingValues(bottom = 16.dp), horizontalArrangement = Arrangement.spacedBy(10.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
            items(groups, key = { it.first }) { (name, list) ->
                Column(Modifier.glass(RoundedCornerShape(22.dp)).padding(10.dp)) {
                    BoxWithConstraints(Modifier.fillMaxWidth().aspectRatio(1f)) {
                        val cell = maxWidth / 2
                        val icon = (cell - 14.dp).coerceIn(16.dp, 56.dp)
                        Column {
                            list.take(4).chunked(2).forEachIndexed { r, row ->
                                Row {
                                    row.forEachIndexed { c, app ->
                                        val more = r == 1 && c == 1 && list.size > 4
                                        Box(Modifier.size(cell), contentAlignment = Alignment.Center) {
                                            if (more) {
                                                Box(Modifier.size(icon).clip(RoundedCornerShape(icon * 0.24f)).background(NoCap.Field).clickable { openGroup = name }, contentAlignment = Alignment.Center) {
                                                    Text("+${list.size - 3}", style = MaterialTheme.typography.labelLarge)
                                                }
                                            } else {
                                                DrawerTile(app, open, menu, icon, label = false)
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                    Text(name, Modifier.fillMaxWidth().clickable { openGroup = name }.padding(top = 6.dp), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis, textAlign = TextAlign.Center)
                }
            }
        }
    }
}
