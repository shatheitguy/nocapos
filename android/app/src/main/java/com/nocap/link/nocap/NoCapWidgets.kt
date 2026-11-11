package com.nocap.link.nocap

import android.os.Build
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.BoxWithConstraintsScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.nocap.link.Layer
import com.nocap.link.MainActivity
import com.nocap.link.Screen
import com.nocap.link.ServerState
import com.nocap.link.launcher.AppTile
import com.nocap.link.launcher.NoCapItem
import com.nocap.link.launcher.NoCapKinds
import com.nocap.link.launcher.human
import com.nocap.link.t
import com.nocap.link.ui.Chip
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.Orbitron
import com.nocap.link.ui.glass
import kotlinx.coroutines.delay

/** NoCap OS's own home-screen widgets, styled like the NoCapOS desktop's. */
@Composable
fun NoCapWidget(a: MainActivity, item: NoCapItem) {
    when (item.kind) {
        NoCapKinds.CLOCK -> ClockWidget(a, item)
        NoCapKinds.WEATHER -> WeatherWidget(a, item)
        NoCapKinds.CALENDAR -> CalendarWidget(a, item)
        NoCapKinds.STATUS -> StatusWidget(a, item, item.opt("show", "both"))
        NoCapKinds.SERVER -> StatusWidget(a, item, "server")
        NoCapKinds.PHONE -> StatusWidget(a, item, "phone")
        NoCapKinds.STORAGE -> StorageWidget(a, item)
        NoCapKinds.MUSIC -> MusicWidget(a, item)
        NoCapKinds.TOGGLES -> TogglesWidget(a, item)
        NoCapKinds.NOTES -> NotesWidget(a, item)
        NoCapKinds.PHOTO -> PhotoWidget(a, item)
        NoCapKinds.APPS -> AppsWidget(a, item)
        NoCapKinds.SEARCH -> SearchWidget(a, item)
        NoCapKinds.QUICK -> QuickWidget(a, item)
        NoCapKinds.AI -> AiWidget(a, item)
        NoCapKinds.FILES -> FilesWidget(a, item)
        NoCapKinds.TIMER -> TimerWidget(a, item)
        else -> WSurface(item, null) { Text(item.kind, style = MaterialTheme.typography.bodySmall) }
    }
}

// ---------- shared pieces ----------

/** A widget's panel: NoCap glass (default), solid, or nothing; optional accent glow. */
/** The space a widget's content has (usable from nested rows and columns). */
class WScope(val maxWidth: Dp, val maxHeight: Dp)

@Composable
fun WSurface(item: NoCapItem, onClick: (() -> Unit)?, pad: Dp = 12.dp, content: @Composable WScope.() -> Unit) {
    val shape = RoundedCornerShape(22.dp)
    val accent = NoCap.Red
    val glow = item.flag("glow", true)
    val base = when (item.opt("bg", "glass")) {
        "solid" -> Modifier.clip(shape).background(NoCap.Card).border(1.dp, NoCap.GlassEdge, shape)
        "clear" -> Modifier
        else -> Modifier.glass(shape, edge = if (glow) accent.copy(alpha = 0.32f) else NoCap.GlassEdge)
    }
    val glowMod = if (glow && item.opt("bg", "glass") != "clear") Modifier.drawBehind {
        drawRect(Brush.radialGradient(listOf(accent.copy(alpha = 0.16f), Color.Transparent), center = Offset(0f, 0f), radius = size.maxDimension * 0.8f))
    } else Modifier
    BoxWithConstraints(
        Modifier.fillMaxSize().then(base).then(glowMod).then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier).padding(pad),
    ) { WScope(maxWidth, maxHeight).content() }
}

/** A widget title like the desktop's: red dot + Orbitron caps. */
@Composable
fun WTitle(text: String, modifier: Modifier = Modifier, trailing: @Composable (() -> Unit)? = null) {
    Row(modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.size(6.dp).clip(CircleShape).background(NoCap.Red))
        Text(
            text.uppercase(), Modifier.weight(1f).padding(start = 7.dp),
            style = MaterialTheme.typography.labelMedium.copy(fontFamily = Orbitron, fontSize = 9.sp, letterSpacing = 1.8.sp),
            color = NoCap.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis,
        )
        trailing?.invoke()
    }
}

fun levelColor(percent: Double): Color = when { percent >= 90 -> NoCap.Bad; percent >= 75 -> NoCap.Warn; else -> NoCap.Red }

/** An animated progress ring with a slowly turning highlight. */
@Composable
fun Ring(percent: Double, size: Dp, label: String? = null, sub: String? = null, color: Color = levelColor(percent), center: String? = null) {
    val p by animateFloatAsState((percent / 100.0).toFloat().coerceIn(0f, 1f), tween(900), label = "ring")
    val spin = rememberInfiniteTransition(label = "spin").animateFloat(0f, 360f, infiniteRepeatable(tween(6000, easing = LinearEasing)), label = "spin")
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Box(contentAlignment = Alignment.Center) {
            Canvas(Modifier.size(size)) {
                val sw = (size.toPx() * 0.1f).coerceAtLeast(3.dp.toPx())
                val inset = sw / 2
                val arc = Size(this.size.width - sw, this.size.height - sw)
                drawArc(Color.White.copy(alpha = 0.09f), 0f, 360f, false, Offset(inset, inset), arc, style = Stroke(sw))
                drawArc(color.copy(alpha = 0.25f), -90f, 360f * p, false, Offset(inset, inset), arc, style = Stroke(sw * 2.2f, cap = StrokeCap.Round))
                drawArc(Brush.sweepGradient(listOf(color.copy(alpha = 0.55f), color, color.copy(alpha = 0.55f))), -90f, 360f * p, false, Offset(inset, inset), arc, style = Stroke(sw, cap = StrokeCap.Round))
                if (p > 0.02f) {
                    val ang = Math.toRadians((-90 + 360 * p * ((spin.value % 360) / 360f)).toDouble())
                    val r = (this.size.width - sw) / 2
                    drawCircle(Color.White.copy(alpha = 0.7f), sw * 0.32f, Offset(this.size.width / 2 + (r * kotlin.math.cos(ang)).toFloat(), this.size.height / 2 + (r * kotlin.math.sin(ang)).toFloat()))
                }
            }
            Text(center ?: "${percent.toInt()}%", style = MaterialTheme.typography.labelMedium.copy(fontSize = (size.value * 0.22f).coerceIn(9f, 16f).sp, fontWeight = FontWeight.Bold), maxLines = 1)
        }
        if (label != null) Text(label, style = MaterialTheme.typography.labelSmall, color = NoCap.Muted, maxLines = 1)
        if (sub != null) Text(sub, style = MaterialTheme.typography.labelSmall.copy(fontSize = 10.sp), color = NoCap.Muted, maxLines = 1)
    }
}

/** A sparkline with a soft fill and a pulsing last point. */
@Composable
fun Spark(values: List<Float>, modifier: Modifier, color: Color = NoCap.Red, max: Float? = null) {
    val pulse = rememberInfiniteTransition(label = "pulse").animateFloat(0.4f, 1f, infiniteRepeatable(tween(900), RepeatMode.Reverse), label = "pulse")
    Canvas(modifier) {
        if (values.size < 2) {
            drawLine(color.copy(alpha = 0.3f), Offset(0f, size.height * 0.8f), Offset(size.width, size.height * 0.8f), 1.5.dp.toPx())
            return@Canvas
        }
        val top = (max ?: values.max()).coerceAtLeast(0.0001f)
        val step = size.width / (values.size - 1)
        val line = Path()
        values.forEachIndexed { i, v ->
            val x = i * step
            val y = size.height - (v / top).coerceIn(0f, 1f) * size.height * 0.9f
            if (i == 0) line.moveTo(x, y) else line.lineTo(x, y)
        }
        val fill = Path().apply { addPath(line); lineTo(size.width, size.height); lineTo(0f, size.height); close() }
        drawPath(fill, Brush.verticalGradient(listOf(color.copy(alpha = 0.35f), Color.Transparent)))
        drawPath(line, color, style = Stroke(2.dp.toPx(), cap = StrokeCap.Round))
        val ly = size.height - (values.last() / top).coerceIn(0f, 1f) * size.height * 0.9f
        drawCircle(color.copy(alpha = 0.3f * pulse.value), 7.dp.toPx(), Offset(size.width, ly))
        drawCircle(color, 3.dp.toPx(), Offset(size.width, ly))
    }
}

/** A thin labeled progress bar. */
@Composable
fun Meter(label: String, percent: Double, value: String, modifier: Modifier = Modifier) {
    val p by animateFloatAsState((percent / 100.0).toFloat().coerceIn(0f, 1f), tween(800), label = "meter")
    Column(modifier) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(label, Modifier.weight(1f), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted, maxLines = 1)
            Text(value, style = MaterialTheme.typography.labelSmall, maxLines = 1)
        }
        Box(Modifier.fillMaxWidth().padding(top = 3.dp).height(5.dp).clip(RoundedCornerShape(3.dp)).background(Color.White.copy(alpha = 0.09f))) {
            val c = levelColor(percent)
            Box(Modifier.fillMaxWidth(p).fillMaxHeight().clip(RoundedCornerShape(3.dp)).background(Brush.horizontalGradient(listOf(c.copy(alpha = 0.6f), c))))
        }
    }
}

fun rate(bps: Double) = human(bps.toLong()) + "/s"

fun uptime(sec: Double): String {
    val m = (sec / 60).toLong()
    val d = m / 1440
    val h = (m % 1440) / 60
    return when {
        d > 0 -> "${d}d ${h}h"
        h > 0 -> "${h}h ${m % 60}m"
        else -> "${m}m"
    }
}

@Composable
fun Pill(text: String, onClick: () -> Unit) {
    Text(
        text, Modifier.clip(RoundedCornerShape(12.dp)).background(NoCap.Red).clickable(onClick = onClick).padding(horizontal = 14.dp, vertical = 8.dp),
        style = MaterialTheme.typography.labelLarge, color = Color.White, maxLines = 1,
    )
}

/** A round glyph button (widget controls). */
@Composable
fun GlyphButton(glyph: String, label: String?, on: Boolean = false, size: Dp = 36.dp, onClick: () -> Unit) {
    Column(Modifier.clip(RoundedCornerShape(12.dp)).clickable(onClick = onClick).padding(3.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Box(
            Modifier.size(size).clip(CircleShape).background(if (on) NoCap.Red else NoCap.Red.copy(alpha = 0.16f))
                .border(1.dp, NoCap.Red.copy(alpha = if (on) 0.9f else 0.35f), CircleShape),
            contentAlignment = Alignment.Center,
        ) { Text(glyph, style = MaterialTheme.typography.labelLarge.copy(fontFamily = Orbitron, fontSize = (size.value * 0.32f).sp), color = if (on) Color.White else NoCap.Red, maxLines = 1) }
        if (label != null) Text(label, style = MaterialTheme.typography.labelSmall.copy(fontSize = 11.sp), maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

// ---------- NoCap status: server + this phone ----------

@Composable
fun StatusWidget(a: MainActivity, item: NoCapItem, show: String) {
    val ctx = LocalContext.current
    val wantPhone = show != "server"
    val wantServer = show != "phone"
    if (wantPhone) LaunchedEffect(Unit) { while (true) { DeviceStats.refresh(ctx); delay(2000) } }
    val title = when (show) {
        "server" -> t("Server Vibes", "Server status")
        "phone" -> t("This Phone", "This phone")
        else -> t("NoCap Status", "NoCap status")
    }
    WSurface(item, onClick = { if (wantServer) a.layer = Layer.HUB else a.openSystem(android.content.Intent.ACTION_POWER_USAGE_SUMMARY) }) {
        val wide = maxWidth >= 240.dp
        val tall = maxHeight >= 200.dp
        val tiny = maxHeight < 110.dp
        Column(Modifier.fillMaxSize()) {
            if (!tiny) WTitle(title)
            if (!tiny) Spacer(Modifier.height(6.dp))
            when {
                wantServer && wantPhone && tall -> {
                    ServerBlock(a, Modifier.weight(1f), rich = wide)
                    Box(Modifier.fillMaxWidth().padding(vertical = 6.dp).height(1.dp).background(NoCap.GlassEdge))
                    PhoneBlock(Modifier.weight(1f), rich = wide)
                }
                wantServer && wantPhone && wide -> Row(Modifier.fillMaxSize()) {
                    ServerBlock(a, Modifier.weight(1f).fillMaxHeight(), rich = false)
                    Box(Modifier.padding(horizontal = 8.dp).width(1.dp).fillMaxHeight().background(NoCap.GlassEdge))
                    PhoneBlock(Modifier.weight(1f).fillMaxHeight(), rich = false)
                }
                wantServer && wantPhone -> CompactBoth(a)
                wantServer -> ServerBlock(a, Modifier.fillMaxSize(), rich = wide, tall = tall, tiny = tiny)
                else -> PhoneBlock(Modifier.fillMaxSize(), rich = wide, tall = tall, tiny = tiny)
            }
        }
    }
}

@Composable
private fun CompactBoth(a: MainActivity) {
    val s = a.summary
    val d = DeviceStats.snap
    Row(Modifier.fillMaxSize(), horizontalArrangement = Arrangement.SpaceEvenly, verticalAlignment = Alignment.CenterVertically) {
        if (a.server is ServerState.Online && s != null && s.ready) Ring(s.cpuPercent, 52.dp, t("Server", "Server"), "CPU") else Ring(0.0, 52.dp, t("Server", "Server"), serverLabel(a).first, color = serverLabel(a).second, center = "–")
        Ring((d?.battery ?: 0).toDouble(), 52.dp, t("Phone", "Phone"), if (d?.charging == true) t("Charging", "Charging") else t("Battery", "Battery"), color = batteryColor(d))
    }
}

private fun serverLabel(a: MainActivity): Pair<String, Color> = when (val st = a.server) {
    is ServerState.Online -> if (a.outdated != null) t("Needs a Glow Up", "Update needed") to NoCap.Warn else t("Online", "Online") to NoCap.Ok
    is ServerState.Offline -> t("Offline", "Offline") to NoCap.Warn
    is ServerState.Unlinked -> t("Unlinked", "Unlinked") to NoCap.Bad
    is ServerState.CertChanged -> t("Cert changed", "Certificate changed") to NoCap.Warn
    is ServerState.NotLinked -> t("Not linked", "Not linked") to NoCap.Muted
    else -> { if (st is ServerState.Checking) t("Checking…", "Checking…") to NoCap.Muted else "" to NoCap.Muted }
}

private fun batteryColor(d: DeviceStats.Snap?): Color = when {
    d == null -> NoCap.Muted
    d.charging -> NoCap.Ok
    d.battery <= 15 -> NoCap.Bad
    d.battery <= 30 -> NoCap.Warn
    else -> NoCap.Red
}

@Composable
private fun ServerBlock(a: MainActivity, modifier: Modifier, rich: Boolean, tall: Boolean = false, tiny: Boolean = false) {
    val st = a.server
    val s = a.summary
    val (label, color) = serverLabel(a)
    Column(modifier) {
        if (st is ServerState.NotLinked) {
            Text(t("Link your NoCap server and its vitals live here.", "Link your NoCap server to see its status here."), style = MaterialTheme.typography.bodySmall, maxLines = 2)
            Spacer(Modifier.height(6.dp))
            Pill(t("Link up", "Link a server")) { a.screen = Screen.LinkForm() }
            return@Column
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(s?.server?.name?.ifBlank { null } ?: a.link?.serverName ?: "NoCapOS", Modifier.weight(1f), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if ((s?.updates ?: 0) > 0 && !tiny) {
                Text(
                    t("${s!!.updates} glow ups", "${s!!.updates} updates"), Modifier.padding(end = 6.dp).clip(RoundedCornerShape(50)).background(NoCap.Warn.copy(alpha = 0.16f)).padding(horizontal = 8.dp, vertical = 2.dp),
                    style = MaterialTheme.typography.labelSmall, color = NoCap.Warn, maxLines = 1,
                )
            }
            Chip(label, color)
        }
        if (a.outdated != null) { Text(MainActivity.needsUpdate(a.outdated), style = MaterialTheme.typography.bodySmall, color = NoCap.Warn, maxLines = 3); return@Column }
        if (st is ServerState.Offline) { Text(st.why, style = MaterialTheme.typography.bodySmall, color = NoCap.Warn, maxLines = 2); return@Column }
        if (st !is ServerState.Online || s == null || !s.ready) { Text(t("Warming up the numbers…", "Collecting metrics…"), style = MaterialTheme.typography.bodySmall); return@Column }
        if (tiny) {
            Text("CPU ${s.cpuPercent.toInt()}% · RAM ${s.memory.percent.toInt()}%", style = MaterialTheme.typography.labelSmall, color = NoCap.Muted, maxLines = 1)
            return@Column
        }
        Spacer(Modifier.height(4.dp))
        if (rich) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceAround) {
                Ring(s.cpuPercent, 48.dp, "CPU", "${s.cores} ${t("cores", "cores")}")
                Ring(s.memory.percent, 48.dp, "RAM", human(s.memory.used))
                Ring(s.storage.percent, 48.dp, t("Disk", "Storage"), "${human(s.storage.total - s.storage.used)} ${t("free", "free")}")
            }
        } else {
            Meter("CPU", s.cpuPercent, "${s.cpuPercent.toInt()}%")
            Spacer(Modifier.height(4.dp))
            Meter("RAM", s.memory.percent, "${s.memory.percent.toInt()}%")
            Spacer(Modifier.height(4.dp))
            Meter(t("Disk", "Storage"), s.storage.percent, "${s.storage.percent.toInt()}%")
        }
        if (rich || tall) {
            Spacer(Modifier.height(6.dp))
            val net = s.net
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    if (net != null) "↓ ${rate(net.first)}  ↑ ${rate(net.second)}" else t("Up ${uptime(s.uptime)}", "Up ${uptime(s.uptime)}"),
                    Modifier.weight(1f), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted, maxLines = 1,
                )
                if (net != null) Text(t("up ${uptime(s.uptime)}", "up ${uptime(s.uptime)}"), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted, maxLines = 1)
            }
            Spark(if (s.net != null) a.netHist else a.cpuHist, Modifier.fillMaxWidth().weight(1f, fill = true).padding(top = 2.dp), max = if (s.net != null) null else 100f)
        }
    }
}

@Composable
private fun PhoneBlock(modifier: Modifier, rich: Boolean, tall: Boolean = false, tiny: Boolean = false) {
    val d = DeviceStats.snap
    Column(modifier) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(Build.MODEL ?: t("This phone", "This phone"), Modifier.weight(1f), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            val (nl, nc) = when (d?.net) {
                "wifi" -> "Wi-Fi" to NoCap.Ok
                "cell" -> t("Mobile", "Mobile data") to NoCap.Ok
                "ethernet" -> "Ethernet" to NoCap.Ok
                "vpn" -> "VPN" to NoCap.Ok
                "offline" -> t("Offline", "Offline") to NoCap.Warn
                null -> "…" to NoCap.Muted
                else -> t("Online", "Online") to NoCap.Ok
            }
            Chip(nl, nc)
        }
        if (d == null) return@Column
        val batt = if (d.battery >= 0) "${d.battery}%" else "–"
        if (tiny) {
            Text("${t("Battery", "Battery")} $batt${if (d.charging) " ⚡" else ""} · RAM ${d.ramPct.toInt()}%", style = MaterialTheme.typography.labelSmall, color = NoCap.Muted, maxLines = 1)
            return@Column
        }
        Spacer(Modifier.height(4.dp))
        if (rich) {
            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceAround) {
                Ring(d.battery.toDouble().coerceAtLeast(0.0), 48.dp, t("Battery", "Battery"), if (d.charging) t("charging ⚡", "charging") else "${d.tempC.toInt()}°C", color = batteryColor(d))
                Ring(d.ramPct, 48.dp, "RAM", human(d.ramUsed))
                Ring(d.diskPct, 48.dp, t("Storage", "Storage"), "${human(d.diskTotal - d.diskUsed)} ${t("free", "free")}")
            }
        } else {
            Meter(t("Battery", "Battery") + if (d.charging) " ⚡" else "", d.battery.toDouble(), batt)
            Spacer(Modifier.height(4.dp))
            Meter("RAM", d.ramPct, "${d.ramPct.toInt()}%")
            Spacer(Modifier.height(4.dp))
            Meter(t("Storage", "Storage"), d.diskPct, "${d.diskPct.toInt()}%")
        }
        if (rich || tall) {
            Spacer(Modifier.height(6.dp))
            Text("↓ ${rate(d.down)}  ↑ ${rate(d.up)}", style = MaterialTheme.typography.labelSmall, color = NoCap.Muted, maxLines = 1)
            Spark(DeviceStats.netHist, Modifier.fillMaxWidth().weight(1f, fill = true).padding(top = 2.dp))
        }
    }
}

// ---------- storage ----------

@Composable
private fun StorageWidget(a: MainActivity, item: NoCapItem) {
    val ctx = LocalContext.current
    LaunchedEffect(Unit) { while (true) { DeviceStats.refresh(ctx); delay(10_000) } }
    val d = DeviceStats.snap
    val s = a.summary
    WSurface(item, onClick = { a.openSystem(android.provider.Settings.ACTION_INTERNAL_STORAGE_SETTINGS) }) {
        val tall = maxHeight >= 110.dp
        Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center) {
            if (tall) { WTitle(t("Storage", "Storage")); Spacer(Modifier.height(6.dp)) }
            if (d != null) Meter(t("This phone", "This phone"), d.diskPct, "${human(d.diskTotal - d.diskUsed)} ${t("free", "free")}")
            if (item.flag("server", true) && s != null && s.ready && (tall || d == null)) {
                Spacer(Modifier.height(8.dp))
                Meter(s.server.name.ifBlank { "NoCap" }, s.storage.percent, "${human(s.storage.total - s.storage.used)} ${t("free", "free")}")
            }
        }
    }
}

// ---------- NoCap Link: files, quick actions, AI, search ----------

@Composable
private fun FilesWidget(a: MainActivity, item: NoCapItem) {
    WSurface(item, null) {
        val rows = ((maxHeight - 30.dp) / 30.dp).toInt().coerceIn(1, 10)
        Column(Modifier.fillMaxSize()) {
            WTitle(t("Fresh Files", "Recent files"), Modifier.clickable { a.layer = Layer.HUB })
            Spacer(Modifier.height(6.dp))
            when {
                a.server is ServerState.NotLinked -> Text(t("Link your server to see fresh files.", "Link your server to see recent files."), style = MaterialTheme.typography.bodySmall)
                a.outdated != null -> Text(MainActivity.needsUpdate(a.outdated), style = MaterialTheme.typography.bodySmall, color = NoCap.Warn)
                a.recentFiles.isEmpty() -> Text(t("Nothing new lately.", "No recent files."), style = MaterialTheme.typography.bodySmall)
                else -> a.recentFiles.filter { !it.dir }.take(rows).forEach { f ->
                    Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).clickable { a.openFile(f) }.padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                        Box(Modifier.size(22.dp).clip(RoundedCornerShape(6.dp)).background(NoCap.Red.copy(alpha = 0.16f)), contentAlignment = Alignment.Center) {
                            Text(f.name.substringAfterLast('.', "").take(3).uppercase().ifBlank { "•" }, style = MaterialTheme.typography.labelSmall.copy(fontSize = 8.sp), color = NoCap.Red)
                        }
                        Text(f.name, Modifier.weight(1f).padding(start = 8.dp), style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text(human(f.size), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted)
                    }
                }
            }
        }
    }
}

/** NoCap Link quick actions, pick which ones in the edit sheet. */
val quickActions = listOf("desktop", "ai", "send", "files", "search", "clip", "settings", "drawer")

fun quickName(id: String) = when (id) {
    "desktop" -> t("Desktop", "Desktop")
    "ai" -> t("Ask AI", "NoCap AI")
    "send" -> t("Send", "Send file")
    "files" -> t("Files", "Files")
    "search" -> t("Search", "Search")
    "clip" -> t("Clip it", "Send clipboard")
    "settings" -> t("Settings", "Settings")
    else -> t("Roster", "Apps")
}

private fun quickGlyph(id: String) = when (id) {
    "desktop" -> "▣"; "ai" -> "AI"; "send" -> "↑"; "files" -> "▤"; "search" -> "⌕"; "clip" -> "⧉"; "settings" -> "⚙"; else -> "⋮⋮"
}

fun runQuick(a: MainActivity, id: String) {
    when (id) {
        "desktop" -> a.openDesktop()
        "ai" -> a.openAi()
        "send" -> a.pickToSend()
        "files" -> a.layer = Layer.HUB
        "search" -> a.layer = Layer.SEARCH
        "clip" -> com.nocap.link.sync.Sync.sendClipboard(a, force = true) { _, m -> a.toast = m }
        "settings" -> a.layer = Layer.SETTINGS
        else -> a.openDrawer()
    }
}

@Composable
private fun QuickWidget(a: MainActivity, item: NoCapItem) {
    val ids = item.opt("actions", "desktop,ai,send,files").split(',').filter { it in quickActions }
    WSurface(item, null, pad = 6.dp) {
        val perRow = if (maxWidth < 200.dp) 2 else 4
        Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.SpaceEvenly) {
            ids.chunked(perRow).take(if (maxHeight > 120.dp) 2 else 1).forEach { row ->
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceEvenly) {
                    row.forEach { id -> GlyphButton(quickGlyph(id), quickName(id)) { runQuick(a, id) } }
                }
            }
        }
    }
}

private val prompts = listOf(
    "How's my server doing?" to "How is my server doing?",
    "What's eating my storage?" to "What is using my storage?",
    "Any glow ups waiting?" to "Are there updates waiting?",
    "Summarize today's tea" to "Summarize today's notifications",
)

@Composable
private fun AiWidget(a: MainActivity, item: NoCapItem) {
    var q by remember { mutableStateOf("") }
    WSurface(item, null, pad = 8.dp) {
        val tall = maxHeight > 110.dp
        Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center) {
            Row(
                Modifier.fillMaxWidth().height(44.dp).clip(RoundedCornerShape(50)).background(NoCap.Field).padding(start = 14.dp, end = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text("AI", style = MaterialTheme.typography.labelLarge.copy(fontFamily = Orbitron), color = NoCap.Red)
                Box(Modifier.weight(1f).padding(horizontal = 10.dp)) {
                    if (q.isEmpty()) Text(t("Ask NoCap AI anything…", "Ask NoCap AI…"), style = MaterialTheme.typography.bodyLarge, color = NoCap.Muted, maxLines = 1)
                    BasicTextField(
                        q, { q = it }, singleLine = true, modifier = Modifier.fillMaxWidth(),
                        textStyle = MaterialTheme.typography.bodyLarge.copy(color = NoCap.Text),
                        cursorBrush = androidx.compose.ui.graphics.SolidColor(NoCap.Red),
                        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
                        keyboardActions = KeyboardActions(onSend = { a.openAi(q.trim().ifBlank { null }); q = "" }),
                    )
                }
                Box(Modifier.size(36.dp).clip(CircleShape).background(NoCap.Red).clickable { a.openAi(q.trim().ifBlank { null }); q = "" }, contentAlignment = Alignment.Center) {
                    Text("➤", color = Color.White, style = MaterialTheme.typography.labelLarge)
                }
            }
            if (tall) {
                Spacer(Modifier.height(8.dp))
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    prompts.take(if (maxWidth > 260.dp) 3 else 2).forEach { (g, s) ->
                        Text(
                            t(g, s), Modifier.weight(1f).clip(RoundedCornerShape(10.dp)).background(NoCap.Field).clickable { a.openAi(t(g, s)) }.padding(horizontal = 8.dp, vertical = 6.dp),
                            style = MaterialTheme.typography.labelSmall, maxLines = 2, overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun SearchWidget(a: MainActivity, item: NoCapItem) {
    WSurface(item, null, pad = 6.dp) {
        Row(
            Modifier.fillMaxSize().clip(RoundedCornerShape(50)).background(NoCap.Field).clickable { a.layer = Layer.SEARCH }.padding(start = 14.dp, end = 6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            androidx.compose.foundation.Image(androidx.compose.ui.res.painterResource(com.nocap.link.R.drawable.nocap_mark), null, Modifier.size(20.dp))
            Text(t("Search apps, files, settings…", "Search apps, files and settings"), Modifier.weight(1f).padding(start = 10.dp), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (item.flag("mic", true)) GlyphButton("◉", null, size = 32.dp) { a.voiceSearch() }
            if (item.flag("ai", true) && maxWidth > 200.dp) GlyphButton("AI", null, size = 32.dp) { a.openAi() }
        }
    }
}

// ---------- app shortcuts ----------

@Composable
private fun AppsWidget(a: MainActivity, item: NoCapItem) {
    val keys = item.opt("apps").split('|').filter { it.isNotBlank() }
    val byKey = remember(a.phoneApps) { a.phoneApps.associateBy { it.key } }
    val apps = keys.mapNotNull { byKey[it] }
    WSurface(item, null, pad = 6.dp) {
        if (apps.isEmpty()) {
            Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
                Text(t("Pick your go-to apps", "Choose apps for this widget"), style = MaterialTheme.typography.bodySmall, maxLines = 1)
                Spacer(Modifier.height(6.dp))
                Pill(t("Pick apps", "Choose apps")) { a.editing = item }
            }
            return@WSurface
        }
        val icon = (minOf(maxWidth.value / 4.6f, 52f)).dp
        val cols = (maxWidth / (icon + 18.dp)).toInt().coerceAtLeast(1)
        val rowH = icon + if (item.flag("labels", true)) 26.dp else 10.dp
        val rows = (maxHeight / rowH).toInt().coerceAtLeast(1)
        Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.SpaceEvenly) {
            apps.take(cols * rows).chunked(cols).forEach { row ->
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceEvenly) {
                    row.forEach { app ->
                        Box(Modifier.clip(RoundedCornerShape(12.dp)).clickable { a.launch(app) }) {
                            AppTile(app, size = icon, label = item.flag("labels", true), onWallpaper = item.opt("bg", "glass") == "clear", labelWidth = icon + 14.dp)
                        }
                    }
                    repeat(cols - row.size) { Spacer(Modifier.width(icon)) }
                }
            }
        }
    }
}
