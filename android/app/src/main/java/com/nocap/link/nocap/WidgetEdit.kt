package com.nocap.link.nocap

import android.Manifest
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DatePicker
import androidx.compose.material3.DatePickerDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TimePicker
import androidx.compose.material3.rememberDatePickerState
import androidx.compose.material3.rememberTimePickerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.nocap.link.MainActivity
import com.nocap.link.Opt
import com.nocap.link.launcher.AppIcon
import com.nocap.link.launcher.Cell
import com.nocap.link.launcher.Home
import com.nocap.link.launcher.NoCapItem
import com.nocap.link.launcher.NoCapKinds
import com.nocap.link.launcher.Toggle
import com.nocap.link.t
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.Orbitron
import com.nocap.link.ui.SearchField
import com.nocap.link.ui.glass
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import java.util.Calendar

fun widgetName(kind: String) = when (kind) {
    NoCapKinds.CLOCK -> t("Clock", "Clock")
    NoCapKinds.WEATHER -> t("Weather", "Weather")
    NoCapKinds.CALENDAR -> t("The Agenda", "Calendar")
    NoCapKinds.STATUS -> t("NoCap Status", "NoCap status")
    NoCapKinds.SERVER -> t("Server Vibes", "Server status")
    NoCapKinds.PHONE -> t("This Phone", "Phone status")
    NoCapKinds.STORAGE -> t("Storage", "Storage")
    NoCapKinds.MUSIC -> t("Now Playing", "Now playing")
    NoCapKinds.TOGGLES -> t("Quick Toggles", "Quick toggles")
    NoCapKinds.NOTES -> t("Notes & To-dos", "Notes and to-do")
    NoCapKinds.PHOTO -> t("Photo Frame", "Photo frame")
    NoCapKinds.APPS -> t("Go-to Apps", "App shortcuts")
    NoCapKinds.SEARCH -> t("Search Bar", "Search bar")
    NoCapKinds.QUICK -> t("Quick Moves", "NoCap Link actions")
    NoCapKinds.AI -> t("Ask NoCap AI", "NoCap AI")
    NoCapKinds.FILES -> t("Fresh Files", "Recent files")
    NoCapKinds.TIMER -> t("Countdown & Timer", "Countdown and timer")
    NoCapKinds.SIGNINS -> t("Who's Signed In", "Signed-in devices")
    else -> kind
}

fun widgetBlurb(kind: String) = when (kind) {
    NoCapKinds.CLOCK -> t("NoCap glow, digits, analog and more", "Several clock faces, date and next alarm")
    NoCapKinds.WEATHER -> t("Now + 5 days, your city or location", "Current weather and forecast")
    NoCapKinds.CALENDAR -> t("What's coming up (opt-in)", "Upcoming events (needs calendar access)")
    NoCapKinds.STATUS -> t("Server + this phone, live rings", "Your server and this phone at a glance")
    NoCapKinds.SERVER -> t("CPU, RAM, storage, network", "CPU, memory, storage, network")
    NoCapKinds.PHONE -> t("Battery, RAM, storage, Wi-Fi", "Battery, memory, storage, network")
    NoCapKinds.STORAGE -> t("Space left here and on your server", "Free space on the phone and server")
    NoCapKinds.MUSIC -> t("What's playing, with controls", "Media controls (needs notification access)")
    NoCapKinds.TOGGLES -> t("Flashlight, Wi-Fi, DND and more", "Flashlight, Wi-Fi, Do not disturb…")
    NoCapKinds.NOTES -> t("Quick notes or a to-do list", "A note or a checklist, kept on this phone")
    NoCapKinds.PHOTO -> t("Your pics on rotation", "Rotating photos from your gallery")
    NoCapKinds.APPS -> t("Your favorite apps in one tile", "A grid of apps you choose")
    NoCapKinds.SEARCH -> t("Search with voice and AI", "Search, voice and NoCap AI")
    NoCapKinds.QUICK -> t("Desktop, AI, send, clipboard", "Desktop, AI, send a file and more")
    NoCapKinds.AI -> t("Type and ask, right here", "Ask the server's AI Assistant")
    NoCapKinds.FILES -> t("What changed lately on your server", "Recently changed files on your server")
    NoCapKinds.TIMER -> t("Count down to the big day or time stuff", "Count down to a date or run a timer")
    NoCapKinds.SIGNINS -> t("Every device on your NoCap account rn", "Devices signed in to your NoCap account")
    else -> ""
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun <T> Chips(options: List<Pair<String, T>>, selected: (T) -> Boolean, onPick: (T) -> Unit) {
    FlowRow(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
        options.forEach { (label, v) ->
            val on = selected(v)
            Text(
                label, Modifier.clip(RoundedCornerShape(50)).background(if (on) NoCap.Red else NoCap.Field).clickable { onPick(v) }.padding(horizontal = 12.dp, vertical = 7.dp),
                style = MaterialTheme.typography.labelLarge, color = if (on) Color.White else NoCap.Text, maxLines = 1,
            )
        }
    }
}

@Composable
fun Field(value: String, onChange: (String) -> Unit, hint: String, single: Boolean = true, modifier: Modifier = Modifier) {
    Box(modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(NoCap.Field).padding(12.dp)) {
        if (value.isEmpty()) Text(hint, style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
        BasicTextField(value, onChange, singleLine = single, modifier = Modifier.fillMaxWidth(), textStyle = MaterialTheme.typography.bodyMedium.copy(color = NoCap.Text), cursorBrush = SolidColor(NoCap.Red))
    }
}

/** Changes a widget's size in place (or tells you there's no room). */
fun resizeTo(a: MainActivity, item: NoCapItem, w: Int, h: Int) {
    val (page, now) = Home.find(item.id) ?: return
    val c = now.cell
    val want = Cell(c.x.coerceAtMost((com.nocap.link.Prefs.columns - w).coerceAtLeast(0)), c.y.coerceAtMost((com.nocap.link.Prefs.rows - h).coerceAtLeast(0)), w, h)
    if (Home.isFree(page, want, item.id)) Home.replace(now.at(want))
    else a.toast = t("No room for that size here. Move it or clear some space.", "There's no room for that size here.")
}

/** The edit sheet for one of NoCap's own widgets. */
@OptIn(ExperimentalLayoutApi::class, ExperimentalFoundationApi::class)
@Composable
fun WidgetEditSheet(a: MainActivity, start: NoCapItem) {
    val item = (Home.find(start.id)?.second as? NoCapItem) ?: start
    fun put(vararg kv: Pair<String, String?>) = Home.replace(item.with(*kv))
    fun close() { a.editing = null }
    BackHandler { close() }
    Box(Modifier.fillMaxSize().background(Color(0x99000000)).clickable(remember { MutableInteractionSource() }, null) { close() }, contentAlignment = Alignment.BottomCenter) {
        Column(
            Modifier.widthIn(max = 560.dp).fillMaxWidth().heightIn(max = 640.dp).statusBarsPadding()
                .glass(RoundedCornerShape(topStart = 26.dp, topEnd = 26.dp), strong = true)
                .clickable(remember { MutableInteractionSource() }, null) {}
                .navigationBarsPadding().imePadding().padding(horizontal = 18.dp, vertical = 14.dp),
        ) {
            Box(Modifier.align(Alignment.CenterHorizontally).size(40.dp, 4.dp).clip(RoundedCornerShape(2.dp)).background(NoCap.GlassEdge))
            Row(Modifier.fillMaxWidth().padding(top = 10.dp, bottom = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(widgetName(item.kind).uppercase(), Modifier.weight(1f), style = MaterialTheme.typography.headlineSmall)
                Text(t("Done", "Done"), Modifier.clip(RoundedCornerShape(10.dp)).clickable { close() }.padding(8.dp), style = MaterialTheme.typography.labelLarge, color = NoCap.Red)
            }
            Column(Modifier.verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                KindOptions(a, item, ::put)
                Label(t("Size", "Size"))
                Chips(NoCapKinds.sizes(item.kind).map { (w, h) -> "$w×$h" to (w to h) }, { it.first == item.cell.w && it.second == item.cell.h }) { (w, h) -> resizeTo(a, item, w, h) }
                Label(t("Look", "Background"))
                Chips(listOf(t("Glass", "Glass") to "glass", t("Solid", "Solid") to "solid", t("See-through", "None") to "clear"), { it == item.opt("bg", if (item.kind == NoCapKinds.CLOCK && item.opt("face", "glow") != "analog") "clear" else "glass") }) { put("bg" to it) }
                Toggle(t("NoCap glow", "Accent glow"), null, item.flag("glow", true)) { put("glow" to if (it) "1" else "0") }
                Spacer(Modifier.height(10.dp))
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class, ExperimentalMaterial3Api::class)
@Composable
private fun KindOptions(a: MainActivity, item: NoCapItem, put: (Array<out Pair<String, String?>>) -> Unit) {
    fun p(vararg kv: Pair<String, String?>) = put(kv)
    val ctx = LocalContext.current
    when (item.kind) {
        NoCapKinds.CLOCK -> {
            Label(t("Face", "Clock face"))
            Chips(clockFaces.map { faceName(it) to it }, { it == item.opt("face", "glow") }) { p("face" to it) }
            Label(t("Hours", "Time format"))
            Chips(listOf(t("Phone default", "Automatic") to "auto", "12h" to "12", "24h" to "24"), { it == item.opt("h24", "auto") }) { p("h24" to it) }
            Label(t("Alignment", "Alignment"))
            Chips(listOf(t("Left", "Left") to "start", t("Center", "Center") to "center"), { it == item.opt("align", "start") }) { p("align" to it) }
            Toggle(t("Seconds", "Show seconds"), null, item.flag("seconds", false)) { p("seconds" to if (it) "1" else "0") }
            Toggle(t("Date", "Show date"), null, item.flag("date", true)) { p("date" to if (it) "1" else "0") }
            Toggle(t("Next alarm", "Show next alarm"), null, item.flag("alarm", true)) { p("alarm" to if (it) "1" else "0") }
        }
        NoCapKinds.WEATHER -> WeatherOptions(a, item) { kv -> put(kv) }
        NoCapKinds.CALENDAR -> {
            if (!Agenda.allowed(ctx)) Pill(t("Allow calendar access", "Allow calendar access")) { a.askPermission(Manifest.permission.READ_CALENDAR) {} }
            Label(t("Look ahead", "Show events for"))
            Chips(listOf(t("Today", "1 day") to "1", t("3 days", "3 days") to "3", t("A week", "7 days") to "7", t("2 weeks", "14 days") to "14"), { it == item.opt("days", "7") }) { p("days" to it) }
            Toggle(t("All-day stuff", "Show all-day events"), null, item.flag("allday", true)) { p("allday" to if (it) "1" else "0") }
        }
        NoCapKinds.STATUS -> {
            Label(t("Show", "Show"))
            Chips(listOf(t("Both", "Server and phone") to "both", t("Server", "Server") to "server", t("Phone", "This phone") to "phone"), { it == item.opt("show", "both") }) { p("show" to it) }
        }
        NoCapKinds.STORAGE -> Toggle(t("Include my NoCap server", "Include NoCap server"), null, item.flag("server", true)) { p("server" to if (it) "1" else "0") }
        NoCapKinds.MUSIC -> if (!com.nocap.link.system.DotsService.allowed(ctx)) Pill(t("Allow notification access", "Allow notification access")) { com.nocap.link.system.DotsService.openSettings(ctx) }
        NoCapKinds.TOGGLES -> {
            Label(t("Toggles", "Toggles"))
            val cur = item.opt("items", "torch,wifi,bt,dnd,data,location").split(',').filter { it.isNotBlank() }
            Chips(toggleIds.map { toggleName(it) to it }, { it in cur }) { id -> p("items" to (if (id in cur) cur - id else cur + id).joinToString(",")) }
        }
        NoCapKinds.QUICK -> {
            Label(t("Actions", "Actions"))
            val cur = item.opt("actions", "desktop,ai,send,files").split(',').filter { it.isNotBlank() }
            Chips(quickActions.map { quickName(it) to it }, { it in cur }) { id -> p("actions" to (if (id in cur) cur - id else cur + id).joinToString(",")) }
        }
        NoCapKinds.SEARCH -> {
            Toggle(t("Voice button", "Voice search button"), null, item.flag("mic", true)) { p("mic" to if (it) "1" else "0") }
            Toggle(t("AI button", "NoCap AI button"), null, item.flag("ai", true)) { p("ai" to if (it) "1" else "0") }
        }
        NoCapKinds.NOTES -> NotesOptions(item) { kv -> put(kv) }
        NoCapKinds.PHOTO -> {
            val names = item.opt("photos").split('|').filter { it.isNotBlank() }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Pill(t("Add pics", "Add photos")) { a.pickPhotos(item) }
                if (names.isNotEmpty()) Pill(t("Clear all", "Remove all")) { p("photos" to null) }
            }
            Text(t("${names.size} pics on rotation (up to 20).", "${names.size} photos (up to 20)."), style = MaterialTheme.typography.bodySmall)
            Label(t("Switch every", "Change photo every"))
            Chips(listOf(t("Never", "Off") to "0", "5 min" to "5", "15 min" to "15", t("1 hour", "1 hour") to "60"), { it == item.opt("every", "15") }) { p("every" to it) }
            Label(t("Fit", "Fit"))
            Chips(listOf(t("Fill", "Fill") to "crop", t("Whole pic", "Fit") to "fit"), { it == item.opt("fit", "crop") }) { p("fit" to it) }
        }
        NoCapKinds.APPS -> AppsOptions(a, item) { kv -> put(kv) }
        NoCapKinds.TIMER -> TimerOptions(item) { kv -> put(kv) }
        else -> {}
    }
}

@Composable
private fun WeatherOptions(a: MainActivity, item: NoCapItem, put: (Array<out Pair<String, String?>>) -> Unit) {
    val ctx = LocalContext.current
    var q by remember { mutableStateOf("") }
    var results by remember { mutableStateOf<List<Weather.Place>>(emptyList()) }
    var busy by remember { mutableStateOf(false) }
    LaunchedEffect(q) {
        results = emptyList()
        if (q.trim().length < 2) return@LaunchedEffect
        delay(350)
        busy = true
        results = runCatching { withContext(Dispatchers.IO) { Weather.search(q) } }.getOrDefault(emptyList())
        busy = false
    }
    val src = item.opt("src", "shared")
    Label(t("Where", "Location"))
    Chips(
        listOf(
            (Weather.Place.decode(Opt.weatherPlace.v)?.name ?: t("Shared place", "Shared place")) to "shared",
            t("My location", "My location") to "gps",
            (Weather.Place.decode(item.opt("place"))?.name ?: t("A city", "A city")) to "city",
        ),
        { it == src },
    ) { s ->
        if (s == "gps") {
            a.askPermission(Manifest.permission.ACCESS_COARSE_LOCATION) { ok ->
                if (ok) Home.replace(item.with("src" to "gps")) else a.toast = t("No location, no stress — search a city instead.", "Location not allowed. Search for a city instead.")
            }
        } else put(arrayOf("src" to s))
    }
    if (src == "gps" && !Weather.hasLocation(ctx)) Text(t("Location's off for NoCap OS.", "Location access is off."), style = MaterialTheme.typography.bodySmall, color = NoCap.Warn)
    Hint2(t("Only rough location, only when you allow it. Forecasts come from Open-Meteo.", "Uses approximate location only if you allow it. Forecasts by Open-Meteo."))
    SearchField(q, { q = it }, t("Search a city…", "Search for a city"))
    if (busy) Text(t("Looking…", "Searching…"), style = MaterialTheme.typography.bodySmall)
    results.forEach { r ->
        Column(
            Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable {
                put(arrayOf("src" to "city", "place" to r.encode()))
                if (Opt.weatherPlace.v.isBlank()) Opt.weatherPlace.set(r.encode())
                q = ""
            }.padding(8.dp),
        ) {
            Text(r.name, style = MaterialTheme.typography.titleSmall)
            if (r.country.isNotBlank()) Text(r.country, style = MaterialTheme.typography.bodySmall)
        }
    }
    if (src == "city" && item.opt("place").isNotBlank()) {
        Text(
            t("Make this the shared place (other widgets + home screens elsewhere)", "Use this place for all weather widgets"),
            Modifier.clip(RoundedCornerShape(10.dp)).clickable { Opt.weatherPlace.set(item.opt("place")); put(arrayOf("src" to "shared")) }.padding(vertical = 8.dp),
            style = MaterialTheme.typography.titleSmall, color = NoCap.Red,
        )
    }
    Toggle(t("Fahrenheit", "Use °F"), null, Opt.fahrenheit.v) { Opt.fahrenheit.set(it) }
}

@Composable
private fun Hint2(text: String) = Text(text, style = MaterialTheme.typography.bodySmall)

@Composable
private fun NotesOptions(item: NoCapItem, put: (Array<out Pair<String, String?>>) -> Unit) {
    val todo = item.opt("mode", "notes") == "todo"
    Chips(listOf(t("Note", "Note") to "notes", t("To-do list", "Checklist") to "todo"), { it == item.opt("mode", "notes") }) { put(arrayOf("mode" to it)) }
    var title by remember(item.id) { mutableStateOf(item.opt("title")) }
    Field(title, { title = it.take(40); put(arrayOf("title" to title)) }, t("Title", "Title"))
    if (!todo) {
        var text by remember(item.id) { mutableStateOf(item.opt("text")) }
        Field(text, { text = it.take(4000); put(arrayOf("text" to text)) }, t("Write something…", "Write a note"), single = false, modifier = Modifier.heightIn(min = 140.dp))
    } else {
        val list = todos(item)
        var add by remember { mutableStateOf("") }
        list.forEachIndexed { i, td ->
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text(if (td.done) "☑" else "☐", Modifier.clickable { Home.replace(withTodos(item, list.mapIndexed { j, x -> if (j == i) x.copy(done = !x.done) else x })) }.padding(6.dp), color = NoCap.Red)
                Text(td.text, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                Text("✕", Modifier.clickable { Home.replace(withTodos(item, list.filterIndexed { j, _ -> j != i })) }.padding(8.dp), color = NoCap.Muted)
            }
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            Field(add, { add = it.take(200) }, t("Add a to-do", "New item"), modifier = Modifier.weight(1f))
            Text("+", Modifier.padding(start = 8.dp).clip(RoundedCornerShape(10.dp)).background(NoCap.Red).clickable {
                if (add.isNotBlank()) { Home.replace(withTodos(item, list + Todo(add.trim(), false))); add = "" }
            }.padding(horizontal = 16.dp, vertical = 8.dp), style = MaterialTheme.typography.titleMedium, color = Color.White)
        }
        if (list.any { it.done }) Text(t("Clear done", "Remove completed"), Modifier.clickable { Home.replace(withTodos(item, list.filter { !it.done })) }.padding(vertical = 6.dp), color = NoCap.Red, style = MaterialTheme.typography.labelLarge)
    }
}

@Composable
private fun AppsOptions(a: MainActivity, item: NoCapItem, put: (Array<out Pair<String, String?>>) -> Unit) {
    val cur = item.opt("apps").split('|').filter { it.isNotBlank() }
    var q by remember { mutableStateOf("") }
    Toggle(t("App names", "Show labels"), null, item.flag("labels", true)) { put(arrayOf("labels" to if (it) "1" else "0")) }
    Label(t("Picked ${cur.size}/16", "Selected ${cur.size} of 16"))
    SearchField(q, { q = it }, t("Find an app", "Search apps"))
    val list = a.phoneApps.filter { it.key !in com.nocap.link.Prefs.hidden && (q.isBlank() || q.trim().lowercase() in it.label.lowercase()) }
    list.take(60).forEach { app ->
        val on = app.key in cur
        Row(
            Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable {
                val next = if (on) cur - app.key else if (cur.size < 16) cur + app.key else cur
                put(arrayOf("apps" to next.joinToString("|")))
            }.padding(vertical = 5.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            AppIcon(app, 32.dp)
            Text(app.label, Modifier.weight(1f).padding(start = 10.dp), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(if (on) "✓" else "", style = MaterialTheme.typography.titleMedium, color = NoCap.Red)
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun TimerOptions(item: NoCapItem, put: (Array<out Pair<String, String?>>) -> Unit) {
    val countdown = item.opt("mode", "timer") == "countdown"
    Chips(listOf(t("Timer", "Timer") to "timer", t("Countdown to a date", "Countdown to a date") to "countdown"), { it == item.opt("mode", "timer") }) { put(arrayOf("mode" to it)) }
    var title by remember(item.id) { mutableStateOf(item.opt("title")) }
    Field(title, { title = it.take(40); put(arrayOf("title" to title)) }, if (countdown) t("What's the big day?", "Event name") else t("Timer name", "Timer name"))
    if (!countdown) {
        Label(t("Length", "Duration"))
        Chips(listOf(1, 3, 5, 10, 15, 25, 45, 60).map { "$it min" to (it * 60_000L).toString() }, { it == item.opt("dur", "300000") }) { put(arrayOf("dur" to it, "left" to null, "end" to null)) }
        Toggle(
            t("Ring with the clock app", "Ring using the clock app"),
            t("Rings even if your phone is asleep. Off = NoCap OS notification (can be a bit late when the phone's dozing).", "Reliable alarm. When off, NoCap OS posts a notification, which may be delayed while the phone sleeps."),
            item.flag("ring", true),
        ) { put(arrayOf("ring" to if (it) "1" else "0")) }
    } else {
        var pickDate by remember { mutableStateOf(false) }
        var pickTime by remember { mutableStateOf(false) }
        val target = item.opt("target").toLongOrNull()
        val cal = Calendar.getInstance().apply { if (target != null) timeInMillis = target }
        Text(
            if (target != null) android.text.format.DateFormat.format("EEE d MMM yyyy, HH:mm", cal).toString() else t("No date yet", "No date set"),
            style = MaterialTheme.typography.titleSmall,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            Pill(t("Pick date", "Choose date")) { pickDate = true }
            Pill(t("Pick time", "Choose time")) { pickTime = true }
        }
        if (pickDate) {
            val st = rememberDatePickerState(initialSelectedDateMillis = target ?: System.currentTimeMillis())
            DatePickerDialog(
                onDismissRequest = { pickDate = false },
                confirmButton = {
                    TextButton(onClick = {
                        pickDate = false
                        st.selectedDateMillis?.let { utc ->
                            val d = Calendar.getInstance(java.util.TimeZone.getTimeZone("UTC")).apply { timeInMillis = utc }
                            val c = Calendar.getInstance().apply { if (target != null) timeInMillis = target else { set(Calendar.HOUR_OF_DAY, 9); set(Calendar.MINUTE, 0) } }
                            c.set(d.get(Calendar.YEAR), d.get(Calendar.MONTH), d.get(Calendar.DAY_OF_MONTH))
                            c.set(Calendar.SECOND, 0)
                            put(arrayOf("target" to c.timeInMillis.toString()))
                        }
                    }) { Text(t("Bet", "OK"), color = NoCap.Red) }
                },
                dismissButton = { TextButton(onClick = { pickDate = false }) { Text(t("Nah", "Cancel"), color = NoCap.Text) } },
            ) { DatePicker(st) }
        }
        if (pickTime) {
            val st = rememberTimePickerState(cal.get(Calendar.HOUR_OF_DAY), cal.get(Calendar.MINUTE))
            AlertDialog(
                onDismissRequest = { pickTime = false },
                containerColor = NoCap.GlassStrong,
                text = { TimePicker(st) },
                confirmButton = {
                    TextButton(onClick = {
                        pickTime = false
                        val c = Calendar.getInstance().apply { if (target != null) timeInMillis = target; set(Calendar.HOUR_OF_DAY, st.hour); set(Calendar.MINUTE, st.minute); set(Calendar.SECOND, 0) }
                        put(arrayOf("target" to c.timeInMillis.toString()))
                    }) { Text(t("Bet", "OK"), color = NoCap.Red) }
                },
                dismissButton = { TextButton(onClick = { pickTime = false }) { Text(t("Nah", "Cancel"), color = NoCap.Text) } },
            )
        }
    }
}

/** Tapping a weather widget: the next 24 hours and the week. */
@Composable
fun WeatherSheet(a: MainActivity, item: NoCapItem) {
    val ctx = LocalContext.current
    var r by remember { mutableStateOf<Weather.Report?>(null) }
    LaunchedEffect(item.id) {
        val p = withContext(Dispatchers.IO) { weatherPlace(ctx, item) } ?: return@LaunchedEffect
        r = Weather.cached(p, Opt.fahrenheit.v) ?: runCatching { withContext(Dispatchers.IO) { Weather.forecast(p, Opt.fahrenheit.v) } }.getOrNull()
    }
    BackHandler { a.weatherFor = null }
    Box(Modifier.fillMaxSize().background(Color(0x99000000)).clickable(remember { MutableInteractionSource() }, null) { a.weatherFor = null }, contentAlignment = Alignment.BottomCenter) {
        Column(
            Modifier.widthIn(max = 560.dp).fillMaxWidth().glass(RoundedCornerShape(topStart = 26.dp, topEnd = 26.dp), strong = true)
                .clickable(remember { MutableInteractionSource() }, null) {}.navigationBarsPadding().padding(18.dp),
        ) {
            val rep = r
            if (rep == null) { Text(t("Checking the sky…", "Loading…"), style = MaterialTheme.typography.bodyMedium); return@Column }
            Row(verticalAlignment = Alignment.CenterVertically) {
                WeatherGlyph(rep.code, rep.isDay, 72.dp)
                Column(Modifier.weight(1f).padding(start = 12.dp)) {
                    Text(rep.place.name.uppercase(), style = MaterialTheme.typography.headlineSmall)
                    Text("${Weather.deg(rep.temp)} · ${Weather.describe(rep.code)}", style = MaterialTheme.typography.titleMedium)
                    Text(t("Feels ${Weather.deg(rep.feels)} · wind ${rep.wind.toInt()} · humidity ${rep.humidity}%", "Feels like ${Weather.deg(rep.feels)}, wind ${rep.wind.toInt()}, humidity ${rep.humidity}%"), style = MaterialTheme.typography.bodySmall)
                }
            }
            Spacer(Modifier.height(12.dp))
            Label(t("Next hours", "Hourly"))
            Row(Modifier.fillMaxWidth().padding(vertical = 6.dp).horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(14.dp)) {
                rep.hours.forEach { h ->
                    Column(horizontalAlignment = Alignment.CenterHorizontally) {
                        Text(h.time.takeLast(5), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted)
                        WeatherGlyph(h.code, h.isDay, 28.dp)
                        Text(Weather.deg(h.temp), style = MaterialTheme.typography.labelMedium)
                    }
                }
            }
            Label(t("This week", "7 days"))
            rep.days.forEach { d ->
                Row(Modifier.fillMaxWidth().padding(vertical = 3.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text(dayName(d.date), Modifier.widthIn(min = 54.dp), style = MaterialTheme.typography.titleSmall)
                    WeatherGlyph(d.code, true, 26.dp)
                    Text(Weather.describe(d.code), Modifier.weight(1f).padding(start = 8.dp), style = MaterialTheme.typography.bodySmall, maxLines = 1)
                    if (d.rain > 0) Text("${d.rain}%  ", style = MaterialTheme.typography.labelSmall, color = Color(0xFF4FB3FF))
                    Text("${Weather.deg(d.hi)} / ${Weather.deg(d.lo)}", style = MaterialTheme.typography.labelLarge)
                }
            }
            Text("Open-Meteo", Modifier.padding(top = 8.dp), style = MaterialTheme.typography.labelSmall.copy(fontFamily = Orbitron), color = NoCap.Muted)
            Row(Modifier.padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                Pill(t("Edit", "Settings")) { a.weatherFor = null; a.editing = item }
            }
        }
    }
}
