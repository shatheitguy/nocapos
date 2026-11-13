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
 * The NoCap control center: glass and NoCap red, quick toggles, brightness,
 * what's playing, NoCap Link tiles, and your notifications grouped by app
 * (reply and dismiss). Swipe up on it, tap outside, or press back to close.
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
        Modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.45f))
            .clickable(remember { MutableInteractionSource() }, null) { h.close() },
        contentAlignment = Alignment.TopCenter,
    ) {
        val shape = RoundedCornerShape(bottomStart = 30.dp, bottomEnd = 30.dp)
        var pull by remember { mutableFloatStateOf(0f) }
        val closeOnSwipeUp = Modifier.pointerInput(Unit) {
            detectVerticalDragGestures(onDragStart = { pull = 0f }, onDragEnd = { if (pull < -60.dp.toPx()) h.close() }) { c, dy -> c.consume(); pull += dy }
        }
        Column(
            Modifier.widthIn(max = 640.dp).fillMaxWidth()
                .glass(shape, strong = true, edge = NoCap.Red.copy(alpha = 0.45f))
                .background(Brush.verticalGradient(listOf(NoCap.Red.copy(alpha = 0.26f), Color.Transparent)))
                .clickable(remember { MutableInteractionSource() }, null) {}
                .statusBarsPadding(),
        ) {
            Header(h, closeOnSwipeUp)
            Column(
                Modifier.weight(1f, fill = false).verticalScroll(rememberScrollState()).padding(horizontal = 16.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                @Suppress("UNUSED_EXPRESSION") tick
                Toggles(h)
                BrightnessRow(h)
                Media.now?.let { MediaCard(ctx) }
                LinkRow(h)
                Notifications(h)
                Spacer(Modifier.height(4.dp))
            }
            Box(Modifier.fillMaxWidth().then(closeOnSwipeUp).padding(vertical = 12.dp), contentAlignment = Alignment.Center) {
                Box(Modifier.width(44.dp).height(5.dp).clip(RoundedCornerShape(3.dp)).background(NoCap.Text.copy(alpha = 0.35f)))
            }
        }
    }
}

@Composable
private fun Header(h: ControlHost, modifier: Modifier) {
    val ctx = h.ctx
    val now = Date()
    Row(modifier.fillMaxWidth().padding(start = 20.dp, end = 12.dp, top = 10.dp, bottom = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(DateFormat.getTimeFormat(ctx).format(now), style = MaterialTheme.typography.displaySmall, color = NoCap.Text)
            Text(DateFormat.format("EEEE, MMMM d", now).toString(), style = MaterialTheme.typography.bodySmall)
        }
        GlyphButton("⚙", null, size = 38.dp) { h.start(Intent(Settings.ACTION_SETTINGS)); h.close() }
        GlyphButton("✕", null, size = 38.dp) { h.close() }
    }
}

@Composable
private fun Tile(glyph: String, label: String, sub: String?, on: Boolean?, modifier: Modifier, onClick: () -> Unit) {
    val active = on == true
    Column(
        modifier.clip(RoundedCornerShape(18.dp))
            .background(if (active) NoCap.Red else NoCap.Field)
            .border(1.dp, if (active) NoCap.Red else NoCap.GlassEdge, RoundedCornerShape(18.dp))
            .clickable(onClick = onClick).padding(horizontal = 10.dp, vertical = 10.dp),
    ) {
        Text(glyph, style = MaterialTheme.typography.titleMedium.copy(fontFamily = Orbitron), color = if (active) Color.White else NoCap.Red, maxLines = 1)
        Text(label, style = MaterialTheme.typography.labelLarge, color = if (active) Color.White else NoCap.Text, maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (sub != null) Text(sub, style = MaterialTheme.typography.labelSmall, color = if (active) Color.White.copy(alpha = 0.85f) else NoCap.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
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
    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Tile("≋", "Wi-Fi", onOff(wifi), wifi, Modifier.weight(1f)) { h.start(Switches.wifiIntent()) }
            Tile("ᛒ", "Bluetooth", onOff(bt), bt, Modifier.weight(1f)) { h.start(Switches.bluetoothIntent()) }
            Tile("✦", t("Flashlight", "Flashlight"), onOff(Switches.torch()), Switches.torch(), Modifier.weight(1f)) {
                if (!Torch.toggle(ctx)) h.toast(t("No flashlight on this phone.", "This phone has no flashlight."))
            }
        }
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Tile("☾", t("Do not disturb", "Do not disturb"), onOff(dnd), dnd, Modifier.weight(1f)) {
                if (!Switches.toggleDnd(ctx)) {
                    h.toast(t("Let NoCap OS flip Do not disturb, then tap again.", "Allow Do not disturb access for NoCap OS, then tap again."))
                    h.start(Intent(Settings.ACTION_NOTIFICATION_POLICY_ACCESS_SETTINGS))
                }
                tick++
            }
            Tile("⟳", t("Rotation lock", "Rotation lock"), onOff(rot), rot, Modifier.weight(1f)) {
                if (!Switches.toggleRotation(ctx)) needWrite()
                tick++
            }
            Tile("⚙", t("All settings", "Settings"), null, null, Modifier.weight(1f)) { h.start(Intent(Settings.ACTION_SETTINGS)); h.close() }
        }
    }
}

@Composable
private fun BrightnessRow(h: ControlHost) {
    val ctx = h.ctx
    var level by remember { mutableFloatStateOf(Switches.brightness(ctx)) }
    var asked by remember { mutableStateOf(false) }
    Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(18.dp)).background(NoCap.Field).padding(horizontal = 14.dp), verticalAlignment = Alignment.CenterVertically) {
        Text("☀", style = MaterialTheme.typography.titleMedium, color = NoCap.Red)
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
            modifier = Modifier.weight(1f).padding(start = 10.dp),
            colors = SliderDefaults.colors(thumbColor = NoCap.Red, activeTrackColor = NoCap.Red, inactiveTrackColor = NoCap.Text.copy(alpha = 0.15f)),
        )
    }
}

@Composable
private fun MediaCard(ctx: Context) {
    val n = Media.now ?: return
    val art = remember(n.art) { n.art?.asImageBitmap() }
    Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(18.dp)).background(NoCap.Field).clickable { Media.open(ctx) }.padding(10.dp), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.size(52.dp).clip(RoundedCornerShape(12.dp)).background(Brush.linearGradient(listOf(NoCap.Red.copy(alpha = 0.5f), Color(0xFF14161C)))), contentAlignment = Alignment.Center) {
            if (art != null) Image(art, null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop) else Text("♪", color = Color.White)
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
    Label("NoCap Link")
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Tile("⧉", t("Clipboard", "Clipboard"), t("Send to NoCap", "Send to NoCap"), null, Modifier.weight(1f)) {
            Sync.sendClipboard(ctx, force = true) { _, m -> h.toast(m) }
        }
        Tile("⌖", t("Find my phone", "Find my phone"), null, null, Modifier.weight(1f)) {
            val f = LinkTiles.findMyPhone
            if (f == null || !f.open(ctx)) h.toast(t("Find my phone drops with a NoCap Link update.", "Find my phone isn't available in this version yet."))
        }
    }
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(18.dp)).background(NoCap.Field).clickable { h.openHub() }.padding(horizontal = 14.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        val color = when (line.ok) { true -> NoCap.Ok; false -> NoCap.Warn; null -> NoCap.Muted }
        Box(Modifier.size(10.dp).clip(CircleShape).background(color))
        Text(line.text, Modifier.padding(start = 10.dp).weight(1f), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
        Text("›", style = MaterialTheme.typography.titleMedium, color = NoCap.Red)
    }
}

@Composable
private fun Notifications(h: ControlHost) {
    val ctx = h.ctx
    val allowed = remember { DotsService.allowed(ctx) }
    val changes = DotsService.changes
    val groups = remember(changes) { Notifs.group(Notifs.active(ctx).map(Notifs::item)) }
    Row(verticalAlignment = Alignment.CenterVertically) {
        Label(t("Notifs", "Notifications"), Modifier.weight(1f))
        if (groups.any { g -> g.items.any { it.clearable } }) {
            Text(t("Clear all", "Clear all"), Modifier.clip(RoundedCornerShape(8.dp)).clickable { Notifs.dismissAll() }.padding(6.dp), style = MaterialTheme.typography.labelLarge, color = NoCap.Red)
        }
    }
    if (!allowed) {
        Text(t("Allow notification access to see them here.", "Allow notification access to show notifications here."), style = MaterialTheme.typography.bodySmall)
        Text(
            t("Allow", "Allow"), Modifier.clip(RoundedCornerShape(12.dp)).background(NoCap.Red).clickable { DotsService.openSettings(ctx) }.padding(horizontal = 14.dp, vertical = 8.dp),
            style = MaterialTheme.typography.labelLarge, color = Color.White,
        )
        return
    }
    if (groups.isEmpty()) {
        Text(t("No notifs. Peace and quiet.", "No notifications."), style = MaterialTheme.typography.bodySmall)
        return
    }
    var replyTo by remember { mutableStateOf<String?>(null) }
    groups.take(12).forEach { g ->
        Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(18.dp)).background(NoCap.Field).padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Text(Notifs.appName(ctx, g.pkg) + if (g.items.size > 1) " · ${g.items.size}" else "", style = MaterialTheme.typography.labelMedium, color = NoCap.Red)
            g.items.take(5).forEach { n ->
                NotifRow(h, n, replyTo == n.key, onReply = { replyTo = if (replyTo == n.key) null else n.key }, onSent = { replyTo = null })
            }
        }
    }
}

@Composable
private fun NotifRow(h: ControlHost, n: Notifs.Item, replying: Boolean, onReply: () -> Unit, onSent: () -> Unit) {
    val ctx = h.ctx
    Column(Modifier.fillMaxWidth()) {
        Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { if (Notifs.open(ctx, n.key)) h.close() }.padding(vertical = 2.dp)) {
            if (n.title.isNotBlank()) Text(n.title, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (n.text.isNotBlank()) Text(n.text, style = MaterialTheme.typography.bodySmall, maxLines = 3, overflow = TextOverflow.Ellipsis)
        }
        Row(horizontalArrangement = Arrangement.spacedBy(14.dp)) {
            if (n.canReply) Text(t("Reply", "Reply"), Modifier.clip(RoundedCornerShape(8.dp)).clickable(onClick = onReply).padding(vertical = 4.dp), style = MaterialTheme.typography.labelLarge, color = NoCap.Red)
            if (n.clearable) Text(t("Yeet", "Dismiss"), Modifier.clip(RoundedCornerShape(8.dp)).clickable { Notifs.dismiss(n.key) }.padding(vertical = 4.dp), style = MaterialTheme.typography.labelLarge, color = NoCap.Muted)
        }
        if (replying) {
            var text by remember { mutableStateOf("") }
            Row(Modifier.fillMaxWidth().padding(top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.weight(1f).heightIn(min = 40.dp).glass(RoundedCornerShape(20.dp)).padding(horizontal = 14.dp, vertical = 10.dp)) {
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
