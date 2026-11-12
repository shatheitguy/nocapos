package com.nocap.link.home

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.Image
import androidx.compose.foundation.interaction.MutableInteractionSource
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
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.layout.boundsInRoot
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.dp
import com.nocap.link.data.AppShortcut
import com.nocap.link.data.PhoneApp
import com.nocap.link.data.PhoneApps
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.drawer.AppIcon
import com.nocap.link.drawer.AppTile
import com.nocap.link.Layer
import com.nocap.link.MainActivity
import com.nocap.link.ui.glass
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.Orbitron
import com.nocap.link.widget.noCapWidgetName
import com.nocap.link.widget.Widgets
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** Places a popup next to [anchor] (above it when there's no room below), inside the screen. */
@Composable
fun AnchoredPopup(anchor: Rect, onDismiss: () -> Unit, content: @Composable () -> Unit) {
    val gap = with(LocalDensity.current) { 8.dp.toPx() }
    Box(Modifier.fillMaxSize().background(Color(0x66000000)).clickable(remember { MutableInteractionSource() }, null, onClick = onDismiss)) {
        Layout(content = {
            Box(Modifier.clickable(remember { MutableInteractionSource() }, null) {}) { content() }
        }) { measurables, constraints ->
            val p = measurables.first().measure(Constraints(maxWidth = constraints.maxWidth, maxHeight = constraints.maxHeight))
            layout(constraints.maxWidth, constraints.maxHeight) {
                val x = (anchor.center.x - p.width / 2f).coerceIn(gap, (constraints.maxWidth - p.width - gap).coerceAtLeast(gap))
                val below = anchor.bottom + gap
                val y = if (below + p.height < constraints.maxHeight - gap * 6) below else (anchor.top - gap - p.height).coerceAtLeast(gap * 6)
                p.place(x.toInt(), y.toInt())
            }
        }
    }
}

@Composable
private fun MenuRow(text: String, danger: Boolean = false, onClick: () -> Unit) {
    Text(
        text, Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable(onClick = onClick).padding(horizontal = 12.dp, vertical = 11.dp),
        style = MaterialTheme.typography.titleSmall, color = if (danger) NoCap.Red else NoCap.Text,
    )
}

@Composable
private fun MenuCard(content: @Composable () -> Unit) {
    Column(Modifier.widthIn(min = 220.dp, max = 300.dp).glass(RoundedCornerShape(18.dp), strong = true).padding(8.dp)) { content() }
}

/** Long-press on an app: its shortcuts, then info, uninstall, hide, home/dock. */
@Composable
fun AppMenu(a: MainActivity, ui: HomeUi, m: Menu, page: Int) {
    val app = m.app
    var shortcuts by remember(app?.key) { mutableStateOf<List<AppShortcut>>(emptyList()) }
    LaunchedEffect(app?.key) {
        if (app != null) shortcuts = withContext(Dispatchers.IO) { runCatching { PhoneApps.shortcuts(a, app) }.getOrDefault(emptyList()) }
    }
    fun close() { ui.menu = null; ui.drag = null }
    AnchoredPopup(m.anchor, ::close) {
        MenuCard {
            if (app == null) {
                MenuRow(t("Remove", "Remove"), danger = true) { m.item?.let { Home.remove(it.id) }; close() }
                return@MenuCard
            }
            Row(Modifier.padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
                Image(app.icon, null, Modifier.size(28.dp))
                Text(app.label, Modifier.padding(start = 10.dp), style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            if (shortcuts.isNotEmpty()) {
                shortcuts.forEach { s ->
                    Row(
                        Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { PhoneApps.startShortcut(a, s); close() }.padding(horizontal = 12.dp, vertical = 9.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        s.icon?.let { Image(it, null, Modifier.size(20.dp)) } ?: Spacer(Modifier.size(20.dp))
                        Text(s.label, Modifier.padding(start = 10.dp), style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                }
                Box(Modifier.fillMaxWidth().padding(vertical = 4.dp).height(1.dp).background(NoCap.GlassEdge))
            }
            val locked = com.nocap.link.data.Opt.lockLayout.v
            if (!locked && (m.from == From.DRAWER || m.from == From.SEARCH)) {
                MenuRow(t("Add to home", "Add to home screen")) {
                    val p = Home.addApp(app.key, page)
                    a.toast = t("Added to page ${p + 1}.", "Added to page ${p + 1}.")
                    close(); a.layer = Layer.NONE
                }
            }
            val inDock = app.key in Home.layout.dock
            if (!locked) MenuRow(if (inDock) t("Unpin from dock", "Remove from dock") else t("Pin to dock", "Add to dock")) {
                if (inDock) Home.setDock(Home.layout.dock - app.key)
                else if (Home.layout.dock.size >= com.nocap.link.data.Opt.dockCount.v) a.toast = t("Dock's full — unpin something first.", "The dock is full.")
                else {
                    Home.setDock(Home.layout.dock + app.key)
                    if (m.from == From.HOME) m.item?.let { Home.remove(it.id) }
                }
                close()
            }
            if (!locked && m.from == From.HOME) MenuRow(t("Remove from home", "Remove from home screen")) { m.item?.let { Home.remove(it.id) }; close() }
            MenuRow(t("Customize icon & name", "Edit icon and label")) { a.customizing = app; close() }
            if (!locked && m.from == From.FOLDER && m.folder != null) {
                MenuRow(t("Take out of folder", "Remove from folder")) {
                    val f = m.folder
                    Home.removeFromFolder(f, app.key, page)
                    ui.folder = null
                    close()
                }
            }
            MenuRow(t("App info", "App info")) {
                runCatching { PhoneApps.info(a, app) }.onFailure { a.toast = t("Couldn't open app info.", "App info couldn't be opened.") }
                close()
            }
            MenuRow(t("Hide", "Hide app")) {
                Prefs.hide(app.key, true)
                a.toast = t("${app.label} is hidden. Unhide it in Settings.", "${app.label} hidden. Unhide it in Settings.")
                close()
            }
            if (!app.system) MenuRow(t("Uninstall", "Uninstall"), danger = true) {
                runCatching { PhoneApps.uninstall(a, app) }.onFailure { a.toast = t("Couldn't start the uninstall.", "The app couldn't be uninstalled from here.") }
                close()
            }
        }
    }
}

/** Long-press on a widget: move, resize, remove. */
@Composable
fun WidgetMenu(a: MainActivity, ui: HomeUi, page: Int, item: Item) {
    val anchor = ui.itemRects[item.id] ?: Rect(Offset(200f, 400f), Offset(400f, 600f))
    // AppWidgetManager / PackageManager lookups: once per widget, not every recomposition.
    val widgetId = (item as? WidgetItem)?.widgetId
    val info = remember(widgetId) { widgetId?.let { Widgets.info(a, it) } }
    val widgetLabel = remember(info) { info?.let { runCatching { it.loadLabel(a.packageManager) }.getOrNull() } }
    val resizable = item is WidgetItem && Widgets.canResize(info)
    fun close() { ui.widgetMenu = null }
    val locked = com.nocap.link.data.Opt.lockLayout.v
    AnchoredPopup(Rect(anchor.center, anchor.center), ::close) {
        MenuCard {
            Text(
                when (item) {
                    is NoCapItem -> noCapWidgetName(item.kind)
                    is WidgetItem -> widgetLabel ?: t("Widget", "Widget")
                    else -> t("Widget", "Widget")
                },
                Modifier.padding(8.dp), style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis,
            )
            if (item is NoCapItem) MenuRow(t("Customize", "Customize")) { a.editing = item; close() }
            if (locked) {
                Text(lockedMsg(), Modifier.padding(horizontal = 12.dp, vertical = 6.dp), style = MaterialTheme.typography.bodySmall)
                return@MenuCard
            }
            if (item is WidgetItem) {
                MenuRow(t("Move", "Move")) { ui.moving = item; close() }
            } else {
                Text(t("Hold and drag to move it.", "Long-press and drag to move it."), Modifier.padding(horizontal = 12.dp, vertical = 4.dp), style = MaterialTheme.typography.bodySmall)
            }
            if (resizable || item is NoCapItem) {
                val c = (Home.find(item.id)?.second ?: item).cell
                Row(Modifier.padding(horizontal = 12.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(t("Size", "Size"), Modifier.weight(1f), style = MaterialTheme.typography.titleSmall)
                    Stepper("${c.w}×${c.h}")
                }
                Row(Modifier.padding(horizontal = 8.dp).fillMaxWidth(), horizontalArrangement = Arrangement.SpaceEvenly) {
                    SmallBtn(t("Narrower", "W −")) { resize(a, page, item, -1, 0) }
                    SmallBtn(t("Wider", "W +")) { resize(a, page, item, 1, 0) }
                    SmallBtn(t("Shorter", "H −")) { resize(a, page, item, 0, -1) }
                    SmallBtn(t("Taller", "H +")) { resize(a, page, item, 0, 1) }
                }
            }
            MenuRow(t("Yeet it", "Remove"), danger = true) {
                if (item is WidgetItem) Widgets.delete(a, item.widgetId)
                Home.remove(item.id)
                close()
            }
        }
    }
}

private fun resize(a: MainActivity, page: Int, item: Item, dw: Int, dh: Int) {
    val now = Home.find(item.id)?.second ?: return
    val c = now.cell
    var nw = (c.w + dw).coerceAtLeast(1)
    var nh = (c.h + dh).coerceAtLeast(1)
    if (now is NoCapItem) {
        val s = NoCapKinds.spec(now.kind)
        nw = nw.coerceIn(s.min.first, s.max.first)
        nh = nh.coerceIn(s.min.second, s.max.second)
    }
    val n = Cell(c.x, c.y, nw, nh)
    if (n == c) { a.toast = t("That's as far as it goes.", "That's the limit for this widget."); return }
    if (Home.isFree(page, n, item.id)) {
        Home.replace(now.at(n))
        // keep the menu showing the new size
    } else {
        a.toast = t("No room to grow that way.", "There's no room for that size.")
    }
}

@Composable
private fun Stepper(text: String) {
    Text(text, style = MaterialTheme.typography.labelLarge, color = NoCap.Red)
}

@Composable
private fun SmallBtn(text: String, onClick: () -> Unit) {
    Text(
        text, Modifier.padding(horizontal = 2.dp).clip(RoundedCornerShape(8.dp)).background(NoCap.Field).clickable(onClick = onClick).padding(horizontal = 8.dp, vertical = 6.dp),
        style = MaterialTheme.typography.labelSmall, maxLines = 1,
    )
}

/** Long-press on an empty spot: wallpaper, widgets, settings, pages. */
@Composable
fun EditMenu(a: MainActivity, ui: HomeUi, at: Offset, page: Int, goTo: (Int) -> Unit) {
    fun close() { ui.editAt = null }
    AnchoredPopup(Rect(at, at), ::close) {
        MenuCard {
            Label(t("The Hub", "Home screen"), Modifier.padding(8.dp), color = NoCap.Red)
            val locked = com.nocap.link.data.Opt.lockLayout.v
            if (locked) {
                Text(lockedMsg(), Modifier.padding(horizontal = 12.dp, vertical = 4.dp), style = MaterialTheme.typography.bodySmall)
                MenuRow(t("Unlock layout", "Unlock layout")) { com.nocap.link.data.Opt.lockLayout.set(false); close() }
            } else {
                MenuRow(t("Widgets", "Add widgets")) { a.layer = Layer.WIDGETS; close() }
                MenuRow(t("Add a page", "Add page")) { Home.addPage(); goTo(Home.layout.pages.size - 1); close() }
                if (Home.layout.pages.size > 1 && page < Home.layout.pages.size && Home.page(page).items.none { it is WidgetItem }) {
                    MenuRow(t("Yeet this page", "Remove this page"), danger = Home.page(page).items.isNotEmpty()) {
                        Home.removePage(page); goTo((page - 1).coerceAtLeast(0)); close()
                    }
                }
                MenuRow(t("Lock layout", "Lock layout")) { com.nocap.link.data.Opt.lockLayout.set(true); close() }
            }
            MenuRow(t("Change the aesthetic", "Wallpaper")) { a.settingsQuery = t("Wall", "Wallpaper"); a.layer = Layer.SETTINGS; close() }
            MenuRow(t("Settings", "Settings")) { a.layer = Layer.SETTINGS; close() }
        }
    }
}

/** An open folder: a window-like glass card with a title bar, like NoCapOS's windows. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
fun FolderWindow(a: MainActivity, ui: HomeUi, page: Int, f: FolderItem, byKey: Map<String, PhoneApp>) {
    var name by remember(f.id) { mutableStateOf(f.name) }
    val current = (Home.find(f.id)?.second as? FolderItem) ?: f
    // distinctBy: the grid below keys items by app, and a duplicate key would crash it.
    val apps = current.keys.mapNotNull { byKey[it] }.distinctBy { it.key }
    fun close() {
        if (name.isNotBlank() && name != current.name) Home.replace(current.copy(name = name.trim()))
        ui.folder = null
    }
    Box(Modifier.fillMaxSize().background(Color(0x99000000)).clickable(remember { MutableInteractionSource() }, null) { close() }, contentAlignment = Alignment.Center) {
        WindowFrame(
            title = {
                BasicTextField(
                    name, { name = it.take(40) }, singleLine = true,
                    textStyle = MaterialTheme.typography.labelLarge.copy(fontFamily = Orbitron, color = NoCap.Text),
                    cursorBrush = SolidColor(NoCap.Red),
                )
            },
            onClose = ::close,
            modifier = Modifier.padding(24.dp).widthIn(max = 420.dp).clickable(remember { MutableInteractionSource() }, null) {},
        ) {
            LazyVerticalGrid(GridCells.Fixed(4), Modifier.padding(10.dp).height(((apps.size + 3) / 4 * 96).coerceIn(96, 400).dp)) {
                items(apps, key = { it.key }) { app ->
                    var r by remember { mutableStateOf(Rect.Zero) }
                    Box(
                        Modifier.onGloballyPositioned { r = it.boundsInRoot() }.clip(RoundedCornerShape(14.dp)).combinedClickable(
                            onClick = { a.launch(app); close() },
                            onLongClick = { ui.menu = Menu(app, null, r, From.FOLDER, current) },
                        ),
                        contentAlignment = Alignment.Center,
                    ) { AppTile(app, size = 50.dp, onWallpaper = false, labelWidth = 72.dp) }
                }
            }
        }
    }
    // Keep page in scope for the folder menu.
    LaunchedEffect(page) {}
}

/** A NoCapOS-style window: title bar with controls, glass body. */
@Composable
fun WindowFrame(title: @Composable () -> Unit, onClose: (() -> Unit)?, modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Column(modifier.glass(RoundedCornerShape(18.dp), strong = true)) {
        Row(
            Modifier.fillMaxWidth().background(Color(0x221A1C22)).padding(horizontal = 14.dp, vertical = 9.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box(Modifier.size(8.dp).clip(CircleShape).background(NoCap.Red))
            Box(Modifier.weight(1f).padding(start = 10.dp)) { title() }
            Text("—", Modifier.padding(horizontal = 6.dp), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted)
            Text("▢", Modifier.padding(horizontal = 6.dp), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted)
            Text(
                "✕", Modifier.padding(start = 6.dp).clip(CircleShape).then(if (onClose != null) Modifier.clickable(onClick = onClose) else Modifier).padding(4.dp),
                style = MaterialTheme.typography.labelSmall, color = if (onClose != null) NoCap.Text else NoCap.Muted,
            )
        }
        Box(Modifier.fillMaxWidth().height(1.dp).background(NoCap.GlassEdge))
        content()
    }
}

/** Per-app custom name and icon. */
@Composable
fun CustomizeApp(a: MainActivity, app: PhoneApp) {
    var label by remember(app.key) { mutableStateOf(com.nocap.link.data.Opt.customLabels.v[app.key] ?: app.label) }
    fun close() { a.customizing = null }
    androidx.compose.material3.AlertDialog(
        onDismissRequest = ::close,
        containerColor = NoCap.GlassStrong,
        title = { Text(t("Make it yours", "Customize app"), style = MaterialTheme.typography.titleMedium) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    AppIcon(app, 52.dp)
                    Spacer(Modifier.width(12.dp))
                    com.nocap.link.widget.Field(label, { label = it.take(40) }, app.label, modifier = Modifier.weight(1f))
                }
                MenuRow(t("Pick an icon from my pics", "Choose icon from gallery")) {
                    runCatching { a.pickIcon(app) }.onFailure { a.toast = t("No photo picker on this phone.", "No photo picker is available.") }
                }
                if (app.custom) MenuRow(t("Back to the original icon", "Reset icon")) {
                    com.nocap.link.data.Opt.customIcons.put(app.key, null); com.nocap.link.data.CustomIcons.prune(a); a.reloadApps()
                }
                if (com.nocap.link.data.Opt.customLabels.v.containsKey(app.key)) MenuRow(t("Back to the original name", "Reset name")) {
                    com.nocap.link.data.Opt.customLabels.put(app.key, null); a.reloadApps(); close()
                }
            }
        },
        confirmButton = {
            androidx.compose.material3.TextButton(onClick = {
                val l = label.trim()
                com.nocap.link.data.Opt.customLabels.put(app.key, l.takeIf { it.isNotBlank() && (it != app.label || app.key in com.nocap.link.data.Opt.customLabels.v) })
                a.reloadApps(); close()
            }) { Text(t("Save", "Save"), color = NoCap.Red) }
        },
        dismissButton = { androidx.compose.material3.TextButton(onClick = ::close) { Text(t("Nah", "Cancel"), color = NoCap.Text) } },
    )
}

