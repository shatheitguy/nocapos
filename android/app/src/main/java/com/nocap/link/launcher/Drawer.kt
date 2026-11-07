package com.nocap.link.launcher

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
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
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.boundsInRoot
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.nocap.link.DrawerStyle
import com.nocap.link.Layer
import com.nocap.link.MainActivity
import com.nocap.link.PhoneApp
import com.nocap.link.PhoneApps
import com.nocap.link.Prefs
import com.nocap.link.t
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.SearchField
import com.nocap.link.ui.glass
import kotlinx.coroutines.launch

/** The app drawer, in the style picked in Settings. */
@Composable
fun Drawer(a: MainActivity, ui: HomeUi, page: Int) {
    var q by remember { mutableStateOf("") }
    val visible = a.phoneApps.filter { it.key !in Prefs.hidden }
    val shown = if (q.isBlank()) visible else visible.filter { q.trim().lowercase() in it.label.lowercase() }
    val open: (PhoneApp) -> Unit = { a.launch(it); a.layer = Layer.NONE }
    val menu: (PhoneApp, Rect) -> Unit = { app, r -> ui.menu = Menu(app, null, r, From.DRAWER) }
    Column(
        Modifier.fillMaxSize().background(Color(0xEE07080B)).statusBarsPadding().navigationBarsPadding().padding(horizontal = 14.dp)
            .pointerInput(Unit) {
                // Swipe down from the top closes it.
                var dy = 0f
                detectVerticalDragGestures(onDragStart = { dy = 0f }, onDragEnd = { if (dy > 220f) a.layer = Layer.NONE }) { _, d -> dy += d }
            },
    ) {
        Row(Modifier.fillMaxWidth().padding(top = 12.dp, bottom = 10.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(t("THE ROSTER", "LAUNCHPAD"), style = MaterialTheme.typography.headlineSmall, modifier = Modifier.weight(1f))
            IconButton(onClick = { a.layer = Layer.NONE }) { Icon(Icons.Filled.Close, t("Close", "Close"), tint = NoCap.Text) }
        }
        SearchField(q, { q = it }, t("Search the roster (${visible.size})", "Search ${visible.size} apps"))
        Spacer(Modifier.height(12.dp))
        Box(Modifier.weight(1f).fillMaxWidth()) {
            when {
                q.isNotBlank() -> GridStyle(shown, open, menu)
                Prefs.drawer == DrawerStyle.PAGED -> PagedStyle(shown, open, menu)
                Prefs.drawer == DrawerStyle.GRID -> GridStyle(shown, open, menu)
                Prefs.drawer == DrawerStyle.LIST -> ListStyle(shown, open, menu)
                else -> CategoryStyle(a, shown, open, menu)
            }
        }
    }
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun DrawerTile(app: PhoneApp, open: (PhoneApp) -> Unit, menu: (PhoneApp, Rect) -> Unit) {
    var r by remember { mutableStateOf(Rect.Zero) }
    Box(
        Modifier.onGloballyPositioned { r = it.boundsInRoot() }.clip(RoundedCornerShape(14.dp))
            .combinedClickable(onClick = { open(app) }, onLongClick = { menu(app, r) }),
        contentAlignment = Alignment.Center,
    ) { AppTile(app, size = (Prefs.iconSize - 4).dp) }
}

/** NoCap Launchpad: a paged grid with page dots. */
@Composable
private fun PagedStyle(apps: List<PhoneApp>, open: (PhoneApp) -> Unit, menu: (PhoneApp, Rect) -> Unit) {
    BoxWithConstraints(Modifier.fillMaxSize()) {
        val cols = Prefs.columns
        val rowH = (Prefs.iconSize + if (Prefs.labels) 34 else 16).dp
        val rows = ((maxHeight - 28.dp) / rowH).toInt().coerceAtLeast(1)
        val per = cols * rows
        val pages = apps.chunked(per).ifEmpty { listOf(emptyList()) }
        val pager = rememberPagerState { pages.size }
        val scope = rememberCoroutineScope()
        Column {
            HorizontalPager(pager, Modifier.weight(1f)) { p ->
                Column {
                    pages[p].chunked(cols).forEach { row ->
                        Row(Modifier.fillMaxWidth().height(rowH)) {
                            row.forEach { app -> Box(Modifier.weight(1f), contentAlignment = Alignment.Center) { DrawerTile(app, open, menu) } }
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
                            .background(if (on) NoCap.Red else Color.White.copy(alpha = 0.3f))
                            .clickable { scope.launch { pager.animateScrollToPage(i) } },
                    )
                }
            }
        }
    }
}

@Composable
private fun GridStyle(apps: List<PhoneApp>, open: (PhoneApp) -> Unit, menu: (PhoneApp, Rect) -> Unit) {
    LazyVerticalGrid(GridCells.Fixed(Prefs.columns), contentPadding = PaddingValues(bottom = 24.dp)) {
        items(apps, key = { it.key }) { app -> DrawerTile(app, open, menu) }
    }
}

/** A–Z list with a letter rail for fast scrolling. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
private fun ListStyle(apps: List<PhoneApp>, open: (PhoneApp) -> Unit, menu: (PhoneApp, Rect) -> Unit) {
    fun letter(app: PhoneApp) = app.label.firstOrNull()?.uppercaseChar()?.takeIf { it.isLetter() }?.toString() ?: "#"
    val groups = apps.groupBy(::letter).toSortedMap(compareBy { if (it == "#") "~" else it })
    val index = mutableMapOf<String, Int>()
    var i = 0
    groups.forEach { (l, list) -> index[l] = i; i += 1 + list.size }
    val state = rememberLazyListState()
    val scope = rememberCoroutineScope()
    var active by remember { mutableStateOf<String?>(null) }
    val letters = groups.keys.toList()
    Row(Modifier.fillMaxSize()) {
        LazyColumn(state = state, modifier = Modifier.weight(1f), contentPadding = PaddingValues(bottom = 24.dp)) {
            groups.forEach { (l, list) ->
                stickyHeader(key = "h$l") {
                    Text(l, Modifier.fillMaxWidth().background(Color(0xEE07080B)).padding(vertical = 4.dp, horizontal = 6.dp), style = MaterialTheme.typography.headlineSmall, color = NoCap.Red)
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
        Column(
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

/** Smart folders by kind of app, plus your NoCap server's apps. */
@Composable
private fun CategoryStyle(a: MainActivity, apps: List<PhoneApp>, open: (PhoneApp) -> Unit, menu: (PhoneApp, Rect) -> Unit) {
    val groups = PhoneApps.groupedByCategory(apps)
    var expanded by remember { mutableStateOf<String?>(null) }
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
                Column(Modifier.fillMaxWidth().glass(RoundedCornerShape(18.dp)).clickable { expanded = if (isOpen) null else name }.padding(12.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(name, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                        Text("${list.size}", style = MaterialTheme.typography.labelLarge, color = NoCap.Muted)
                    }
                    Spacer(Modifier.height(8.dp))
                    val showing = if (isOpen) list else list.take(Prefs.columns)
                    showing.chunked(Prefs.columns).forEach { row ->
                        Row(Modifier.fillMaxWidth()) {
                            row.forEach { app -> Box(Modifier.weight(1f), contentAlignment = Alignment.Center) { DrawerTile(app, open, menu) } }
                            repeat(Prefs.columns - row.size) { Spacer(Modifier.weight(1f)) }
                        }
                    }
                    if (!isOpen && list.size > Prefs.columns) Text(t("+${list.size - Prefs.columns} more — tap to open", "${list.size - Prefs.columns} more"), style = MaterialTheme.typography.bodySmall)
                }
            }
        }
    }
}
