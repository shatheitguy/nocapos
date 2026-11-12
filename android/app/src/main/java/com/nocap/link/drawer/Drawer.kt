package com.nocap.link.drawer

import androidx.compose.foundation.background
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
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.Dp
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

/** The app drawer, in the style picked in Settings. */
@Composable
fun Drawer(a: MainActivity, ui: HomeUi, page: Int) {
    var q by remember { mutableStateOf("") }
    val all = drawerApps(a)
    val shown = all.filter { matches(it, q) }
    val open: (PhoneApp) -> Unit = { a.launch(it); a.layer = Layer.NONE }
    val menu: (PhoneApp, Rect) -> Unit = { app, r -> ui.menu = Menu(app, null, r, From.DRAWER) }
    val style = Opt.drawer.v
    Column(
        Modifier.fillMaxSize().background(NoCap.Bg.copy(alpha = Opt.drawerOpacity.v / 100f)).statusBarsPadding().navigationBarsPadding().padding(horizontal = 14.dp)
            .pointerInput(Unit) {
                // Swipe down from the top closes it.
                var dy = 0f
                detectVerticalDragGestures(onDragStart = { dy = 0f }, onDragEnd = { if (dy > 220f) a.layer = Layer.NONE }) { _, d -> dy += d }
            },
    ) {
        Row(Modifier.fillMaxWidth().padding(top = 12.dp, bottom = 10.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(if (style == DrawerStyle.CLASSIC) t("ALL APPS", "ALL APPS") else t("THE ROSTER", "LAUNCHPAD"), style = MaterialTheme.typography.headlineSmall, modifier = Modifier.weight(1f))
            IconButton(onClick = { a.layer = Layer.NONE }) { Icon(Icons.Filled.Close, t("Close", "Close"), tint = NoCap.Text) }
        }
        SearchField(q, { q = it }, t("Search the roster (${all.size})", "Search ${all.size} apps"))
        Spacer(Modifier.height(10.dp))
        Box(Modifier.weight(1f).fillMaxWidth()) {
            when {
                style == DrawerStyle.CLASSIC -> ClassicStyle(a, q, open, menu)
                q.isNotBlank() -> GridStyle(shown, open, menu, recents = emptyList())
                style == DrawerStyle.PAGED -> PagedStyle(shown, open, menu)
                style == DrawerStyle.LIST -> ListStyle(shown, open, menu)
                style == DrawerStyle.CATEGORIES -> CategoryStyle(a, shown, open, menu)
                else -> GridStyle(shown, open, menu, recents = if (Opt.drawerRecents.v) Usage.recent(LocalContext.current, shown, Opt.drawerColumns.v) else emptyList())
            }
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun DrawerTile(app: PhoneApp, open: (PhoneApp) -> Unit, menu: (PhoneApp, Rect) -> Unit, size: Dp = (Opt.iconSize.v - 4).dp, label: Boolean = Opt.labels.v, labelWidth: Dp = size + 22.dp) {
    var r by remember { mutableStateOf(Rect.Zero) }
    Box(
        Modifier.onGloballyPositioned { r = it.boundsInRoot() }.clip(RoundedCornerShape(14.dp))
            .combinedClickable(onClick = { open(app) }, onLongClick = { menu(app, r) }),
        contentAlignment = Alignment.Center,
    ) { AppTile(app, size = size, label = label, onWallpaper = false, labelWidth = labelWidth) }
}

/** Icon size that fits [cols] columns in [width]. */
private fun fitIcon(width: Dp, cols: Int): Dp = minOf(Opt.iconSize.v.dp - 4.dp, width / cols - 18.dp).coerceAtLeast(34.dp)

/** NoCap Launchpad: a paged grid with page dots. */
@Composable
private fun PagedStyle(apps: List<PhoneApp>, open: (PhoneApp) -> Unit, menu: (PhoneApp, Rect) -> Unit) {
    BoxWithConstraints(Modifier.fillMaxSize()) {
        val cols = Opt.drawerColumns.v
        val icon = fitIcon(maxWidth, cols)
        val lw = maxWidth / cols - 4.dp
        val rowH = icon + if (Opt.labels.v) (Opt.labelSize.v + 24).dp else 16.dp
        val rows = ((maxHeight - 28.dp) / rowH).toInt().coerceAtLeast(1)
        val per = cols * rows
        val pages = apps.chunked(per).ifEmpty { listOf(emptyList()) }
        val pager = rememberPagerState { pages.size }
        val scope = rememberCoroutineScope()
        Column {
            HorizontalPager(pager, Modifier.weight(1f)) { p ->
                Column {
                    // getOrElse: the list can shrink (search, uninstall) while the pager still shows an old page.
                    pages.getOrElse(p) { emptyList() }.chunked(cols).forEach { row ->
                        Row(Modifier.fillMaxWidth().height(rowH)) {
                            row.forEach { app -> Box(Modifier.weight(1f), contentAlignment = Alignment.Center) { DrawerTile(app, open, menu, icon, labelWidth = lw) } }
                            repeat(cols - row.size) { Spacer(Modifier.weight(1f)) }
                        }
                    }
                }
            }
            Row(Modifier.fillMaxWidth().height(28.dp), horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
                repeat(pages.size) { i ->
                    val on = i == pager.currentPage
                    Box(
                        Modifier.padding(horizontal = 4.dp).size(if (on) 9.dp else 7.dp).clip(RoundedCornerShape(50))
                            .background(if (on) NoCap.Red else NoCap.Muted.copy(alpha = 0.5f))
                            .clickable { scope.launch { pager.animateScrollToPage(i) } },
                    )
                }
            }
        }
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
