package com.nocap.link.control

import android.content.Context
import android.content.Intent
import android.provider.Settings
import android.text.format.DateFormat
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.Image
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.nocap.link.data.t
import com.nocap.link.MainActivity
import com.nocap.link.Layer
import com.nocap.link.ServerState
import com.nocap.link.sync.Sync
import com.nocap.link.system.DotsService
import com.nocap.link.ui.glass
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.Orbitron
import com.nocap.link.widget.GlyphButton
import com.nocap.link.widget.Media
import com.nocap.link.widget.Torch
import java.util.Date
import kotlinx.coroutines.delay
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.runtime.produceState
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.core.graphics.drawable.toBitmap
import com.nocap.link.R
import com.nocap.link.ui.GlassLevel
import com.nocap.link.ui.NcIcon
import com.nocap.link.ui.PillButton
import com.nocap.link.ui.Shell
import com.nocap.link.ui.ShellAvatar
import com.nocap.link.ui.ShellIconButton
import com.nocap.link.ui.shellGlass
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** What the control center needs from the screen showing it (the launcher, or its own see-through screen over any app). */
interface ControlHost {
    val ctx: Context
    fun toast(msg: String)
    fun close()
    fun openHub()
    /** The server's status line; read from Compose, so it may use state. */
    fun serverLine(): ServerGlance.Line

    fun start(i: Intent) {
        runCatching { ctx.startActivity(i.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
            .onFailure { toast(t("Your phone wouldn't open that.", "That couldn't be opened.")) }
    }
}

/** The control center inside the launcher (swipe down on the home screen). */
class LauncherHost(private val a: MainActivity) : ControlHost {
    override val ctx: Context get() = a
    override fun toast(msg: String) { a.toast = msg }
    override fun close() { a.layer = Layer.NONE }
    override fun openHub() { a.layer = Layer.HUB }
    override fun serverLine(): ServerGlance.Line = when (val s = a.server) {
        is ServerState.Online -> {
            val name = s.me.server.name.ifBlank { "NoCapOS" }
            val sum = a.summary
            ServerGlance.describe(name, System.currentTimeMillis(), System.currentTimeMillis(), sum?.cpuPercent?.toInt() ?: 0, sum?.updates ?: 0)
        }
        ServerState.NotLinked -> ServerGlance.Line(t("Not linked yet", "Not linked"), null)
        ServerState.Checking -> ServerGlance.Line(t("Checking in…", "Connecting…"), null)
        is ServerState.Offline -> ServerGlance.Line(t("Can't reach your server", "Server offline"), false)
        is ServerState.CertChanged -> ServerGlance.Line(t("Certificate changed: check it", "Certificate changed"), false)
        ServerState.Unlinked -> ServerGlance.Line(t("Your server forgot this phone", "No longer linked"), false)
    }
}

/**
 * The NoCap control center, like the web OS Control Center and notification
 * center: a frosted popover with the clock, round-icon tiles in a 2-column
 * grid, the brightness slider panel, what's playing, NoCap Link, and your
 * notifications as cards grouped by app (reply and dismiss). Swipe up on it,
 * tap outside, or press back to close.
 */
@Composable
fun ControlCenter(h: ControlHost) {
    val ctx = h.ctx
    var tick by remember { mutableIntStateOf(0) }
    LaunchedEffect(Unit) {
        Media.start(ctx)
        while (true) { delay(2000); tick++ }
    }
    Box(
        Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.4f))
            .clickable(remember { MutableInteractionSource() }, null) { h.close() },
        contentAlignment = Alignment.TopCenter,
    ) {
        val shape = RoundedCornerShape(24.dp)
        var pull by remember { mutableFloatStateOf(0f) }
        val closeOnSwipeUp = Modifier.pointerInput(Unit) {
            detectVerticalDragGestures(onDragStart = { pull = 0f }, onDragEnd = { if (pull < -60.dp.toPx()) h.close() }) { c, dy -> c.consume(); pull += dy }
        }
        Column(
            Modifier.statusBarsPadding().navigationBarsPadding().padding(horizontal = 10.dp, vertical = 8.dp).widthIn(max = 520.dp).fillMaxWidth()
                .shellGlass(shape, GlassLevel.MENU)
                .clickable(remember { MutableInteractionSource() }, null) {},
        ) {
            Header(h, closeOnSwipeUp)
            Column(
                Modifier.weight(1f, fill = false).verticalScroll(rememberScrollState()).padding(horizontal = 14.dp),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                @Suppress("UNUSED_EXPRESSION") tick
                Toggles(h)
                Sliders(h)
                Media.now?.let { MediaCard(ctx) }
                LinkRow(h)
                Notifications(h)
                Spacer(Modifier.height(4.dp))
            }
            Box(Modifier.fillMaxWidth().then(closeOnSwipeUp).padding(vertical = 10.dp), contentAlignment = Alignment.Center) {
                Box(Modifier.width(40.dp).height(5.dp).clip(RoundedCornerShape(3.dp)).background(NoCap.Text.copy(alpha = 0.3f)))
            }
        }
    }
}

/** The clock and date (the web's clock panel), with Settings and close. */
@Composable
private fun Header(h: ControlHost, modifier: Modifier) {
    val ctx = h.ctx
    val now = Date()
    Row(modifier.fillMaxWidth().padding(start = 16.dp, end = 12.dp, top = 14.dp, bottom = 10.dp), verticalAlignment = Alignment.CenterVertically) {
        ShellAvatar(android.os.Build.MODEL ?: "Phone", 42.dp)
        Column(Modifier.weight(1f).padding(start = 12.dp)) {
            Text(DateFormat.getTimeFormat(ctx).format(now), style = MaterialTheme.typography.titleMedium.copy(fontSize = 26.sp, fontWeight = FontWeight.Bold), color = NoCap.Text, maxLines = 1)
            Text(DateFormat.format("EEEE, d MMMM", now).toString(), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted, maxLines = 1)
        }
        ShellIconButton("settings", t("Phone settings", "Settings"), size = 36.dp) { h.start(Intent(Settings.ACTION_SETTINGS)); h.close() }
        Spacer(Modifier.width(6.dp))
        ShellIconButton("close", t("Close", "Close"), size = 36.dp) { h.close() }
    }
}

/**
 * A Control Center tile (.cc-tile): round icon (accent when on), title and
 * status; on tiles get the accent tint and edge.
 */
@Composable
private fun Tile(icon: String, label: String, sub: String?, on: Boolean?, modifier: Modifier, onClick: () -> Unit) {
    val active = on == true
    val shape = RoundedCornerShape(14.dp)
    val red = NoCap.Red
    Row(
        modifier.heightIn(min = 52.dp).clip(shape)
            .background(if (active) red.copy(alpha = 0.16f).compositeOver(Shell.hover) else Shell.hover)
            .border(1.dp, if (active) red.copy(alpha = 0.45f) else Shell.edge, shape)
            .clickable(onClick = onClick).padding(start = 10.dp, end = 8.dp, top = 9.dp, bottom = 9.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            Modifier.size(30.dp).clip(CircleShape).background(if (active) red else NoCap.Text.copy(alpha = 0.1f)),
            contentAlignment = Alignment.Center,
        ) { NcIcon(icon, if (active) Color.White else NoCap.Text, 16.dp) }
        Column(Modifier.padding(start = 9.dp).weight(1f)) {
            Text(label, style = MaterialTheme.typography.labelLarge.copy(fontSize = 14.sp, fontWeight = FontWeight.SemiBold, letterSpacing = 0.sp), color = NoCap.Text, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (sub != null) Text(sub, style = MaterialTheme.typography.labelSmall.copy(fontSize = 12.sp, fontWeight = FontWeight.Normal), color = NoCap.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

private fun Color.compositeOver(bg: Color): Color {
    val a = alpha
    val outA = a + bg.alpha * (1 - a)
    if (outA <= 0f) return Color.Transparent
    return Color((red * a + bg.red * bg.alpha * (1 - a)) / outA, (green * a + bg.green * bg.alpha * (1 - a)) / outA, (blue * a + bg.blue * bg.alpha * (1 - a)) / outA, outA)
}

private fun onOff(v: Boolean?) = when (v) {
    true -> t("On", "On")
    false -> t("Off", "Off")
    null -> null
}

@Composable
private fun Toggles(h: ControlHost) {
    val ctx = h.ctx
    var tick by remember { mutableIntStateOf(0) }
    @Suppress("UNUSED_EXPRESSION") tick
    fun needWrite() {
        h.toast(t("Allow NoCap OS to change system settings, then tap again.", "Allow “Modify system settings” for NoCap OS, then tap again."))
        h.start(Switches.writeSettingsIntent(ctx))
    }
    val wifi = Switches.wifi(ctx)
    val bt = Switches.bluetooth(ctx)
    val dnd = Switches.dnd(ctx)
    val rot = Switches.rotationLocked(ctx)
    val torch = Switches.torch()
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Tile("wifi", "Wi-Fi", onOff(wifi), wifi, Modifier.weight(1f)) { h.start(Switches.wifiIntent()) }
            Tile("devices", "Bluetooth", onOff(bt), bt, Modifier.weight(1f)) { h.start(Switches.bluetoothIntent()) }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Tile("moon", t("Do not disturb", "Do not disturb"), onOff(dnd), dnd, Modifier.weight(1f)) {
                if (!Switches.toggleDnd(ctx)) {
                    h.toast(t("Let NoCap OS flip Do not disturb, then tap again.", "Allow Do not disturb access for NoCap OS, then tap again."))
                    h.start(Intent(Settings.ACTION_NOTIFICATION_POLICY_ACCESS_SETTINGS))
                }
                tick++
            }
            Tile("sun", t("Flashlight", "Flashlight"), onOff(torch), torch, Modifier.weight(1f)) {
                if (!Torch.toggle(ctx)) h.toast(t("No flashlight on this phone.", "This phone has no flashlight."))
            }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Tile("rotate", t("Rotation lock", "Rotation lock"), onOff(rot), rot, Modifier.weight(1f)) {
                if (!Switches.toggleRotation(ctx)) needWrite()
                tick++
            }
            Tile("settings", t("All settings", "Settings"), null, null, Modifier.weight(1f)) { h.start(Intent(Settings.ACTION_SETTINGS)); h.close() }
        }
    }
}

/** The web's slider panel (.cc-sliders): label, a round icon and the track. */
@Composable
private fun Sliders(h: ControlHost) {
    val ctx = h.ctx
    var level by remember { mutableFloatStateOf(Switches.brightness(ctx)) }
    var asked by remember { mutableStateOf(false) }
    val shape = RoundedCornerShape(14.dp)
    Column(Modifier.fillMaxWidth().clip(shape).background(Shell.hover).border(1.dp, Shell.edge, shape).padding(12.dp)) {
        Text(t("Brightness", "Brightness"), style = MaterialTheme.typography.labelMedium.copy(fontSize = 13.sp, letterSpacing = 0.sp), color = NoCap.Text)
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(30.dp).clip(CircleShape).background(NoCap.Text.copy(alpha = 0.1f)), contentAlignment = Alignment.Center) {
                NcIcon(if (level < 0.4f) "moon" else "sun", NoCap.Text, 16.dp)
            }
            Slider(
                level,
                onValueChange = { v ->
                    level = v
                    if (!Switches.setBrightness(ctx, v) && !asked) {
                        asked = true
                        h.toast(t("Allow NoCap OS to change system settings to set brightness.", "Allow “Modify system settings” for NoCap OS to change brightness."))
                        h.start(Switches.writeSettingsIntent(ctx))
                    }
                },
                modifier = Modifier.weight(1f).padding(horizontal = 8.dp),
                colors = SliderDefaults.colors(thumbColor = Color.White, activeTrackColor = NoCap.Red, inactiveTrackColor = NoCap.Text.copy(alpha = 0.15f)),
            )
            Text("${(level * 100).toInt()}%", Modifier.widthIn(min = 36.dp), style = MaterialTheme.typography.labelSmall.copy(fontSize = 12.sp), color = NoCap.Muted)
        }
    }
}

@Composable
private fun MediaCard(ctx: Context) {
    val n = Media.now ?: return
    val art = remember(n.art) { n.art?.asImageBitmap() }
    val shape = RoundedCornerShape(14.dp)
    Row(Modifier.fillMaxWidth().clip(shape).background(Shell.hover).border(1.dp, Shell.edge, shape).clickable { Media.open(ctx) }.padding(10.dp), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.size(48.dp).clip(RoundedCornerShape(11.dp)).background(Brush.linearGradient(listOf(NoCap.Red.copy(alpha = 0.5f), Color(0xFF14161C)))), contentAlignment = Alignment.Center) {
            if (art != null) Image(art, null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop) else NcIcon("music", Color.White, 20.dp)
        }
        Column(Modifier.weight(1f).padding(horizontal = 10.dp)) {
            Text(n.title.ifBlank { t("Something's playing", "Now playing") }, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(listOf(n.artist, n.app).filter { it.isNotBlank() }.joinToString(" · "), style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        GlyphButton("⏮", null, size = 32.dp) { Media.prev() }
        GlyphButton(if (n.playing) "❚❚" else "▶", null, on = true, size = 40.dp) { Media.toggle() }
        GlyphButton("⏭", null, size = 32.dp) { Media.next() }
    }
}

@Composable
private fun LinkRow(h: ControlHost) {
    val ctx = h.ctx
    val line = h.serverLine()
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Tile("clipboard", t("Clipboard", "Clipboard"), t("Send to NoCap", "Send to NoCap"), null, Modifier.weight(1f)) {
            Sync.sendClipboard(ctx, force = true) { _, m -> h.toast(m) }
        }
        Tile("mapPin", t("Find my phone", "Find my phone"), null, null, Modifier.weight(1f)) {
            val f = LinkTiles.findMyPhone
            if (f == null || !f.open(ctx)) h.toast(t("Find my phone drops with a NoCap Link update.", "Find my phone isn't available in this version yet."))
        }
    }
    val shape = RoundedCornerShape(14.dp)
    Row(
        Modifier.fillMaxWidth().clip(shape).background(Shell.hover).border(1.dp, Shell.edge, shape).clickable { h.openHub() }.padding(horizontal = 12.dp, vertical = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        val color = when (line.ok) { true -> NoCap.Ok; false -> NoCap.Warn; null -> NoCap.Muted }
        Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(22.dp))
        Column(Modifier.padding(start = 10.dp).weight(1f)) {
            Text("NoCap Link", style = MaterialTheme.typography.labelLarge.copy(fontSize = 14.sp, letterSpacing = 0.sp), color = NoCap.Text)
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.size(7.dp).clip(CircleShape).background(color))
                Text(line.text, Modifier.padding(start = 6.dp), style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
        NcIcon("chevronRight", NoCap.Muted, 18.dp)
    }
}

/** App icons for notification cards, loaded off the main thread and kept for the session. */
private object NotifIcons {
    private val cache = java.util.concurrent.ConcurrentHashMap<String, ImageBitmap>()

    fun cached(pkg: String): ImageBitmap? = cache[pkg]

    fun load(ctx: Context, pkg: String): ImageBitmap? = cache[pkg] ?: runCatching {
        val px = (32 * ctx.resources.displayMetrics.density).toInt().coerceAtLeast(16)
        ctx.packageManager.getApplicationIcon(pkg).toBitmap(px, px).asImageBitmap()
    }.getOrNull()?.also { if (cache.size < 64) cache[pkg] = it }
}

@Composable
private fun NotifIcon(ctx: Context, pkg: String) {
    val icon by produceState(NotifIcons.cached(pkg), pkg) { if (value == null) value = withContext(Dispatchers.IO) { NotifIcons.load(ctx, pkg) } }
    val shape = RoundedCornerShape(8.dp)
    val i = icon
    if (i != null) Image(i, null, Modifier.size(32.dp).clip(shape))
    else Box(Modifier.size(32.dp).clip(shape).background(NoCap.Text.copy(alpha = 0.1f)), contentAlignment = Alignment.Center) { NcIcon("bell", NoCap.Muted, 16.dp) }
}

/** "now", "5m", "2h", "3d", like the web's notification times. */
internal fun ago(time: Long, now: Long = System.currentTimeMillis()): String {
    val s = ((now - time) / 1000).coerceAtLeast(0)
    return when {
        time <= 0L -> ""
        s < 60 -> t("now", "now")
        s < 3600 -> "${s / 60}m"
        s < 86_400 -> "${s / 3600}h"
        else -> "${s / 86_400}d"
    }
}

@Composable
private fun Notifications(h: ControlHost) {
    val ctx = h.ctx
    val allowed = remember { DotsService.allowed(ctx) }
    val changes = DotsService.changes
    val groups = remember(changes) { Notifs.group(Notifs.active(ctx).map(Notifs::item)) }
    Row(Modifier.padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(t("Notifs", "Notifications"), Modifier.weight(1f), style = MaterialTheme.typography.titleSmall.copy(fontWeight = FontWeight.Bold), color = NoCap.Text)
        if (groups.any { g -> g.items.any { it.clearable } }) {
            Text(
                t("Clear all", "Clear all"),
                Modifier.clip(RoundedCornerShape(50)).background(Shell.hover).clickable { Notifs.dismissAll() }.padding(horizontal = 12.dp, vertical = 6.dp),
                style = MaterialTheme.typography.labelMedium.copy(letterSpacing = 0.sp), color = NoCap.Text,
            )
        }
    }
    if (!allowed) {
        Text(t("Allow notification access to see them here.", "Allow notification access to show notifications here."), style = MaterialTheme.typography.bodySmall)
        PillButton(t("Allow", "Allow"), { DotsService.openSettings(ctx) })
        return
    }
    if (groups.isEmpty()) {
        Box(Modifier.fillMaxWidth().clip(RoundedCornerShape(18.dp)).background(Shell.hover).padding(vertical = 18.dp), contentAlignment = Alignment.Center) {
            Text(t("No notifs. Peace and quiet.", "No notifications."), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
        }
        return
    }
    var replyTo by remember { mutableStateOf<String?>(null) }
    var expanded by remember { mutableStateOf(setOf<String>()) }
    groups.take(12).forEach { g ->
        val open = g.pkg in expanded || g.items.size == 1
        val shown = if (open) g.items.take(6) else g.items.take(1)
        Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
            if (g.items.size > 1) {
                Row(Modifier.fillMaxWidth().padding(horizontal = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(Notifs.appName(ctx, g.pkg), Modifier.weight(1f), style = MaterialTheme.typography.labelMedium.copy(letterSpacing = 0.sp), color = NoCap.Muted, maxLines = 1)
                    Text(
                        if (open) t("Show less", "Show less") else t("+${g.items.size - 1} more", "${g.items.size - 1} more"),
                        Modifier.clip(RoundedCornerShape(50)).clickable { expanded = if (open) expanded - g.pkg else expanded + g.pkg }.padding(horizontal = 8.dp, vertical = 2.dp),
                        style = MaterialTheme.typography.labelMedium.copy(letterSpacing = 0.sp), color = NoCap.Red,
                    )
                }
            }
            shown.forEachIndexed { i, n ->
                NotifCard(h, g.pkg, n, stacked = !open && i == 0 && g.items.size > 1, replying = replyTo == n.key,
                    onReply = { replyTo = if (replyTo == n.key) null else n.key }, onSent = { replyTo = null })
            }
        }
    }
}

/** One notification card (.nc-card): app icon, title and time, the text, then Reply / Dismiss. */
@Composable
private fun NotifCard(h: ControlHost, pkg: String, n: Notifs.Item, stacked: Boolean, replying: Boolean, onReply: () -> Unit, onSent: () -> Unit) {
    val ctx = h.ctx
    val shape = RoundedCornerShape(18.dp)
    Box {
        // A collapsed group shows a peek of the card under it.
        if (stacked) Box(Modifier.matchParentSize().padding(horizontal = 10.dp).offset(y = 6.dp).clip(shape).background(Shell.hover))
        Column(Modifier.fillMaxWidth().clip(shape).background(Shell.tint(GlassLevel.SHELF)).border(0.5.dp, Shell.edge, shape).padding(start = 12.dp, end = 14.dp, top = 11.dp, bottom = 11.dp)) {
            Row(Modifier.fillMaxWidth().clickable(remember { MutableInteractionSource() }, null) { if (Notifs.open(ctx, n.key)) h.close() }, verticalAlignment = Alignment.CenterVertically) {
                NotifIcon(ctx, pkg)
                Column(Modifier.weight(1f).padding(start = 10.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text(n.title.ifBlank { Notifs.appName(ctx, pkg) }, Modifier.weight(1f), style = MaterialTheme.typography.titleSmall.copy(fontSize = 14.sp), color = NoCap.Text, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(ago(n.time), Modifier.padding(start = 6.dp), style = MaterialTheme.typography.labelSmall.copy(fontSize = 12.sp, fontWeight = FontWeight.Normal), color = NoCap.Muted)
                    }
                    if (n.text.isNotBlank()) Text(n.text, style = MaterialTheme.typography.bodySmall.copy(fontSize = 13.sp), color = NoCap.Text.copy(alpha = 0.84f), maxLines = 4, overflow = TextOverflow.Ellipsis)
                }
            }
            if (n.canReply || n.clearable) {
                Row(Modifier.padding(start = 42.dp, top = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    if (n.canReply) SmallPill(t("Reply", "Reply"), onReply)
                    if (n.clearable) SmallPill(t("Yeet", "Dismiss")) { Notifs.dismiss(n.key) }
                }
            }
            if (replying) {
                var text by remember { mutableStateOf("") }
                Row(Modifier.fillMaxWidth().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                    Box(Modifier.weight(1f).heightIn(min = 40.dp).clip(RoundedCornerShape(20.dp)).background(Shell.hover).border(1.dp, Shell.edge, RoundedCornerShape(20.dp)).padding(horizontal = 14.dp, vertical = 10.dp)) {
                        if (text.isEmpty()) Text(t("Say something…", "Type a reply"), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
                        BasicTextField(text, { text = it }, Modifier.fillMaxWidth(), textStyle = MaterialTheme.typography.bodyMedium.copy(color = NoCap.Text), cursorBrush = SolidColor(NoCap.Red))
                    }
                    Spacer(Modifier.width(8.dp))
                    GlyphButton("➤", null, on = true, size = 40.dp) {
                        if (text.isBlank()) return@GlyphButton
                        if (Notifs.reply(ctx, n.key, text.trim())) { h.toast(t("Sent.", "Reply sent.")); onSent() } else h.toast(t("That app didn't take the reply.", "The reply couldn't be sent."))
                    }
                }
            }
        }
    }
}

@Composable
private fun SmallPill(text: String, onClick: () -> Unit) {
    Text(
        text, Modifier.clip(RoundedCornerShape(50)).background(Shell.hover).clickable(onClick = onClick).padding(horizontal = 12.dp, vertical = 5.dp),
        style = MaterialTheme.typography.labelMedium.copy(letterSpacing = 0.sp), color = NoCap.Text,
    )
}
