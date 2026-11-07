package com.nocap.link.nocap

import android.text.format.DateFormat
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
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
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.nocap.link.Layer
import com.nocap.link.MainActivity
import com.nocap.link.Screen
import com.nocap.link.ServerState
import com.nocap.link.launcher.NoCapKinds
import com.nocap.link.launcher.WindowFrame
import com.nocap.link.launcher.human
import com.nocap.link.launcher.labelShadow
import com.nocap.link.t
import com.nocap.link.ui.Chip
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.Orbitron
import kotlinx.coroutines.delay
import java.util.Date

/** NoCap OS's own home-screen widgets, styled like the NoCapOS desktop's. */
@Composable
fun NoCapWidget(a: MainActivity, kind: String) {
    when (kind) {
        NoCapKinds.CLOCK -> ClockWidget()
        NoCapKinds.SERVER -> Card(t("Server Vibes", "Server")) { ServerBody(a) }
        NoCapKinds.QUICK -> QuickWidget(a)
        NoCapKinds.FILES -> Card(t("Fresh Files", "Recent files")) { FilesBody(a) }
        NoCapKinds.AI -> AiWidget(a)
        else -> Card(kind) {}
    }
}

@Composable
private fun Card(title: String, content: @Composable () -> Unit) {
    WindowFrame(
        title = { Text(title.uppercase(), style = MaterialTheme.typography.labelMedium.copy(fontFamily = Orbitron, fontSize = 10.sp, letterSpacing = 2.sp), maxLines = 1) },
        onClose = null,
        modifier = Modifier.fillMaxSize(),
    ) { Box(Modifier.fillMaxSize().padding(horizontal = 12.dp, vertical = 8.dp)) { content() } }
}

@Composable
private fun ClockWidget() {
    val ctx = LocalContext.current
    var now by remember { mutableStateOf(Date()) }
    LaunchedEffect(Unit) { while (true) { now = Date(); delay(1000L - System.currentTimeMillis() % 1000L) } }
    val time = DateFormat.format(if (DateFormat.is24HourFormat(ctx)) "HH:mm" else "h:mm", now).toString()
    val date = DateFormat.format("EEEE, d MMMM", now).toString()
    Column(Modifier.fillMaxSize().padding(horizontal = 8.dp), verticalArrangement = Arrangement.Center) {
        Text(time, style = MaterialTheme.typography.displayLarge.copy(shadow = labelShadow), color = Color.White)
        Text(date.uppercase(), Modifier.padding(start = 4.dp), style = MaterialTheme.typography.labelMedium.copy(shadow = labelShadow), color = NoCap.Red)
    }
}

@Composable
private fun ServerBody(a: MainActivity) {
    val st = a.server
    when {
        st is ServerState.NotLinked -> Column {
            Text(t("Link your NoCap server and its vitals live here.", "Link your NoCap server to see its status here."), style = MaterialTheme.typography.bodyMedium)
            Spacer(Modifier.height(8.dp))
            Pill(t("Link with NoCap Link", "Link a server")) { a.screen = Screen.LinkForm() }
        }
        a.outdated != null -> Column {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(a.link?.serverName ?: "NoCapOS", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium, maxLines = 1)
                Chip(t("Online", "Online"), NoCap.Ok)
            }
            Spacer(Modifier.height(6.dp))
            Text(MainActivity.needsUpdate(a.outdated), style = MaterialTheme.typography.bodySmall, color = NoCap.Warn)
        }
        else -> {
            val s = a.summary
            val (label, color) = when (st) {
                is ServerState.Online -> t("Online", "Online") to NoCap.Ok
                is ServerState.Offline -> t("Offline", "Offline") to NoCap.Warn
                is ServerState.Unlinked -> t("Unlinked", "Unlinked") to NoCap.Bad
                is ServerState.CertChanged -> t("Cert changed", "Certificate changed") to NoCap.Warn
                else -> t("Checking…", "Checking…") to NoCap.Muted
            }
            Column(Modifier.fillMaxSize().clickable { a.layer = Layer.HUB }) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(s?.server?.name?.ifBlank { null } ?: a.link?.serverName ?: "NoCapOS", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Chip(label, color)
                }
                Spacer(Modifier.height(6.dp))
                if (st is ServerState.Online && s != null && s.ready) {
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceAround) {
                        Ring("CPU", s.cpuPercent, "${s.cores} ${t("cores", "cores")}")
                        Ring("RAM", s.memory.percent, "${human(s.memory.used)}")
                        Ring(t("Disk", "Storage"), s.storage.percent, "${human(s.storage.total - s.storage.used)} ${t("free", "free")}")
                    }
                    Text(t("Up ${uptime(s.uptime)}, no cap", "Up ${uptime(s.uptime)}") + if (s.server.version.isNotBlank()) "  ·  v${s.server.version}" else "", style = MaterialTheme.typography.bodySmall)
                } else if (st is ServerState.Offline) {
                    Text(st.why, style = MaterialTheme.typography.bodySmall, color = NoCap.Warn, maxLines = 2)
                } else if (st is ServerState.Online) {
                    Text(t("Warming up the numbers…", "Collecting metrics…"), style = MaterialTheme.typography.bodySmall)
                }
            }
        }
    }
}

@Composable
private fun Ring(label: String, percent: Double, sub: String) {
    val p = (percent / 100.0).toFloat().coerceIn(0f, 1f)
    val c = when { percent >= 90 -> NoCap.Bad; percent >= 75 -> NoCap.Warn; else -> NoCap.Red }
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Box(contentAlignment = Alignment.Center) {
            Canvas(Modifier.size(52.dp)) {
                val sw = 5.dp.toPx()
                val inset = sw / 2
                drawArc(Color.White.copy(alpha = 0.1f), 0f, 360f, false, Offset(inset, inset), Size(size.width - sw, size.height - sw), style = Stroke(sw))
                drawArc(c, -90f, 360f * p, false, Offset(inset, inset), Size(size.width - sw, size.height - sw), style = Stroke(sw, cap = StrokeCap.Round))
            }
            Text("${percent.toInt()}%", style = MaterialTheme.typography.labelMedium)
        }
        Text(label, style = MaterialTheme.typography.labelSmall, color = NoCap.Muted)
        Text(sub, style = MaterialTheme.typography.labelSmall, color = NoCap.Muted, maxLines = 1)
    }
}

private fun uptime(sec: Double): String {
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
private fun FilesBody(a: MainActivity) {
    when {
        a.server is ServerState.NotLinked -> Text(t("Link your server to see fresh files.", "Link your server to see recent files."), style = MaterialTheme.typography.bodySmall)
        a.outdated != null -> Text(MainActivity.needsUpdate(a.outdated), style = MaterialTheme.typography.bodySmall, color = NoCap.Warn)
        a.recentFiles.isEmpty() -> Text(t("Nothing new lately.", "No recent files."), style = MaterialTheme.typography.bodySmall)
        else -> Column {
            a.recentFiles.filter { !it.dir }.take(4).forEach { f ->
                Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).clickable { a.openFile(f) }.padding(vertical = 3.dp), verticalAlignment = Alignment.CenterVertically) {
                    Box(Modifier.size(6.dp).clip(CircleShape).background(NoCap.Red))
                    Text(f.name, Modifier.weight(1f).padding(start = 8.dp), style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text(human(f.size), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted)
                }
            }
        }
    }
}

@Composable
private fun QuickWidget(a: MainActivity) {
    Row(
        Modifier.fillMaxSize().clip(RoundedCornerShape(18.dp)).background(Color(0x9914161C)).padding(horizontal = 6.dp),
        horizontalArrangement = Arrangement.SpaceEvenly, verticalAlignment = Alignment.CenterVertically,
    ) {
        Quick("▣", t("Desktop", "Desktop")) { a.openDesktop() }
        Quick("AI", t("Ask AI", "NoCap AI")) { a.openAi() }
        Quick("↑", t("Send", "Send file")) { a.pickToSend() }
        Quick("▤", t("Files", "Files")) { a.layer = Layer.HUB }
    }
}

@Composable
private fun Quick(glyph: String, label: String, onClick: () -> Unit) {
    Column(Modifier.clip(RoundedCornerShape(12.dp)).clickable(onClick = onClick).padding(4.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Box(Modifier.size(34.dp).clip(CircleShape).background(NoCap.Red.copy(alpha = 0.18f)), contentAlignment = Alignment.Center) {
            Text(glyph, style = MaterialTheme.typography.labelLarge.copy(fontFamily = Orbitron, fontSize = 12.sp), color = NoCap.Red)
        }
        Text(label, style = MaterialTheme.typography.labelSmall, maxLines = 1)
    }
}

@Composable
private fun AiWidget(a: MainActivity) {
    Row(
        Modifier.fillMaxSize().padding(vertical = 4.dp).clip(RoundedCornerShape(50)).background(Color(0xB314161C))
            .clickable { a.openAi() }.padding(horizontal = 16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text("AI", style = MaterialTheme.typography.labelLarge.copy(fontFamily = Orbitron), color = NoCap.Red)
        Spacer(Modifier.width(12.dp))
        Text(t("Ask NoCap AI anything…", "Ask NoCap AI…"), Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge, color = NoCap.Muted, maxLines = 1)
    }
}

@Composable
fun Pill(text: String, onClick: () -> Unit) {
    Text(
        text, Modifier.clip(RoundedCornerShape(12.dp)).background(NoCap.Red).clickable(onClick = onClick).padding(horizontal = 14.dp, vertical = 8.dp),
        style = MaterialTheme.typography.labelLarge, color = Color.White,
    )
}

