package com.nocap.link.home

import android.os.Build
import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.animateDpAsState
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.asPaddingValues
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBars
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.statusBars
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.pager.HorizontalPager
import androidx.compose.foundation.pager.PagerState
import androidx.compose.foundation.pager.rememberPagerState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.filled.Delete
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.Icons
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.blur
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.TransformOrigin
import androidx.compose.ui.input.pointer.changedToUp
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.boundsInRoot
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import com.nocap.link.data.BarStyle
import com.nocap.link.data.DockStyle
import com.nocap.link.data.DrawerStyle
import com.nocap.link.data.Gesture
import com.nocap.link.data.Opt
import com.nocap.link.data.PageFx
import com.nocap.link.data.PhoneApp
import com.nocap.link.data.PhoneApps
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.drawer.AppLibrary
import com.nocap.link.drawer.AppTile
import com.nocap.link.drawer.Drawer
import com.nocap.link.drawer.FolderTile
import com.nocap.link.drawer.GhostIcon
import com.nocap.link.drawer.iconShape
import com.nocap.link.drawer.nocapTile
import com.nocap.link.drawer.RosterGlyph
import com.nocap.link.drawer.SearchLayer
import com.nocap.link.Layer
import com.nocap.link.MainActivity
import com.nocap.link.R
import com.nocap.link.ServerState
import com.nocap.link.settings.SettingsScreen
import com.nocap.link.ui.glass
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.Orbitron
import com.nocap.link.ui.Toast
import com.nocap.link.widget.AndroidWidget
import com.nocap.link.widget.WidgetPicker
import com.nocap.link.widget.Widgets
import kotlin.math.abs
import kotlin.math.roundToInt
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

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
    val library = Opt.drawer.v == DrawerStyle.HOME_PAGES
    val pageCount = layout.pages.size + if (library) 1 else 0
    val pager = rememberPagerState { Home.layout.pages.size + if (Opt.drawer.v == DrawerStyle.HOME_PAGES) 1 else 0 }
    val scope = rememberCoroutineScope()
    val view = LocalView.current
    val density = LocalDensity.current
    val byKey = remember(a.phoneApps) { a.phoneApps.associateBy { it.key } }
    val locked = Opt.lockLayout.v
    fun firstPage() { scope.launch { pager.animateScrollToPage(0) } }

    BackHandler(a.layer != Layer.NONE || ui.folder != null || ui.menu != null || ui.editAt != null || ui.widgetMenu != null || ui.moving != null || a.editing != null || a.weatherFor != null || a.customizing != null) {
        when {
            a.customizing != null -> a.customizing = null
            a.editing != null -> a.editing = null
            a.weatherFor != null -> a.weatherFor = null
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
    LaunchedEffect(a.homeTick) {
        if (a.homeTick > 0) {
            ui.menu = null; ui.folder = null; ui.editAt = null; ui.widgetMenu = null; ui.moving = null
            runGesture(a, Gesture.HOME, ::firstPage)
        }
    }
    LaunchedEffect(a.libraryTick) { if (a.libraryTick > 0 && library) pager.animateScrollToPage(pageCount - 1) }

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
            if (target < 0 || target >= Home.layout.pages.size + 1) break
            if (target >= Home.layout.pages.size) Home.addPage()
            pager.animateScrollToPage(target)
        }
    }

    fun onLibrary() = library && pager.currentPage >= Home.layout.pages.size

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
        if (ui.gridRect.contains(pos) && !onLibrary()) {
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
                "roster" -> a.openDrawer()
                "hub" -> a.layer = Layer.HUB
            }
            else -> {}
        }
    }

    fun longPress(h: Hit, pos: Offset): Boolean {
        buzz(view, long = true)
        when (h) {
            is Hit.OnItem -> {
                val r = ui.itemRects[h.item.id] ?: Rect(pos, pos)
                when (val it = h.item) {
                    is AppItem -> ui.menu = Menu(byKey[it.key], it, r, From.HOME)
                    is WidgetItem -> { ui.widgetMenu = h.page to it; return false }
                    else -> ui.widgetMenu = h.page to it
                }
                if (locked) return false
                ui.drag = Drag(h.item, null, pos, pos - r.topLeft, pos)
                return true
            }
            is Hit.Dock -> {
                val r = ui.dockSlots[h.key] ?: Rect(pos, pos)
                ui.menu = Menu(byKey[h.key], null, r, From.DOCK)
                if (locked) return false
                ui.drag = Drag(null, h.key, pos, pos - r.topLeft, pos)
                return true
            }
            is Hit.Empty -> { ui.editAt = pos; return false }
            Hit.None -> { if (!onLibrary() && !ui.dockRect.contains(pos)) ui.editAt = pos; return false }
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
        if (page >= Home.layout.pages.size) return
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
                if (dock.size >= Opt.dockCount.v) { a.toast = t("Dock's full — unpin something first.", "The dock is full."); return }
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
                    else if (d.dockKey != null) {
                        Home.setDock(Home.layout.dock - d.dockKey)
                        Home.set(Home.layout.copy(pages = Home.layout.pages.mapIndexed { i, p -> if (i == page) Page(p.items + dragged.at(Cell(cx, cy))) else p }))
                    }
                }
            }
        }
    }

    val overlay = a.layer == Layer.DRAWER || a.layer == Layer.SETTINGS || a.layer == Layer.SEARCH
    val blurAmount by animateDpAsState(if (overlay && Opt.drawerBlur.v && Build.VERSION.SDK_INT >= 31) 18.dp else 0.dp, label = "blur")

    Box(Modifier.fillMaxSize()) {
        WallpaperLayer(scroll = { (pager.currentPage + pager.currentPageOffsetFraction) / (pageCount - 1).coerceAtLeast(1) }, extraBlur = blurAmount)
        Box(
            Modifier.fillMaxSize().then(if (blurAmount > 0.dp) Modifier.blur(blurAmount) else Modifier).pointerInput(byKey, locked) {
                val slop = viewConfiguration.touchSlop
                val swipe = 70.dp.toPx()
                awaitEachGesture {
                    val down = awaitFirstDown(requireUnconsumed = false)
                    val start = down.position
                    val h = hit(start)
                    if (h is Hit.OnItem && h.item is WidgetItem) return@awaitEachGesture // the widget handles its own touches
                    // A button on top (AI, search bar, banners) already took this touch: it is
                    // theirs, so it must never turn into a long-press or a tap here.
                    if (down.isConsumed || a.layer != Layer.NONE) {
                        while (true) {
                            val ev = awaitPointerEvent()
                            if (ev.changes.none { it.pressed }) break
                        }
                        return@awaitEachGesture
                    }
                    var upConsumed = false
                    var moved = false
                    var multi = false
                    var timedOut = withTimeoutOrNull(viewConfiguration.longPressTimeoutMillis) {
                        var done = false
                        while (!done) {
                            val ev = awaitPointerEvent()
                            if (ev.changes.count { it.pressed } >= 2) { multi = true; done = true; continue }
                            val ch = ev.changes.firstOrNull { it.id == down.id }
                            if (ch == null) {
                                done = true
                            } else if (ch.changedToUpIgnoringConsumed() || !ch.pressed) {
                                // Lifted (or cancelled because another screen opened), even if a child consumed it.
                                upConsumed = ch.isConsumed || !ch.changedToUpIgnoringConsumed()
                                done = true
                            } else if ((ch.position - start).getDistance() > slop) {
                                moved = true
                                done = true
                            }
                        }
                        true
                    } == null
                    if (multi) {
                        // Two fingers: pinch in/out or a two-finger swipe.
                        timedOut = false
                        var first: Pair<Offset, Offset>? = null
                        var last: Pair<Offset, Offset>? = null
                        while (true) {
                            val ev = awaitPointerEvent()
                            val pressed = ev.changes.filter { it.pressed }
                            if (pressed.size >= 2) {
                                val pair = pressed[0].position to pressed[1].position
                                if (first == null) first = pair
                                last = pair
                            }
                            if (ev.changes.none { it.pressed }) break
                        }
                        val f = first
                        val l = last
                        if (f != null && l != null && a.layer == Layer.NONE && ui.drag == null) {
                            val d0 = (f.first - f.second).getDistance()
                            val d1 = (l.first - l.second).getDistance()
                            val cy0 = (f.first.y + f.second.y) / 2
                            val cy1 = (l.first.y + l.second.y) / 2
                            when {
                                d0 > 0 && d1 / d0 < 0.72f -> runGesture(a, Gesture.PINCH_IN, ::firstPage)
                                d0 > 0 && d1 / d0 > 1.35f -> runGesture(a, Gesture.PINCH_OUT, ::firstPage)
                                cy1 - cy0 < -swipe -> runGesture(a, Gesture.TWO_UP, ::firstPage)
                                cy1 - cy0 > swipe -> runGesture(a, Gesture.TWO_DOWN, ::firstPage)
                            }
                        }
                        return@awaitEachGesture
                    }
                    if (timedOut && (a.layer != Layer.NONE || !a.lifecycle.currentState.isAtLeast(androidx.lifecycle.Lifecycle.State.RESUMED))) {
                        // Something opened on top while the finger was down: not a long-press.
                        while (true) {
                            val ev = awaitPointerEvent(PointerEventPass.Initial)
                            if (ev.changes.all { !it.pressed }) break
                        }
                        return@awaitEachGesture
                    }
                    if (timedOut) {
                        // From here on we own the touch: children (widget buttons) must not also react.
                        if (!longPress(h, start)) {
                            while (true) {
                                val ev = awaitPointerEvent(PointerEventPass.Initial)
                                ev.changes.forEach { it.consume() }
                                if (ev.changes.all { !it.pressed }) break
                            }
                            return@awaitEachGesture
                        }
                        while (true) {
                            val ev = awaitPointerEvent(PointerEventPass.Initial)
                            val ch = ev.changes.firstOrNull { it.id == down.id }
                            ev.changes.forEach { it.consume() }
                            if (ch == null) break
                            if (ch.changedToUpIgnoringConsumed() || !ch.pressed) { drop(ch.position); break }
                            dragTo(ch.position)
                        }
                        if (ui.drag != null) ui.drag = null
                        return@awaitEachGesture
                    }
                    if (moved) {
                        // A swipe: up / down run whatever they're bound to.
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
                        if (!horizontal && abs(dy) > swipe && abs(dy) > abs(dx) * 1.4f && a.layer == Layer.NONE && ui.drag == null && !onLibrary()) {
                            if (dy < 0) runGesture(a, Gesture.SWIPE_UP, ::firstPage) else runGesture(a, Gesture.SWIPE_DOWN, ::firstPage)
                        }
                        return@awaitEachGesture
                    }
                    if (upConsumed) return@awaitEachGesture
                    val empty = h is Hit.Empty || h is Hit.None
                    if (empty && !onLibrary() && Opt.binding(Gesture.DOUBLE_TAP) != com.nocap.link.data.Act.NONE.name && ui.moving == null) {
                        val second = withTimeoutOrNull(viewConfiguration.doubleTapTimeoutMillis) { awaitFirstDown(requireUnconsumed = false) }
                        if (second != null && (second.position - start).getDistance() < 60.dp.toPx()) {
                            runGesture(a, Gesture.DOUBLE_TAP, ::firstPage)
                            return@awaitEachGesture
                        }
                    }
                    tap(h)
                }
            },
        ) {
            val bars = Opt.statusBar.v
            Column(Modifier.fillMaxSize().then(if (bars == BarStyle.HIDDEN) Modifier.padding(top = 6.dp) else Modifier.statusBarsPadding()).navigationBarsPadding()) {
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
                        Box(Modifier.fillMaxSize().pageFx(pager, p)) {
                            if (p >= Home.layout.pages.size) AppLibrary(a, ui) else PageGrid(a, ui, p, byKey, cw, ch)
                        }
                    }
                }
                if (Opt.pageDots.v && pageCount > 1) PageDots(pager, pageCount, library)
                Dock(a, ui, byKey)
                Spacer(Modifier.height(if (Opt.dockStyle.v == DockStyle.FLOATING) 14.dp else 8.dp))
            }
        }

        // Status/navigation bar scrims.
        if (Opt.statusBar.v == BarStyle.TINTED) {
            Box(Modifier.fillMaxWidth().height(WindowInsets.statusBars.asPaddingValues().calculateTopPadding() + 12.dp).background(Brush.verticalGradient(listOf(Color.Black.copy(alpha = 0.55f), Color.Transparent))))
        }
        if (Opt.navBar.v == BarStyle.TINTED) {
            Box(Modifier.align(Alignment.BottomCenter).fillMaxWidth().height(WindowInsets.navigationBars.asPaddingValues().calculateBottomPadding() + 12.dp).background(Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = 0.55f)))))
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
            com.nocap.link.sync.Hub(a)
        }
        AnimatedVisibility(a.layer == Layer.SETTINGS, enter = fadeIn(), exit = fadeOut()) {
            SettingsScreen(a)
        }
        AnimatedVisibility(a.layer == Layer.PERMISSIONS, enter = fadeIn(), exit = fadeOut()) {
            com.nocap.link.settings.PermissionsScreen(a)
        }
        if (a.layer == Layer.WIDGETS) {
            WidgetPicker(a, a.cellW, a.cellH, onNoCap = { k, w, h -> a.addNoCapWidget(k, pager.currentPage.coerceAtMost(Home.layout.pages.size - 1), w, h) }, onPick = { a.addWidget(it, pager.currentPage.coerceAtMost(Home.layout.pages.size - 1)) }, onClose = { a.layer = Layer.NONE })
        }
        ui.menu?.let { m -> AppMenu(a, ui, m, page = pager.currentPage.coerceAtMost(Home.layout.pages.size - 1)) }
        a.weatherFor?.let { com.nocap.link.widget.WeatherSheet(a, it) }
        a.editing?.let { com.nocap.link.widget.WidgetEditSheet(a, it) }
        a.customizing?.let { CustomizeApp(a, it) }
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

private fun androidx.compose.ui.input.pointer.PointerInputChange.changedToUpIgnoringConsumed() = previousPressed && !pressed

/** Page transition effects from Settings. */
private fun Modifier.pageFx(pager: PagerState, page: Int): Modifier = graphicsLayer {
    val off = (pager.currentPage - page) + pager.currentPageOffsetFraction
    val o = off.coerceIn(-1f, 1f)
    when (Opt.pageFx.v) {
        PageFx.SLIDE -> {}
        PageFx.FADE -> { translationX = o * size.width; alpha = 1f - abs(o) }
        PageFx.ZOOM -> { val s = 1f - 0.18f * abs(o); scaleX = s; scaleY = s; alpha = 1f - 0.6f * abs(o) }
        PageFx.CUBE -> {
            cameraDistance = 12f * density
            transformOrigin = TransformOrigin(if (o > 0) 1f else 0f, 0.5f)
            rotationY = -45f * o
        }
        PageFx.STACK -> if (o > 0) { translationX = o * size.width; val s = 1f - 0.15f * o; scaleX = s; scaleY = s; alpha = 1f - o }
        PageFx.CAROUSEL -> { rotationY = -25f * o; cameraDistance = 10f * density; val s = 1f - 0.1f * abs(o); scaleX = s; scaleY = s }
    }
}

@Composable
private fun TopStrip(a: MainActivity, ui: HomeUi) {
    if (Opt.searchBar.v) Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
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
                .pointerInput(Unit) { awaitEachGesture { val d = awaitFirstDown(); d.consume(); if (waitUp()) runCatching { a.openAi() }.onFailure { a.toast = t("NoCap AI wouldn't open. Try again.", "NoCap AI couldn't be opened.") } } },
            contentAlignment = Alignment.Center,
        ) { Text("AI", style = MaterialTheme.typography.labelLarge.copy(fontFamily = Orbitron), color = NoCap.Red) }
    } else Spacer(Modifier.height(8.dp))
    // A PackageManager call: checked once per resume, not on every recomposition.
    val isHome = remember(a.resumeTick) { runCatching { PhoneApps.isDefaultHome(a) }.getOrDefault(true) }
    if (ui.drag == null && !Prefs.homeAsked && !isHome) {
        Row(
            Modifier.fillMaxWidth().padding(horizontal = 16.dp).glass(RoundedCornerShape(16.dp), edge = NoCap.Red.copy(alpha = 0.4f)).padding(12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(t("Make NoCap OS your home screen?", "Set NoCap OS as your home app?"), Modifier.weight(1f), style = MaterialTheme.typography.titleSmall)
            Text(
                t("Bet", "Set"), Modifier.clip(RoundedCornerShape(10.dp)).background(NoCap.Red)
                    .pointerInput(Unit) { awaitEachGesture { awaitFirstDown().consume(); if (waitUp()) { Prefs.setHomeAsked(); runCatching { PhoneApps.askToBeHome(a) }.onFailure { a.toast = t("Couldn't open the home app picker.", "The home app settings couldn't be opened.") } } } }
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
        val ch = ev.changes.firstOrNull() ?: return false
        if (ch.changedToUp()) { ch.consume(); return true }
        if (ch.isConsumed) return false
    }
}

@Composable
private fun PageGrid(a: MainActivity, ui: HomeUi, p: Int, byKey: Map<String, PhoneApp>, cw: Dp, ch: Dp) {
    val page = Home.page(p)
    val drag = ui.drag
    // Icons and labels shrink to fit small cells (small phones, landscape, big grids).
    val labelRoom = (Opt.labelSize.v + 14).dp
    val icon = minOf(Opt.iconSize.v.dp, cw - 14.dp, ch - 10.dp).coerceAtLeast(28.dp)
    val showLabels = Opt.labels.v && ch >= icon + labelRoom + 6.dp
    val icon2 = if (showLabels) minOf(icon, ch - labelRoom - 8.dp).coerceAtLeast(28.dp) else icon
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
                    is AppItem -> byKey[item.key]?.let { AppTile(it, size = icon2, label = showLabels, labelWidth = cw - 4.dp) } ?: GhostIcon(icon2)
                    is FolderItem -> FolderTile(item.name, item.keys.mapNotNull { byKey[it] }, size = icon2, label = showLabels, labelWidth = cw - 4.dp)
                    is WidgetItem -> Box(Modifier.fillMaxSize().padding(4.dp)) {
                        AndroidWidget(item, cw * c.w - 8.dp, ch * c.h - 8.dp) { ui.widgetMenu = p to item }
                    }
                    is NoCapItem -> Box(Modifier.fillMaxSize().padding(4.dp)) { com.nocap.link.widget.NoCapWidget(a, item) }
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
private fun PageDots(pager: PagerState, count: Int, library: Boolean) {
    Row(Modifier.fillMaxWidth().padding(vertical = 6.dp), horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
        repeat(count) { i ->
            val on = i == pager.currentPage
            if (library && i == count - 1) {
                // The Stash page's dot is a little grid.
                Box(Modifier.padding(horizontal = 3.dp).size(8.dp).clip(RoundedCornerShape(2.dp)).border(1.5.dp, if (on) NoCap.Red else Color.White.copy(alpha = 0.5f), RoundedCornerShape(2.dp)))
            } else {
                Box(
                    Modifier.padding(horizontal = 3.dp).height(6.dp).width(if (on) 16.dp else 6.dp).clip(RoundedCornerShape(3.dp))
                        .background(if (on) NoCap.Red else Color.White.copy(alpha = 0.35f)),
                )
            }
        }
    }
}

/** The dock, like the NoCapOS desktop's: the Roster, your apps and NoCap Link. */
@Composable
private fun Dock(a: MainActivity, ui: HomeUi, byKey: Map<String, PhoneApp>) {
    val apps = Home.layout.dock.mapNotNull { k -> byKey[k] }.take(Opt.dockCount.v)
    val drag = ui.drag
    val over = drag != null && drag.moved && ui.dockRect.contains(drag.pos)
    val style = Opt.dockStyle.v
    val buttons = Opt.dockButtons.v
    // The app drawer button can be hidden on its own; the drawer still opens with its gesture.
    val roster = Opt.dockDrawerButton.v
    BoxWithConstraints(Modifier.fillMaxWidth().padding(horizontal = if (style == DockStyle.FLOATING) 20.dp else 12.dp), contentAlignment = Alignment.Center) {
        // Fit everything on one row, however narrow the screen.
        val slots = apps.size.coerceAtLeast(1) + (if (buttons) 1 else 0) + (if (roster) 1 else 0)
        val avail = maxWidth - 20.dp - (if (buttons) 18.dp else 0.dp)
        val s = minOf((Opt.iconSize.v - 4).coerceAtLeast(40).dp, avail / slots - 8.dp).coerceAtLeast(32.dp)
        val shape = RoundedCornerShape(if (style == DockStyle.FLOATING) 30.dp else 26.dp)
        val bg = when (style) {
            DockStyle.GLASS -> Modifier.fillMaxWidth().glass(shape, edge = if (over) NoCap.Red.copy(alpha = 0.7f) else NoCap.GlassEdge)
            DockStyle.FLOATING -> Modifier.widthIn(max = maxWidth).glass(shape, strong = true, edge = if (over) NoCap.Red.copy(alpha = 0.8f) else NoCap.Red.copy(alpha = 0.35f))
            DockStyle.NONE -> Modifier.fillMaxWidth().then(if (over) Modifier.border(1.dp, NoCap.Red.copy(alpha = 0.6f), shape) else Modifier)
        }
        Row(
            bg.onGloballyPositioned { ui.dockRect = it.boundsInRoot() }.padding(horizontal = 10.dp, vertical = 8.dp),
            horizontalArrangement = if (style == DockStyle.FLOATING) Arrangement.spacedBy(8.dp) else Arrangement.SpaceEvenly, verticalAlignment = Alignment.CenterVertically,
        ) {
            ui.dockSlots.keys.removeAll { !it.startsWith("btn:") && it !in Home.layout.dock }
            if (roster) {
                Box(Modifier.size(s).onGloballyPositioned { ui.dockSlots["btn:roster"] = it.boundsInRoot() }, contentAlignment = Alignment.Center) {
                    Box(Modifier.size(s).nocapTile(iconShape(s, nocapDefault = true) ?: RoundedCornerShape(s * 0.24f), NoCap.Red, NoCap.light), contentAlignment = Alignment.Center) { RosterGlyph(s * 0.5f) }
                }
                Box(Modifier.width(1.dp).height(s * 0.6f).background(NoCap.GlassEdge))
            } else ui.dockSlots.remove("btn:roster")
            apps.forEach { app ->
                val hidden = drag?.moved == true && drag.dockKey == app.key
                Box(Modifier.onGloballyPositioned { ui.dockSlots[app.key] = it.boundsInRoot() }.alpha(if (hidden) 0.25f else 1f)) {
                    AppTile(app, size = s, label = Opt.dockLabels.v, labelWidth = s + 12.dp)
                }
            }
            if (apps.isEmpty()) Text(t("Drag apps here", "Drag apps here"), style = MaterialTheme.typography.bodySmall)
            if (buttons) {
                Box(Modifier.width(1.dp).height(s * 0.6f).background(NoCap.GlassEdge))
                Box(Modifier.size(s).onGloballyPositioned { ui.dockSlots["btn:hub"] = it.boundsInRoot() }, contentAlignment = Alignment.Center) {
                    Image(painterResource(R.drawable.nocap_mark), t("NoCap Link", "NoCap Link"), Modifier.size(s * 0.8f))
                    val dot = when (a.server) {
                        is ServerState.Online -> if (a.outdated != null) NoCap.Warn else NoCap.Ok
                        is ServerState.NotLinked -> null
                        is ServerState.Checking -> NoCap.Muted
                        else -> NoCap.Warn
                    }
                    if (dot != null) Box(Modifier.align(Alignment.BottomEnd).size(10.dp).clip(CircleShape).background(dot).border(1.5.dp, Color(0xFF07080B), CircleShape))
                }
            } else ui.dockSlots.remove("btn:hub")
        }
    }
}
