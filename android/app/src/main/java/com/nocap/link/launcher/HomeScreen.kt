package com.nocap.link.launcher

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.PagerState
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Search
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.input.pointer.changedToUp
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.boundsInRoot
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import com.nocap.link.Layer
import com.nocap.link.MainActivity
import com.nocap.link.PhoneApp
import com.nocap.link.PhoneApps
import com.nocap.link.Prefs
import com.nocap.link.R
import com.nocap.link.ServerState
import com.nocap.link.SwipeDown
import com.nocap.link.WallpaperLayer
import com.nocap.link.system.Phone
import com.nocap.link.t
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.Toast
import com.nocap.link.ui.glass
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlin.math.abs
import kotlin.math.roundToInt

/** Something being dragged around the home screen. */
data class Drag(val item: Item?, val dockKey: String?, val pos: Offset, val grab: Offset, val start: Offset, val moved: Boolean = false)

/** What a long-press menu is about. */
data class Menu(val app: PhoneApp?, val item: Item?, val anchor: Rect, val from: From, val folder: FolderItem? = null)

enum class From { HOME, DOCK, DRAWER, FOLDER, SEARCH }

/** What's under a finger. */
private sealed interface Hit {
    data class OnItem(val page: Int, val item: Item) : Hit
    data class Empty(val page: Int, val x: Int, val y: Int) : Hit
    data class Dock(val index: Int, val key: String) : Hit
    data class Button(val id: String) : Hit
    data object None : Hit
}

/** Home-screen UI state that the gesture code and the overlays share. */
class HomeUi {
    var drag by mutableStateOf<Drag?>(null)
    var menu by mutableStateOf<Menu?>(null)
    var folder by mutableStateOf<Pair<Int, FolderItem>?>(null)
    var editAt by mutableStateOf<Offset?>(null)
    var widgetMenu by mutableStateOf<Pair<Int, Item>?>(null)
    var moving by mutableStateOf<Item?>(null)
    var gridRect by mutableStateOf(Rect.Zero)
    var dockRect by mutableStateOf(Rect.Zero)
    var removeRect by mutableStateOf(Rect.Zero)
    val dockSlots = mutableMapOf<String, Rect>()
    val itemRects = mutableMapOf<String, Rect>()
    var cellPx = Offset(1f, 1f)
}

@Composable
fun HomeScreen(a: MainActivity) {
    val ui = remember { HomeUi() }
    val layout = Home.layout
    val pager = rememberPagerState { Home.layout.pages.size }
    val scope = rememberCoroutineScope()
    val haptics = LocalHapticFeedback.current
    val density = LocalDensity.current
    val byKey = remember(a.phoneApps) { a.phoneApps.associateBy { it.key } }

    BackHandler(a.layer != Layer.NONE || ui.folder != null || ui.menu != null || ui.editAt != null || ui.widgetMenu != null || ui.moving != null) {
        when {
            ui.menu != null -> ui.menu = null
            ui.widgetMenu != null -> ui.widgetMenu = null
            ui.moving != null -> ui.moving = null
            ui.editAt != null -> ui.editAt = null
            ui.folder != null -> ui.folder = null
            else -> a.layer = Layer.NONE
        }
    }
    LaunchedEffect(a.toast) { if (a.toast != null) { delay(3200); a.toast = null } }
    LaunchedEffect(Prefs.columns, Prefs.rows) { if (Home.loaded) Home.refit() }

    // Flip pages while dragging near an edge.
    val edge = ui.drag?.let { d ->
        val w = ui.gridRect.width
        val zone = with(density) { 28.dp.toPx() }
        when {
            !d.moved || w <= 0f -> 0
            d.pos.x < ui.gridRect.left + zone -> -1
            d.pos.x > ui.gridRect.right - zone -> 1
            else -> 0
        }
    } ?: 0
    LaunchedEffect(edge) {
        while (edge != 0) {
            delay(650)
            val target = pager.currentPage + edge
            if (target < 0) break
            if (target >= Home.layout.pages.size) Home.addPage()
            pager.animateScrollToPage(target)
        }
    }

    fun hit(pos: Offset): Hit {
        if (ui.dockRect.contains(pos)) {
            ui.dockSlots.entries.firstOrNull { it.value.contains(pos) }?.let { (k, _) ->
                return when {
                    k.startsWith("btn:") -> Hit.Button(k.removePrefix("btn:"))
                    else -> Hit.Dock(Home.layout.dock.indexOf(k), k)
                }
            }
            return Hit.None
        }
        if (ui.gridRect.contains(pos)) {
            val page = pager.currentPage
            val cx = ((pos.x - ui.gridRect.left) / ui.cellPx.x).toInt().coerceIn(0, Prefs.columns - 1)
            val cy = ((pos.y - ui.gridRect.top) / ui.cellPx.y).toInt().coerceIn(0, Prefs.rows - 1)
            val item = Home.itemAt(page, cx, cy)
            return if (item != null) Hit.OnItem(page, item) else Hit.Empty(page, cx, cy)
        }
        return Hit.None
    }

    fun tap(h: Hit) {
        val mv = ui.moving
        if (mv != null) {
            if (h is Hit.Empty) {
                val c = Cell(h.x, h.y, mv.cell.w, mv.cell.h)
                if (Home.isFree(h.page, c, mv.id)) Home.move(mv.id, h.page, c) else a.toast = t("Doesn't fit there. Try another spot.", "It doesn't fit there.")
            }
            ui.moving = null
            return
        }
        when (h) {
            is Hit.OnItem -> when (val it = h.item) {
                is AppItem -> byKey[it.key]?.let(a::launch)
                is FolderItem -> ui.folder = h.page to it
                else -> {}
            }
            is Hit.Dock -> byKey[h.key]?.let(a::launch)
            is Hit.Button -> when (h.id) {
                "roster" -> a.layer = Layer.DRAWER
                "hub" -> a.layer = Layer.HUB
            }
            else -> {}
        }
    }

    fun longPress(h: Hit, pos: Offset): Boolean {
        haptics.performHapticFeedback(HapticFeedbackType.LongPress)
        when (h) {
            is Hit.OnItem -> {
                val r = ui.itemRects[h.item.id] ?: Rect(pos, pos)
                when (val it = h.item) {
                    is AppItem -> ui.menu = Menu(byKey[it.key], it, r, From.HOME)
                    is WidgetItem -> { ui.widgetMenu = h.page to it; return false }
                    else -> ui.widgetMenu = h.page to it
                }
                ui.drag = Drag(h.item, null, pos, pos - r.topLeft, pos)
                return true
            }
            is Hit.Dock -> {
                val r = ui.dockSlots[h.key] ?: Rect(pos, pos)
                ui.menu = Menu(byKey[h.key], null, r, From.DOCK)
                ui.drag = Drag(null, h.key, pos, pos - r.topLeft, pos)
                return true
            }
            is Hit.Empty -> { ui.editAt = pos; return false }
            Hit.None -> return false
            is Hit.Button -> return false
        }
    }

    fun dragTo(pos: Offset) {
        val d = ui.drag ?: return
        val moved = d.moved || (pos - d.start).getDistance() > with(density) { 12.dp.toPx() }
        if (moved && !d.moved) { ui.menu = null; ui.widgetMenu = null }
        ui.drag = d.copy(pos = pos, moved = moved)
    }

    fun drop(pos: Offset) {
        val d = ui.drag ?: return
        ui.drag = null
        if (!d.moved) return
        val item = d.item
        val page = pager.currentPage
        val dockKey = d.dockKey ?: (item as? AppItem)?.key
        when {
            item != null && ui.removeRect.contains(pos) -> {
                if (item is WidgetItem) Widgets.delete(a, item.widgetId)
                Home.remove(item.id)
                a.toast = t("Yeeted from the home screen.", "Removed from the home screen.")
            }
            d.dockKey != null && ui.removeRect.contains(pos) -> Home.setDock(Home.layout.dock - d.dockKey)
            ui.dockRect.contains(pos) && dockKey != null && (item == null || item is AppItem) -> {
                val dock = Home.layout.dock.toMutableList()
                dock.remove(dockKey)
                if (dock.size >= 5) { a.toast = t("Dock's full — unpin something first.", "The dock is full."); return }
                val idx = ui.dockSlots.entries.filter { !it.key.startsWith("btn:") && it.key != dockKey }
                    .count { it.value.center.x < pos.x }.coerceIn(0, dock.size)
                dock.add(idx, dockKey)
                Home.setDock(dock)
                if (item != null) Home.remove(item.id)
            }
            ui.gridRect.contains(pos) -> {
                val w = item?.cell?.w ?: 1
                val h = item?.cell?.h ?: 1
                val topLeft = pos - d.grab + Offset(ui.cellPx.x / 2f, ui.cellPx.y / 2f)
                val cx = ((topLeft.x - ui.gridRect.left) / ui.cellPx.x).toInt().coerceIn(0, (Prefs.columns - w).coerceAtLeast(0))
                val cy = ((topLeft.y - ui.gridRect.top) / ui.cellPx.y).toInt().coerceIn(0, (Prefs.rows - h).coerceAtLeast(0))
                val under = run {
                    val px = ((pos.x - ui.gridRect.left) / ui.cellPx.x).toInt()
                    val py = ((pos.y - ui.gridRect.top) / ui.cellPx.y).toInt()
                    Home.itemAt(page, px, py)
                }
                val dragged: Item? = item ?: d.dockKey?.let { AppItem(newId(), Cell(cx, cy), it) }
                if (dragged == null) return
                if (under != null && under.id != dragged.id && (under is AppItem || under is FolderItem) && dragged is AppItem) {
                    Home.mergeInto(dragged, under)
                    if (d.dockKey != null) Home.setDock(Home.layout.dock - d.dockKey)
                } else if (Home.isFree(page, Cell(cx, cy, w, h), item?.id)) {
                    if (item != null) Home.move(item.id, page, Cell(cx, cy, w, h))
                    else {
                        Home.setDock(Home.layout.dock - d.dockKey!!)
                        Home.set(Home.layout.copy(pages = Home.layout.pages.mapIndexed { i, p -> if (i == page) Page(p.items + dragged.at(Cell(cx, cy))) else p }))
                    }
                }
            }
        }
    }

    Box(Modifier.fillMaxSize()) {
        WallpaperLayer()
        Box(
            Modifier.fillMaxSize().pointerInput(byKey) {
                val slop = viewConfiguration.touchSlop
                val swipe = 70.dp.toPx()
                awaitEachGesture {
                    val down = awaitFirstDown(requireUnconsumed = false)
                    val start = down.position
                    val h = hit(start)
                    if (h is Hit.OnItem && h.item is WidgetItem) return@awaitEachGesture // the widget handles its own touches
                    var upConsumed = false
                    var moved = false
                    val timedOut = withTimeoutOrNull(viewConfiguration.longPressTimeoutMillis) {
                        var done = false
                        while (!done) {
                            val ev = awaitPointerEvent()
                            val ch = ev.changes.firstOrNull { it.id == down.id }
                            if (ch == null) {
                                done = true
                            } else if (ch.changedToUp()) {
                                upConsumed = ch.isConsumed
                                done = true
                            } else if ((ch.position - start).getDistance() > slop) {
                                moved = true
                                done = true
                            }
                        }
                        true
                    } == null
                    if (timedOut) {
                        if (!longPress(h, start)) {
                            // Wait for the finger to lift so nothing else reacts.
                            while (true) {
                                val ev = awaitPointerEvent()
                                if (ev.changes.all { !it.pressed }) break
                            }
                            return@awaitEachGesture
                        }
                        while (true) {
                            val ev = awaitPointerEvent()
                            val ch = ev.changes.firstOrNull { it.id == down.id } ?: break
                            if (ch.changedToUp() || !ch.pressed) { drop(ch.position); break }
                            ch.consume()
                            dragTo(ch.position)
                        }
                        if (ui.drag != null) ui.drag = null
                        return@awaitEachGesture
                    }
                    if (moved) {
                        // A swipe: up = app drawer, down = notifications (or search).
                        var last = start
                        var horizontal = false
                        while (true) {
                            val ev = awaitPointerEvent()
                            val ch = ev.changes.firstOrNull { it.id == down.id } ?: break
                            if (ch.isConsumed) horizontal = true
                            last = ch.position
                            if (ch.changedToUp() || !ch.pressed) break
                        }
                        val dx = last.x - start.x
                        val dy = last.y - start.y
                        if (!horizontal && abs(dy) > swipe && abs(dy) > abs(dx) * 1.4f && a.layer == Layer.NONE && ui.drag == null) {
                            if (dy < 0) a.layer = Layer.DRAWER
                            else when (Prefs.swipeDown) {
                                SwipeDown.NOTIFICATIONS -> if (!Phone.expandNotifications(a)) a.layer = Layer.SEARCH
                                SwipeDown.SEARCH -> a.layer = Layer.SEARCH
                                SwipeDown.NOTHING -> {}
                            }
                        }
                        return@awaitEachGesture
                    }
                    if (upConsumed) return@awaitEachGesture
                    val empty = h is Hit.Empty || h is Hit.None
                    if (empty && Prefs.doubleTapLock && ui.moving == null) {
                        val second = withTimeoutOrNull(viewConfiguration.doubleTapTimeoutMillis) { awaitFirstDown(requireUnconsumed = false) }
                        if (second != null && (second.position - start).getDistance() < 60.dp.toPx()) {
                            if (!Phone.lock(a)) a.toast = t("Turn on the lock helper in Settings → Gestures first.", "Enable the lock helper in Settings → Gestures first.")
                            return@awaitEachGesture
                        }
                    }
                    tap(h)
                }
            },
        ) {
            Column(Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding()) {
                TopStrip(a, ui)
                BoxWithConstraints(Modifier.weight(1f).fillMaxWidth().padding(horizontal = 8.dp)) {
                    val cw = maxWidth / Prefs.columns
                    val ch = maxHeight / Prefs.rows
                    a.cellW = cw.value
                    a.cellH = ch.value
                    ui.cellPx = with(density) { Offset(cw.toPx(), ch.toPx()) }
                    HorizontalPager(
                        state = pager, userScrollEnabled = ui.drag == null, beyondViewportPageCount = 1,
                        modifier = Modifier.fillMaxSize().onGloballyPositioned { ui.gridRect = it.boundsInRoot() },
                        key = { it },
                    ) { p ->
                        PageGrid(a, ui, p, byKey, cw, ch)
                    }
                }
                PageDots(pager, layout.pages.size)
                Dock(a, ui, byKey)
                Spacer(Modifier.height(8.dp))
            }
        }

        // Remove target while dragging.
        val d = ui.drag
        if (d != null && d.moved) {
            val over = ui.removeRect.contains(d.pos)
            Row(
                Modifier.align(Alignment.TopCenter).statusBarsPadding().padding(top = 6.dp)
                    .onGloballyPositioned { ui.removeRect = it.boundsInRoot().inflate(24f) }
                    .glass(RoundedCornerShape(50), strong = true, edge = if (over) NoCap.Red else NoCap.GlassEdge)
                    .padding(horizontal = 18.dp, vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(Icons.Filled.Delete, null, tint = if (over) NoCap.Red else NoCap.Text, modifier = Modifier.size(18.dp))
                Text(t("Yeet", "Remove"), Modifier.padding(start = 6.dp), style = MaterialTheme.typography.labelLarge, color = if (over) NoCap.Red else NoCap.Text)
            }
            DragGhost(d, byKey, ui)
        }
        if (ui.moving != null) {
            Box(Modifier.align(Alignment.TopCenter).statusBarsPadding().padding(top = 6.dp).glass(RoundedCornerShape(50), strong = true).padding(horizontal = 18.dp, vertical = 10.dp)) {
                Text(t("Tap an empty spot to move it there", "Tap an empty spot to move it"), style = MaterialTheme.typography.labelLarge)
            }
        }

        ui.folder?.let { (page, f) -> FolderWindow(a, ui, page, f, byKey) }
        ui.widgetMenu?.let { (page, item) -> WidgetMenu(a, ui, page, item) }
        ui.editAt?.let { at -> EditMenu(a, ui, at, pager.currentPage) { scope.launch { pager.animateScrollToPage(it) } } }

        AnimatedVisibility(a.layer == Layer.DRAWER, enter = fadeIn() + slideInVertically { it / 5 }, exit = fadeOut() + slideOutVertically { it / 5 }) {
            Drawer(a, ui, pager.currentPage)
        }
        AnimatedVisibility(a.layer == Layer.SEARCH, enter = fadeIn() + slideInVertically { -it / 8 }, exit = fadeOut()) {
            SearchLayer(a, ui)
        }
        AnimatedVisibility(a.layer == Layer.HUB, enter = fadeIn() + slideInVertically { it / 6 }, exit = fadeOut() + slideOutVertically { it / 6 }) {
            com.nocap.link.nocap.Hub(a)
        }
        AnimatedVisibility(a.layer == Layer.SETTINGS, enter = fadeIn(), exit = fadeOut()) {
            SettingsScreen(a)
        }
        if (a.layer == Layer.WIDGETS) {
            WidgetPicker(a.cellW, a.cellH, onNoCap = { a.addNoCapWidget(it, pager.currentPage) }, onPick = { a.addWidget(it, pager.currentPage) }, onClose = { a.layer = Layer.NONE })
        }
        ui.menu?.let { m -> AppMenu(a, ui, m, page = pager.currentPage) }
        a.busy?.let { msg ->
            Row(
                Modifier.align(Alignment.Center).glass(RoundedCornerShape(18.dp), strong = true).padding(18.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                CircularProgressIndicator(Modifier.size(20.dp), color = NoCap.Red, strokeWidth = 2.dp)
                Text(msg, Modifier.padding(start = 12.dp), style = MaterialTheme.typography.titleSmall)
            }
        }
        a.toast?.let { Toast(it, Modifier.align(Alignment.BottomCenter).navigationBarsPadding().padding(bottom = 120.dp)) }
    }
}

@Composable
private fun TopStrip(a: MainActivity, ui: HomeUi) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Row(
            Modifier.weight(1f).height(44.dp).glass(RoundedCornerShape(22.dp))
                .pointerInput(Unit) { awaitEachGesture { val d = awaitFirstDown(); d.consume(); val up = waitUp(); if (up) a.layer = Layer.SEARCH } }
                .padding(horizontal = 14.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(20.dp))
            Text(t("Search apps, files, settings…", "Search apps, files and settings"), Modifier.weight(1f).padding(start = 10.dp), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted, maxLines = 1)
            Icon(Icons.Filled.Search, null, tint = NoCap.Muted, modifier = Modifier.size(18.dp))
        }
        Spacer(Modifier.width(8.dp))
        Box(
            Modifier.size(44.dp).glass(CircleShape, edge = NoCap.Red.copy(alpha = 0.5f))
                .pointerInput(Unit) { awaitEachGesture { val d = awaitFirstDown(); d.consume(); if (waitUp()) a.openAi() } },
            contentAlignment = Alignment.Center,
        ) { Text("AI", style = MaterialTheme.typography.labelLarge.copy(fontFamily = com.nocap.link.ui.Orbitron), color = NoCap.Red) }
    }
    if (ui.drag == null && !Prefs.homeAsked && !PhoneApps.isDefaultHome(a)) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 16.dp).glass(RoundedCornerShape(16.dp), edge = NoCap.Red.copy(alpha = 0.4f)).padding(12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(t("Make NoCap OS your home screen?", "Set NoCap OS as your home app?"), Modifier.weight(1f), style = MaterialTheme.typography.titleSmall)
            Text(
                t("Bet", "Set"), Modifier.clip(RoundedCornerShape(10.dp)).background(NoCap.Red)
                    .pointerInput(Unit) { awaitEachGesture { awaitFirstDown().consume(); if (waitUp()) { Prefs.setHomeAsked(); PhoneApps.askToBeHome(a) } } }
                    .padding(horizontal = 14.dp, vertical = 6.dp),
                style = MaterialTheme.typography.labelLarge, color = Color.White,
            )
            Text(
                t("Nah", "Later"), Modifier.padding(start = 8.dp)
                    .pointerInput(Unit) { awaitEachGesture { awaitFirstDown().consume(); if (waitUp()) Prefs.setHomeAsked() } }
                    .padding(6.dp),
                style = MaterialTheme.typography.labelLarge, color = NoCap.Muted,
            )
        }
    }
}

/** Waits for the finger to lift; true when it lifted without being dragged off. */
private suspend fun androidx.compose.ui.input.pointer.AwaitPointerEventScope.waitUp(): Boolean {
    while (true) {
        val ev = awaitPointerEvent()
        val ch = ev.changes.first()
        if (ch.changedToUp()) { ch.consume(); return true }
        if (ch.isConsumed) return false
    }
}

@Composable
private fun PageGrid(a: MainActivity, ui: HomeUi, p: Int, byKey: Map<String, PhoneApp>, cw: androidx.compose.ui.unit.Dp, ch: androidx.compose.ui.unit.Dp) {
    val page = Home.page(p)
    val drag = ui.drag
    Box(Modifier.fillMaxSize()) {
        for (item in page.items) {
            val c = item.cell
            val hidden = drag?.moved == true && drag.item?.id == item.id
            Box(
                Modifier.offset(cw * c.x, ch * c.y).size(cw * c.w, ch * c.h)
                    .onGloballyPositioned { ui.itemRects[item.id] = it.boundsInRoot() }
                    .alpha(if (hidden) 0.25f else 1f),
                contentAlignment = Alignment.Center,
            ) {
                when (item) {
                    is AppItem -> byKey[item.key]?.let { AppTile(it) } ?: GhostIcon(Prefs.iconSize.dp)
                    is FolderItem -> FolderTile(item.name, item.keys.mapNotNull { byKey[it] })
                    is WidgetItem -> Box(Modifier.fillMaxSize().padding(4.dp)) {
                        AndroidWidget(item, cw * c.w - 8.dp, ch * c.h - 8.dp) { ui.widgetMenu = p to item }
                    }
                    is NoCapItem -> Box(Modifier.fillMaxSize().padding(4.dp)) { com.nocap.link.nocap.NoCapWidget(a, item.kind) }
                }
            }
        }
        // Where a drop would land.
        if (drag != null && drag.moved && ui.gridRect.contains(drag.pos)) {
            val w = drag.item?.cell?.w ?: 1
            val h = drag.item?.cell?.h ?: 1
            val tl = drag.pos - drag.grab + Offset(ui.cellPx.x / 2f, ui.cellPx.y / 2f)
            val cx = ((tl.x - ui.gridRect.left) / ui.cellPx.x).toInt().coerceIn(0, (Prefs.columns - w).coerceAtLeast(0))
            val cy = ((tl.y - ui.gridRect.top) / ui.cellPx.y).toInt().coerceIn(0, (Prefs.rows - h).coerceAtLeast(0))
            val ok = Home.isFree(p, Cell(cx, cy, w, h), drag.item?.id)
            Box(
                Modifier.offset(cw * cx, ch * cy).size(cw * w, ch * h).padding(3.dp)
                    .border(1.5.dp, if (ok) NoCap.Red.copy(alpha = 0.7f) else Color.White.copy(alpha = 0.2f), RoundedCornerShape(16.dp)),
            )
        }
    }
}

@Composable
private fun DragGhost(d: Drag, byKey: Map<String, PhoneApp>, ui: HomeUi) {
    val density = LocalDensity.current
    val scale by animateFloatAsState(1.08f, label = "lift")
    val topLeft = d.pos - d.grab
    Box(Modifier.offset { IntOffset(topLeft.x.roundToInt(), topLeft.y.roundToInt()) }.scale(scale)) {
        when (val it = d.item) {
            is AppItem -> byKey[it.key]?.let { app -> AppTile(app, label = false) }
            is FolderItem -> FolderTile(it.name, it.keys.mapNotNull { k -> byKey[k] }, label = false)
            is NoCapItem, is WidgetItem -> {
                val w = with(density) { (ui.cellPx.x * it.cell.w).toDp() }
                val h = with(density) { (ui.cellPx.y * it.cell.h).toDp() }
                Box(Modifier.size(w, h).padding(4.dp).glass(RoundedCornerShape(18.dp), edge = NoCap.Red.copy(alpha = 0.6f)))
            }
            null -> d.dockKey?.let { k -> byKey[k]?.let { app -> AppTile(app, label = false) } }
        }
    }
}

@Composable
private fun PageDots(pager: PagerState, count: Int) {
    Row(Modifier.fillMaxWidth().padding(vertical = 6.dp), horizontalArrangement = Arrangement.Center) {
        repeat(count) { i ->
            val on = i == pager.currentPage
            Box(
                Modifier.padding(horizontal = 3.dp).height(6.dp).width(if (on) 16.dp else 6.dp).clip(RoundedCornerShape(3.dp))
                    .background(if (on) NoCap.Red else Color.White.copy(alpha = 0.35f)),
            )
        }
    }
}

/** The dock, like the NoCapOS desktop's: a glass bar with the Roster, your apps and NoCap. */
@Composable
private fun Dock(a: MainActivity, ui: HomeUi, byKey: Map<String, PhoneApp>) {
    val apps = Home.layout.dock.mapNotNull { k -> byKey[k] }
    val drag = ui.drag
    val over = drag != null && drag.moved && ui.dockRect.contains(drag.pos)
    Row(
        Modifier.fillMaxWidth().padding(horizontal = 12.dp)
            .onGloballyPositioned { ui.dockRect = it.boundsInRoot() }
            .glass(RoundedCornerShape(26.dp), edge = if (over) NoCap.Red.copy(alpha = 0.7f) else NoCap.GlassEdge)
            .padding(horizontal = 10.dp, vertical = 8.dp),
        horizontalArrangement = Arrangement.SpaceEvenly, verticalAlignment = Alignment.CenterVertically,
    ) {
        val s = (Prefs.iconSize - 6).coerceAtLeast(40).dp
        Box(Modifier.size(s).onGloballyPositioned { ui.dockSlots["btn:roster"] = it.boundsInRoot() }, contentAlignment = Alignment.Center) { RosterGlyph(s * 0.5f) }
        Box(Modifier.width(1.dp).height(34.dp).background(NoCap.GlassEdge))
        ui.dockSlots.keys.removeAll { !it.startsWith("btn:") && it !in Home.layout.dock }
        apps.forEach { app ->
            val hidden = drag?.moved == true && drag.dockKey == app.key
            Box(Modifier.onGloballyPositioned { ui.dockSlots[app.key] = it.boundsInRoot() }.alpha(if (hidden) 0.25f else 1f)) {
                AppTile(app, size = s, label = false)
            }
        }
        if (apps.isEmpty()) Text(t("Drag apps here", "Drag apps here"), style = MaterialTheme.typography.bodySmall)
        Box(Modifier.width(1.dp).height(34.dp).background(NoCap.GlassEdge))
        Box(Modifier.size(s).onGloballyPositioned { ui.dockSlots["btn:hub"] = it.boundsInRoot() }, contentAlignment = Alignment.Center) {
            Image(painterResource(R.drawable.nocap_mark), t("NoCap Link", "NoCap Link"), Modifier.size(s * 0.8f))
            val st = a.server
            val dot = when (st) {
                is ServerState.Online -> if (a.outdated != null) NoCap.Warn else NoCap.Ok
                is ServerState.NotLinked -> null
                is ServerState.Checking -> NoCap.Muted
                else -> NoCap.Warn
            }
            if (dot != null) Box(Modifier.align(Alignment.BottomEnd).size(10.dp).clip(CircleShape).background(dot).border(1.5.dp, Color(0xFF07080B), CircleShape))
        }
    }
}
