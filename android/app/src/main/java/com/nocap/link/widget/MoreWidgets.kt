package com.nocap.link.widget

import android.app.AlarmManager
import android.app.NotificationManager
import android.app.PendingIntent
import android.bluetooth.BluetoothManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.graphics.BitmapFactory
import android.location.LocationManager
import android.Manifest
import android.net.wifi.WifiManager
import android.os.Build
import android.provider.AlarmClock
import android.provider.Settings
import android.text.format.DateFormat
import androidx.compose.animation.Crossfade
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.graphics.Shadow
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.sp
import androidx.core.content.FileProvider
import com.nocap.link.data.Opt
import com.nocap.link.data.t
import com.nocap.link.home.Home
import com.nocap.link.home.NoCapItem
import com.nocap.link.MainActivity
import com.nocap.link.sync.Notices
import com.nocap.link.system.DotsService
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.Orbitron
import com.nocap.link.ui.titleFamily
import java.io.File
import java.util.Calendar
import java.util.Date
import kotlinx.coroutines.delay
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject

private fun Dp.sp(density: androidx.compose.ui.unit.Density) = with(density) { this@sp.toSp() }

// ---------- clock ----------

val clockFaces = listOf("glow", "digital", "analog", "stacked", "minimal")

fun faceName(f: String) = when (f) {
    "glow" -> t("NoCap Glow", "NoCap glow")
    "digital" -> t("Big Digits", "Digital")
    "analog" -> t("Analog", "Analog")
    "stacked" -> t("Stacked", "Stacked")
    else -> t("Minimal", "Minimal")
}

@Composable
fun ClockWidget(a: MainActivity, item: NoCapItem) {
    val ctx = LocalContext.current
    val density = LocalDensity.current
    val face = item.opt("face", "glow")
    val secs = item.flag("seconds", false) || face == "analog"
    var now by remember { mutableStateOf(Date()) }
    LaunchedEffect(secs) { while (true) { now = Date(); val step = if (secs) 1000L else 60_000L; delay(step - System.currentTimeMillis() % step) } }
    val h24 = when (item.opt("h24", "auto")) { "24" -> true; "12" -> false; else -> DateFormat.is24HourFormat(ctx) }
    val hh = DateFormat.format(if (h24) "HH" else "h", now).toString()
    val mm = DateFormat.format("mm", now).toString()
    val ss = DateFormat.format("ss", now).toString()
    val date = DateFormat.format(item.opt("datefmt", "EEEE, d MMMM"), now).toString()
    val alarm = if (item.flag("alarm", true)) remember(now.time / 60_000) {
        runCatching { ctx.getSystemService(AlarmManager::class.java).nextAlarmClock?.triggerTime }.getOrNull()
            ?.takeIf { it - System.currentTimeMillis() < 24 * 3_600_000L }
            ?.let { DateFormat.format(if (h24) "HH:mm" else "h:mm a", Date(it)).toString() }
    } else null
    val accent = NoCap.Red
    val openClock = { runCatching { a.startActivity(Intent(AlarmClock.ACTION_SHOW_ALARMS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }; Unit }
    val clear = item.copy(cfg = item.cfg + ("bg" to item.opt("bg", if (face == "analog") "glass" else "clear")), )
    WSurface(clear, onClick = openClock, pad = 10.dp) {
        val showDate = item.flag("date", true) && maxHeight > 70.dp
        val center = item.opt("align", "start") == "center"
        val align = if (center) Alignment.CenterHorizontally else Alignment.Start
        val shadow = Shadow(Color.Black.copy(alpha = 0.55f), Offset(0f, 2f), 8f)
        when (face) {
            "analog" -> Row(Modifier.fillMaxSize(), verticalAlignment = Alignment.CenterVertically) {
                val s = minOf(maxWidth, maxHeight)
                AnalogFace(now, s)
                if (maxWidth > s + 90.dp) Column(Modifier.padding(start = 14.dp)) {
                    Text("$hh:$mm", style = MaterialTheme.typography.displaySmall.copy(shadow = shadow), color = Color.White)
                    if (showDate) Text(date.uppercase(), style = MaterialTheme.typography.labelMedium, color = accent, maxLines = 2)
                    alarm?.let { Text("⏰ $it", style = MaterialTheme.typography.labelSmall, color = NoCap.Muted) }
                }
            }
            "stacked" -> Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center, horizontalAlignment = align) {
                val big = (maxHeight * (if (showDate) 0.36f else 0.44f)).sp(density)
                Text(hh.padStart(2, '0'), style = MaterialTheme.typography.displayLarge.copy(fontSize = big, lineHeight = big, shadow = shadow, fontWeight = FontWeight.Black), color = Color.White, maxLines = 1)
                Text(mm, style = MaterialTheme.typography.displayLarge.copy(fontSize = big, lineHeight = big, shadow = shadow, fontWeight = FontWeight.Black), color = accent, maxLines = 1)
                if (showDate) Text(date.uppercase(), style = MaterialTheme.typography.labelMedium.copy(shadow = shadow), color = Color.White, maxLines = 1)
            }
            "minimal" -> Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center, horizontalAlignment = align) {
                val size = minOf((maxHeight * 0.42f).sp(density).value, (maxWidth / 4.2f).sp(density).value).sp
                Text("$hh:$mm" + if (secs) ":$ss" else "", style = MaterialTheme.typography.displayLarge.copy(fontFamily = androidx.compose.ui.text.font.FontFamily.Default, fontWeight = FontWeight.Light, fontSize = size, letterSpacing = 0.sp, shadow = shadow), color = Color.White, maxLines = 1)
                if (showDate) Text(date, style = MaterialTheme.typography.bodyMedium.copy(shadow = shadow), color = Color.White.copy(alpha = 0.85f), maxLines = 1)
                alarm?.let { Text("⏰ $it", style = MaterialTheme.typography.labelSmall.copy(shadow = shadow), color = Color.White.copy(alpha = 0.7f)) }
            }
            else -> Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center, horizontalAlignment = align) {
                val glow = face == "glow"
                val chars = 5 + if (secs) 3 else 0
                val size = minOf((maxHeight * (if (showDate) 0.5f else 0.66f)).sp(density).value, (maxWidth / (chars * 0.78f)).sp(density).value).sp
                val txt = "$hh:$mm" + if (secs) ":$ss" else ""
                Box {
                    if (glow) Text(txt, style = MaterialTheme.typography.displayLarge.copy(fontFamily = titleFamily(), fontWeight = FontWeight.Black, fontSize = size, shadow = Shadow(accent, Offset.Zero, 42f)), color = accent.copy(alpha = 0.55f), maxLines = 1)
                    Text(
                        txt, style = MaterialTheme.typography.displayLarge.copy(fontFamily = titleFamily(), fontWeight = if (glow) FontWeight.Black else FontWeight.Bold, fontSize = size, shadow = if (glow) Shadow(accent.copy(alpha = 0.9f), Offset.Zero, 18f) else shadow),
                        color = Color.White, maxLines = 1,
                    )
                }
                if (glow) Box(Modifier.padding(vertical = 3.dp).width(64.dp).height(2.dp).background(Brush.horizontalGradient(listOf(accent, Color.Transparent))))
                if (showDate) Text(date.uppercase(), Modifier.padding(start = 3.dp), style = MaterialTheme.typography.labelMedium.copy(shadow = shadow), color = if (glow) accent else Color.White, maxLines = 1)
                alarm?.let { Text("⏰ $it", Modifier.padding(start = 3.dp), style = MaterialTheme.typography.labelSmall.copy(shadow = shadow), color = Color.White.copy(alpha = 0.75f)) }
            }
        }
    }
}

@Composable
private fun AnalogFace(now: Date, size: Dp) {
    val accent = NoCap.Red
    val txt = NoCap.Text
    val cal = Calendar.getInstance().apply { time = now }
    val h = cal.get(Calendar.HOUR) + cal.get(Calendar.MINUTE) / 60f
    val m = cal.get(Calendar.MINUTE) + cal.get(Calendar.SECOND) / 60f
    val s = cal.get(Calendar.SECOND).toFloat()
    Canvas(Modifier.size(size)) {
        val r = this.size.minDimension / 2
        val c = center
        drawCircle(Brush.radialGradient(listOf(accent.copy(alpha = 0.18f), Color.Transparent), c, r), r)
        drawCircle(txt.copy(alpha = 0.18f), r - 1.dp.toPx(), c, style = Stroke(1.dp.toPx()))
        for (i in 0 until 12) rotate(i * 30f, c) {
            drawLine(if (i % 3 == 0) accent else txt.copy(alpha = 0.5f), Offset(c.x, c.y - r + 4.dp.toPx()), Offset(c.x, c.y - r + (if (i % 3 == 0) 12 else 8).dp.toPx()), (if (i % 3 == 0) 2.5f else 1.5f).dp.toPx(), cap = StrokeCap.Round)
        }
        rotate(h * 30f, c) { drawLine(txt, c, Offset(c.x, c.y - r * 0.5f), 4.dp.toPx(), cap = StrokeCap.Round) }
        rotate(m * 6f, c) { drawLine(txt, c, Offset(c.x, c.y - r * 0.72f), 3.dp.toPx(), cap = StrokeCap.Round) }
        rotate(s * 6f, c) { drawLine(accent, Offset(c.x, c.y + r * 0.12f), Offset(c.x, c.y - r * 0.8f), 1.5.dp.toPx(), cap = StrokeCap.Round) }
        drawCircle(accent, 4.dp.toPx(), c)
    }
}

// ---------- weather ----------

/** The place a weather widget shows: its own, the shared one, or the phone's location. */
fun weatherPlace(ctx: Context, item: NoCapItem): Weather.Place? = when (item.opt("src", "shared")) {
    "gps" -> Weather.here(ctx) ?: Weather.Place.decode(Opt.weatherPlace.v)
    "city" -> Weather.Place.decode(item.opt("place"))
    else -> Weather.Place.decode(Opt.weatherPlace.v)
}

@Composable
fun WeatherGlyph(code: Int, day: Boolean, size: Dp) {
    val accent = NoCap.Red.toArgb()
    Canvas(Modifier.size(size)) { Weather.draw(drawContext.canvas.nativeCanvas, this.size.minDimension, Weather.kind(code, day), accent) }
}

@Composable
fun WeatherWidget(a: MainActivity, item: NoCapItem) {
    val ctx = LocalContext.current
    val f = Opt.fahrenheit.v
    var place by remember(item.cfg, Opt.weatherPlace.v) { mutableStateOf<Weather.Place?>(null) }
    var err by remember { mutableStateOf<String?>(null) }
    var refresh by remember { mutableIntStateOf(0) }
    LaunchedEffect(item.cfg, Opt.weatherPlace.v, f, refresh) {
        while (true) {
            val p = withContext(Dispatchers.IO) { weatherPlace(ctx, item) }
            place = p
            if (p != null) {
                err = runCatching { withContext(Dispatchers.IO) { Weather.forecast(p, f, force = refresh > 0) } }.exceptionOrNull()?.let { t("Weather's not loading. Offline?", "Couldn't load the weather.") }
                runCatching { com.nocap.link.widget.AppWidgets.updateWeather(ctx, null) }
            }
            delay(20 * 60_000L)
        }
    }
    val r = place?.let { Weather.cached(it, f) }
    WSurface(item, onClick = { if (place == null) a.editing = item else a.weatherFor = item }) {
        if (place == null) {
            Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center) {
                WTitle(t("Weather", "Weather"))
                Text(t("Where you at? Tap to pick a city or use your location.", "Tap to choose a city or use your location."), style = MaterialTheme.typography.bodySmall, maxLines = 3)
            }
            return@WSurface
        }
        if (r == null) {
            Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center) {
                WTitle(place!!.name)
                Text(err ?: t("Checking the sky…", "Loading…"), style = MaterialTheme.typography.bodySmall, color = if (err != null) NoCap.Warn else NoCap.Muted)
            }
            return@WSurface
        }
        val wide = maxWidth > 220.dp
        val tall = maxHeight > 140.dp
        val small = maxHeight < 90.dp
        Column(Modifier.fillMaxSize()) {
            Row(Modifier.fillMaxWidth().weight(1f), verticalAlignment = Alignment.CenterVertically) {
                WeatherGlyph(r.code, r.isDay, if (small) 40.dp else 58.dp)
                Column(Modifier.weight(1f).padding(start = 8.dp)) {
                    Text(Weather.deg(r.temp), style = MaterialTheme.typography.displaySmall.copy(fontSize = if (small) 22.sp else 30.sp), maxLines = 1)
                    if (!small || wide) Text(Weather.describe(r.code), style = MaterialTheme.typography.labelMedium, color = NoCap.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
                }
                if (wide) Column(horizontalAlignment = Alignment.End) {
                    Text(r.place.name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text("↑${Weather.deg(r.hi)}  ↓${Weather.deg(r.lo)}", style = MaterialTheme.typography.labelMedium, color = NoCap.Muted)
                    if (tall || !small) Text(t("Feels ${Weather.deg(r.feels)}", "Feels like ${Weather.deg(r.feels)}"), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted)
                }
            }
            if (tall) {
                Spacer(Modifier.height(6.dp))
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                    val n = if (wide) 5 else 3
                    r.days.drop(1).take(n).forEach { d ->
                        Column(horizontalAlignment = Alignment.CenterHorizontally) {
                            Text(dayName(d.date), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted)
                            WeatherGlyph(d.code, true, 26.dp)
                            Text("${Weather.deg(d.hi)}/${Weather.deg(d.lo)}", style = MaterialTheme.typography.labelSmall.copy(fontSize = 11.sp), maxLines = 1)
                        }
                    }
                }
            }
        }
    }
}

fun dayName(iso: String): String = runCatching {
    val d = java.text.SimpleDateFormat("yyyy-MM-dd", java.util.Locale.US).parse(iso)!!
    DateFormat.format("EEE", d).toString()
}.getOrDefault(iso.takeLast(2))

// ---------- calendar ----------

@Composable
fun CalendarWidget(a: MainActivity, item: NoCapItem) {
    val ctx = LocalContext.current
    var events by remember { mutableStateOf<List<Agenda.Event>>(emptyList()) }
    val allowed = remember(a.resumeTick) { Agenda.allowed(ctx) }
    LaunchedEffect(allowed, item.cfg, a.resumeTick) {
        while (allowed) {
            events = withContext(Dispatchers.IO) { Agenda.upcoming(ctx, item.opt("days", "7").toIntOrNull() ?: 7, item.flag("allday", true)) }
            delay(60_000)
        }
    }
    WSurface(item, null) {
        val wide = maxWidth > 220.dp
        val now = Date()
        Row(Modifier.fillMaxSize()) {
            if (wide) {
                Column(Modifier.width(76.dp).fillMaxHeight().clip(RoundedCornerShape(14.dp)).clickable { Agenda.openCalendar(ctx) }, horizontalAlignment = Alignment.CenterHorizontally) {
                    Text(DateFormat.format("EEE", now).toString().uppercase(), style = MaterialTheme.typography.labelMedium, color = NoCap.Red)
                    Text(DateFormat.format("d", now).toString(), style = MaterialTheme.typography.displaySmall.copy(fontSize = 34.sp))
                    Text(DateFormat.format("MMM", now).toString().uppercase(), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted)
                    Spacer(Modifier.weight(1f))
                    if (allowed) Text("+", Modifier.size(30.dp).clip(CircleShape).background(NoCap.Red.copy(alpha = 0.18f)).clickable { Agenda.add(ctx) }.padding(top = 2.dp), textAlign = TextAlign.Center, style = MaterialTheme.typography.titleMedium, color = NoCap.Red)
                }
                Spacer(Modifier.width(10.dp))
            }
            Column(Modifier.weight(1f).fillMaxHeight()) {
                if (!wide) WTitle(DateFormat.format("EEE d MMM", now).toString(), Modifier.clickable { Agenda.openCalendar(ctx) })
                if (!allowed) {
                    Text(t("Let NoCap OS peek at your calendar (it stays on this phone).", "Allow calendar access to see your events. Nothing leaves this phone."), style = MaterialTheme.typography.bodySmall, maxLines = 3)
                    Spacer(Modifier.height(6.dp))
                    Pill(t("Show my calendar", "Allow calendar")) { a.askPermission(Manifest.permission.READ_CALENDAR) {} }
                    return@Column
                }
                if (events.isEmpty()) {
                    Text(t("Nothing planned. Main character free time.", "No upcoming events."), Modifier.padding(top = 6.dp), style = MaterialTheme.typography.bodySmall)
                    return@Column
                }
                val rows = ((maxHeight - 10.dp) / 38.dp).toInt().coerceAtLeast(1)
                events.take(rows).forEach { e ->
                    Row(Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).clickable { Agenda.open(ctx, e) }.padding(vertical = 3.dp), verticalAlignment = Alignment.CenterVertically) {
                        Box(Modifier.width(3.dp).height(30.dp).clip(RoundedCornerShape(2.dp)).background(if (e.color != 0) Color(e.color) else NoCap.Red))
                        Column(Modifier.weight(1f).padding(start = 8.dp)) {
                            Text(e.title.ifBlank { t("(no title)", "(No title)") }, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Text(whenText(ctx, e), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
            }
        }
    }
}

private fun whenText(ctx: Context, e: Agenda.Event): String {
    val today = Calendar.getInstance()
    val c = Calendar.getInstance().apply { timeInMillis = e.begin }
    val sameDay = c.get(Calendar.YEAR) == today.get(Calendar.YEAR) && c.get(Calendar.DAY_OF_YEAR) == today.get(Calendar.DAY_OF_YEAR)
    val day = if (sameDay) t("Today", "Today") else DateFormat.format("EEE d", c).toString()
    if (e.allDay) return "$day · " + t("all day", "All day")
    val time = DateFormat.getTimeFormat(ctx).format(Date(e.begin))
    return "$day · $time" + if (e.where.isNotBlank()) " · ${e.where}" else ""
}

// ---------- music ----------

@Composable
fun MusicWidget(a: MainActivity, item: NoCapItem) {
    val ctx = LocalContext.current
    val allowed = remember(a.resumeTick) { DotsService.allowed(ctx) }
    LaunchedEffect(allowed) { if (allowed) Media.start(ctx) }
    val n = Media.now
    var tick by remember { mutableLongStateOf(0L) }
    LaunchedEffect(n?.playing) { while (n?.playing == true) { tick++; delay(1000) } }
    WSurface(item, onClick = { if (n != null) Media.open(ctx) else openMusicApp(a) }) {
        if (!allowed) {
            Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center) {
                WTitle(t("Now Playing", "Now playing"))
                Text(t("Allow notification access to see and control what's playing.", "Allow notification access to show and control media."), style = MaterialTheme.typography.bodySmall, maxLines = 2)
                Spacer(Modifier.height(6.dp))
                Pill(t("Allow", "Allow")) { DotsService.openSettings(ctx) }
            }
            return@WSurface
        }
        val tall = maxHeight > 120.dp
        val art = remember(n?.art) { n?.art?.asImageBitmap() }
        Row(Modifier.fillMaxSize(), verticalAlignment = Alignment.CenterVertically) {
            val artSize = if (tall) minOf(maxHeight, 120.dp) else maxHeight
            Box(Modifier.size(artSize).clip(RoundedCornerShape(16.dp)).background(Brush.linearGradient(listOf(NoCap.Red.copy(alpha = 0.5f), Color(0xFF14161C)))), contentAlignment = Alignment.Center) {
                if (art != null) Image(art, null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
                else Text("♪", style = MaterialTheme.typography.displaySmall, color = Color.White)
            }
            Column(Modifier.weight(1f).padding(start = 12.dp), verticalArrangement = Arrangement.Center) {
                Text(n?.title?.ifBlank { null } ?: t("Nothing playing", "Nothing playing"), style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(n?.let { listOf(it.artist, it.app).filter { s -> s.isNotBlank() }.joinToString(" · ") } ?: t("Tap to open your music", "Tap to open a music app"), style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (tall && n != null && n.duration > 0) {
                    val pos = remember(tick, n) { n.pos() }
                    Box(Modifier.fillMaxWidth().padding(top = 8.dp).height(4.dp).clip(RoundedCornerShape(2.dp)).background(Color.White.copy(alpha = 0.12f))) {
                        Box(Modifier.fillMaxWidth((pos.toFloat() / n.duration).coerceIn(0f, 1f)).fillMaxHeight().background(NoCap.Red))
                    }
                    Row { Text(mmss(pos), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted); Spacer(Modifier.weight(1f)); Text(mmss(n.duration), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted) }
                }
                if (n != null) Row(Modifier.padding(top = 4.dp), horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                    GlyphButton("⏮", null, size = 32.dp) { Media.prev() }
                    GlyphButton(if (n.playing) "❚❚" else "▶", null, on = true, size = 40.dp) { Media.toggle() }
                    GlyphButton("⏭", null, size = 32.dp) { Media.next() }
                }
            }
        }
    }
}

private fun mmss(ms: Long): String { val s = ms / 1000; return "%d:%02d".format(s / 60, s % 60) }

private fun openMusicApp(a: MainActivity) {
    runCatching { a.startActivity(Intent.makeMainSelectorActivity(Intent.ACTION_MAIN, Intent.CATEGORY_APP_MUSIC).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
        .onFailure { a.toast = t("No music app found.", "No music app found.") }
}

// ---------- quick toggles ----------

val toggleIds = listOf("torch", "wifi", "bt", "dnd", "data", "location", "volume", "display")

fun toggleName(id: String) = when (id) {
    "torch" -> t("Flashlight", "Flashlight")
    "wifi" -> "Wi-Fi"
    "bt" -> "Bluetooth"
    "dnd" -> t("Do not disturb", "Do not disturb")
    "data" -> t("Internet", "Internet")
    "location" -> t("Location", "Location")
    "volume" -> t("Volume", "Volume")
    else -> t("Display", "Display")
}

private fun toggleGlyph(id: String) = when (id) {
    "torch" -> "✦"; "wifi" -> "≋"; "bt" -> "ᛒ"; "dnd" -> "☾"; "data" -> "⇅"; "location" -> "⌖"; "volume" -> "♪"; else -> "☀"
}

@Composable
fun TogglesWidget(a: MainActivity, item: NoCapItem) {
    val ctx = LocalContext.current
    val ids = item.opt("items", "torch,wifi,bt,dnd,data,location").split(',').filter { it in toggleIds }
    var tick by remember { mutableIntStateOf(0) }
    LaunchedEffect(a.resumeTick) { while (true) { tick++; delay(3000) } }
    fun state(id: String): Boolean? = when (id) {
        "torch" -> Torch.on
        "wifi" -> runCatching { ctx.applicationContext.getSystemService(WifiManager::class.java).isWifiEnabled }.getOrNull()
        "bt" -> runCatching { ctx.getSystemService(BluetoothManager::class.java).adapter?.isEnabled }.getOrNull()
        "dnd" -> ctx.getSystemService(NotificationManager::class.java).currentInterruptionFilter.let { it != NotificationManager.INTERRUPTION_FILTER_ALL && it != NotificationManager.INTERRUPTION_FILTER_UNKNOWN }
        "location" -> if (Build.VERSION.SDK_INT >= 28) ctx.getSystemService(LocationManager::class.java).isLocationEnabled else null
        else -> null
    }
    fun press(id: String) {
        when (id) {
            "torch" -> if (!Torch.toggle(ctx)) a.toast = t("No flashlight on this phone.", "This phone has no flashlight.")
            "wifi" -> a.openSystem(if (Build.VERSION.SDK_INT >= 29) Settings.Panel.ACTION_WIFI else Settings.ACTION_WIFI_SETTINGS)
            "bt" -> a.openSystem(Settings.ACTION_BLUETOOTH_SETTINGS)
            "dnd" -> {
                val nm = ctx.getSystemService(NotificationManager::class.java)
                if (nm.isNotificationPolicyAccessGranted) {
                    nm.setInterruptionFilter(if (state("dnd") == true) NotificationManager.INTERRUPTION_FILTER_ALL else NotificationManager.INTERRUPTION_FILTER_PRIORITY)
                } else {
                    a.toast = t("Let NoCap OS flip Do not disturb, then tap again.", "Allow Do not disturb access for NoCap OS, then tap again.")
                    a.openSystem(Settings.ACTION_NOTIFICATION_POLICY_ACCESS_SETTINGS)
                }
            }
            "data" -> a.openSystem(if (Build.VERSION.SDK_INT >= 29) Settings.Panel.ACTION_INTERNET_CONNECTIVITY else Settings.ACTION_WIRELESS_SETTINGS)
            "location" -> a.openSystem(Settings.ACTION_LOCATION_SOURCE_SETTINGS)
            "volume" -> if (Build.VERSION.SDK_INT >= 29) a.openSystem(Settings.Panel.ACTION_VOLUME) else a.openSystem(Settings.ACTION_SOUND_SETTINGS)
            else -> a.openSystem(Settings.ACTION_DISPLAY_SETTINGS)
        }
        tick++
    }
    WSurface(item, null, pad = 6.dp) {
        val perRow = (maxWidth / 64.dp).toInt().coerceIn(2, 6)
        val rows = if (maxHeight > 120.dp) 2 else 1
        Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.SpaceEvenly) {
            ids.take(perRow * rows).chunked(perRow).forEach { row ->
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceEvenly) {
                    row.forEach { id ->
                        @Suppress("UNUSED_EXPRESSION") tick
                        GlyphButton(toggleGlyph(id), if (maxHeight > 70.dp) toggleName(id) else null, on = state(id) == true, size = 38.dp) { press(id) }
                    }
                }
            }
        }
    }
}

// ---------- notes / to-do ----------

data class Todo(val text: String, val done: Boolean)

fun todos(item: NoCapItem): List<Todo> = runCatching {
    val a = JSONArray(item.opt("todo", "[]"))
    (0 until a.length()).map { val o = a.getJSONObject(it); Todo(o.optString("t"), o.optBoolean("d")) }
}.getOrDefault(emptyList())

fun withTodos(item: NoCapItem, list: List<Todo>): NoCapItem =
    item.with("todo" to JSONArray(list.map { JSONObject().put("t", it.text).put("d", it.done) }).toString())

@Composable
fun NotesWidget(a: MainActivity, item: NoCapItem) {
    val todo = item.opt("mode", "notes") == "todo"
    WSurface(item, onClick = { a.editing = item }) {
        Column(Modifier.fillMaxSize()) {
            WTitle(item.opt("title", if (todo) t("To-do", "To-do") else t("Notes", "Notes")), trailing = {
                Text("+", Modifier.clip(CircleShape).clickable { a.editing = item }.padding(horizontal = 8.dp), style = MaterialTheme.typography.titleMedium, color = NoCap.Red)
            })
            Spacer(Modifier.height(4.dp))
            if (todo) {
                val list = todos(item)
                if (list.isEmpty()) Text(t("Nothing to do. Iconic.", "Nothing to do. Tap + to add."), style = MaterialTheme.typography.bodySmall)
                val rows = ((maxHeight - 30.dp) / 28.dp).toInt().coerceAtLeast(1)
                list.sortedBy { it.done }.take(rows).forEach { td ->
                    Row(
                        Modifier.fillMaxWidth().clip(RoundedCornerShape(8.dp)).clickable {
                            val cur = (Home.find(item.id)?.second as? NoCapItem) ?: item
                            Home.replace(withTodos(cur, todos(cur).map { if (it == td) it.copy(done = !it.done) else it }))
                        }.padding(vertical = 3.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Box(
                            Modifier.size(18.dp).clip(RoundedCornerShape(5.dp)).background(if (td.done) NoCap.Red else Color.Transparent).border(1.5.dp, NoCap.Red, RoundedCornerShape(5.dp)),
                            contentAlignment = Alignment.Center,
                        ) { if (td.done) Text("✓", style = MaterialTheme.typography.labelSmall, color = Color.White) }
                        Text(
                            td.text, Modifier.padding(start = 8.dp), style = MaterialTheme.typography.bodyMedium.copy(textDecoration = if (td.done) TextDecoration.LineThrough else null),
                            color = if (td.done) NoCap.Muted else NoCap.Text, maxLines = 1, overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
            } else {
                val text = item.opt("text")
                Text(text.ifBlank { t("Tap to jot something down.", "Tap to write a note.") }, style = MaterialTheme.typography.bodyMedium, color = if (text.isBlank()) NoCap.Muted else NoCap.Text, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

// ---------- photo frame ----------

@Composable
fun PhotoWidget(a: MainActivity, item: NoCapItem) {
    val ctx = LocalContext.current
    val names = item.opt("photos").split('|').filter { it.isNotBlank() }
    var idx by remember(names) { mutableIntStateOf(0) }
    val every = item.opt("every", "15").toIntOrNull() ?: 15
    LaunchedEffect(names, every) { while (every > 0 && names.size > 1) { delay(every * 60_000L); idx = (idx + 1) % names.size } }
    val shape = RoundedCornerShape(22.dp)
    if (names.isEmpty()) {
        WSurface(item, onClick = { a.pickPhotos(item) }) {
            Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
                Text("▧", style = MaterialTheme.typography.displaySmall, color = NoCap.Red)
                Text(t("Add pics", "Add photos"), style = MaterialTheme.typography.labelLarge, maxLines = 1)
            }
        }
        return
    }
    val name = names[idx % names.size]
    var bmp by remember(name) { mutableStateOf<ImageBitmap?>(null) }
    LaunchedEffect(name) { bmp = withContext(Dispatchers.IO) { runCatching { BitmapFactory.decodeFile(File(Photos.dir(ctx), name).path)?.asImageBitmap() }.getOrNull() } }
    Box(
        Modifier.fillMaxSize().clip(shape).border(1.dp, NoCap.Red.copy(alpha = if (item.flag("glow", true)) 0.4f else 0f), shape)
            .clickable {
                runCatching {
                    val uri = FileProvider.getUriForFile(ctx, "${ctx.packageName}.files", File(Photos.dir(ctx), name))
                    a.startActivity(Intent(Intent.ACTION_VIEW).setDataAndType(uri, "image/jpeg").addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK))
                }.onFailure { idx = (idx + 1) % names.size }
            },
    ) {
        Crossfade(bmp, label = "photo") { b ->
            if (b != null) Image(b, null, Modifier.fillMaxSize(), contentScale = if (item.opt("fit", "crop") == "fit") ContentScale.Fit else ContentScale.Crop)
            else Box(Modifier.fillMaxSize().background(NoCap.Card))
        }
        if (names.size > 1) {
            Text(
                "›", Modifier.align(Alignment.BottomEnd).padding(6.dp).size(28.dp).clip(CircleShape).background(Color(0x88000000)).clickable { idx = (idx + 1) % names.size }.padding(top = 1.dp),
                textAlign = TextAlign.Center, style = MaterialTheme.typography.titleMedium, color = Color.White,
            )
        }
    }
}

// ---------- countdown & timer ----------

@Composable
fun TimerWidget(a: MainActivity, item: NoCapItem) {
    val ctx = LocalContext.current
    val countdown = item.opt("mode", "timer") == "countdown"
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(Unit) { while (true) { now = System.currentTimeMillis(); delay(500) } }
    WSurface(item, onClick = { a.editing = item }) {
        val tiny = maxHeight < 90.dp
        if (countdown) {
            val target = item.opt("target").toLongOrNull()
            Column(Modifier.fillMaxSize(), verticalArrangement = Arrangement.Center) {
                if (!tiny) WTitle(item.opt("title", t("Countdown", "Countdown")))
                if (target == null) { Text(t("Tap to set the big day.", "Tap to set a date."), style = MaterialTheme.typography.bodySmall); return@Column }
                val diff = target - now
                val past = diff < 0
                val ms = kotlin.math.abs(diff)
                val days = ms / 86_400_000L
                val hrs = (ms / 3_600_000L) % 24
                val mins = (ms / 60_000L) % 60
                Row(verticalAlignment = Alignment.Bottom) {
                    Text("$days", style = MaterialTheme.typography.displaySmall.copy(fontSize = if (tiny) 26.sp else 38.sp), color = NoCap.Red)
                    Text(" " + t("days", "days"), Modifier.padding(bottom = 6.dp), style = MaterialTheme.typography.labelLarge)
                }
                Text("${hrs}h ${mins}m " + if (past) t("ago", "ago") else t("to go", "left"), style = MaterialTheme.typography.labelMedium, color = NoCap.Muted)
                if (tiny) Text(item.opt("title"), style = MaterialTheme.typography.labelSmall, maxLines = 1)
            }
            return@WSurface
        }
        val dur = item.opt("dur", "300000").toLongOrNull() ?: 300_000L
        val end = item.opt("end").toLongOrNull()
        val left = when {
            end != null -> (end - now).coerceAtLeast(0)
            else -> item.opt("left").toLongOrNull() ?: dur
        }
        LaunchedEffect(end != null && left == 0L) {
            if (end != null && left == 0L) {
                Home.replace(item.with("end" to null, "left" to null))
                if (!item.flag("ring", true)) {
                    a.toast = t("Time's up!", "Time's up.")
                    a.window.decorView.let { com.nocap.link.home.buzz(it, true) }
                }
            }
        }
        val running = end != null && left > 0
        val p = 1f - left.toFloat() / dur.coerceAtLeast(1)
        Row(Modifier.fillMaxSize(), verticalAlignment = Alignment.CenterVertically) {
            val ring = if (tiny) maxHeight else minOf(maxHeight, 110.dp)
            Box(contentAlignment = Alignment.Center) {
                Ring((p * 100).toDouble(), ring, color = NoCap.Red, center = mmss(left))
            }
            Column(Modifier.weight(1f).padding(start = 10.dp), verticalArrangement = Arrangement.Center) {
                if (!tiny) Text(item.opt("title", t("Timer", "Timer")), style = MaterialTheme.typography.titleSmall, maxLines = 1)
                Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    GlyphButton(if (running) "❚❚" else "▶", null, on = true, size = 34.dp) {
                        if (running) {
                            Home.replace(item.with("end" to null, "left" to left.toString()))
                            Timers.cancel(ctx, item.id)
                        } else {
                            val l = if (left <= 0) dur else left
                            Home.replace(item.with("end" to (System.currentTimeMillis() + l).toString(), "left" to null))
                            if (item.flag("ring", true)) a.clockTimer((l / 1000).toInt(), item.opt("title", "NoCap timer"))
                            else Timers.schedule(ctx, item.id, System.currentTimeMillis() + l, item.opt("title", t("Timer", "Timer")))
                        }
                    }
                    GlyphButton("↺", null, size = 34.dp) { Home.replace(item.with("end" to null, "left" to null)); Timers.cancel(ctx, item.id) }
                    if (!tiny && maxWidth > 200.dp) GlyphButton("+1", null, size = 34.dp) {
                        if (end != null) Home.replace(item.with("end" to (end + 60_000).toString())) else Home.replace(item.with("left" to (left + 60_000).toString(), "dur" to (dur + 60_000).toString()))
                    }
                }
            }
        }
    }
}

/** In-app timers that ring with a notification (when not handed to the clock app). */
object Timers {
    private fun pi(ctx: Context, id: String, title: String?): PendingIntent =
        PendingIntent.getBroadcast(ctx, id.hashCode(), Intent(ctx, TimerReceiver::class.java).putExtra("title", title), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)

    fun schedule(ctx: Context, id: String, at: Long, title: String) {
        runCatching { ctx.getSystemService(AlarmManager::class.java).setAndAllowWhileIdle(AlarmManager.RTC_WAKEUP, at, pi(ctx, id, title)) }
    }

    fun cancel(ctx: Context, id: String) {
        runCatching { ctx.getSystemService(AlarmManager::class.java).cancel(pi(ctx, id, null)) }
    }
}

class TimerReceiver : BroadcastReceiver() {
    override fun onReceive(ctx: Context, intent: Intent) {
        com.nocap.link.data.Prefs.init(ctx)
        Notices.show(ctx, 4040, intent.getStringExtra("title") ?: t("Timer", "Timer"), t("Time's up!", "Time's up."), Intent(ctx, MainActivity::class.java))
    }
}
