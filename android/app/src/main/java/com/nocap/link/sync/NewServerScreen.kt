package com.nocap.link.sync

import androidx.activity.compose.BackHandler
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
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import com.nocap.link.MainActivity
import com.nocap.link.Screen
import com.nocap.link.data.Link
import com.nocap.link.data.Prefs
import com.nocap.link.data.Vault
import com.nocap.link.data.t
import com.nocap.link.net.ApiError
import com.nocap.link.net.NoCapApi
import com.nocap.link.net.Pins
import com.nocap.link.net.Probe
import com.nocap.link.net.SetupApi
import com.nocap.link.ui.Chip
import com.nocap.link.ui.GhostButton
import com.nocap.link.ui.GlassCard
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.RedButton
import com.nocap.link.ui.SearchField
import com.nocap.link.ui.Wallpaper
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject
import java.util.TimeZone

/** The steps of setting up a new server from the phone. */
private enum class Step { TRUST, ACCOUNT, LOOK, MACHINE, NETWORK, APPLY }

/** One thing being saved on the server, with how it went. */
private class Task(val label: String) {
    var state by mutableStateOf("wait") // wait | run | ok | fail
    var note by mutableStateOf("")
}

/**
 * Set up a brand-new NoCap server from the phone, over the home network,
 * with the same API the server's own setup wizard uses: check and pin its
 * certificate, create the administrator, link this phone as their first
 * device, then the look, name, time zone and (in the full setup on the
 * desktop) the network.
 */
@Composable
fun NewServerScreen(a: MainActivity, start: Screen.NewServer, onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    var step by remember { mutableStateOf(Step.TRUST) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var probe by remember { mutableStateOf<Probe?>(null) }
    var api by remember { mutableStateOf<SetupApi?>(null) }

    var username by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var confirm by remember { mutableStateOf("") }
    var genz by remember { mutableStateOf(Prefs.genz) }
    var style by remember { mutableStateOf("glass") }
    var mode by remember { mutableStateOf("dark") }
    var hostname by remember { mutableStateOf("") }
    var zone by remember { mutableStateOf(TimeZone.getDefault().id) }
    var zoneQuery by remember { mutableStateOf("") }
    var changeNetwork by remember { mutableStateOf(false) }
    val tasks = remember { mutableStateListOf<Task>() }
    var applied by remember { mutableStateOf(false) }

    // Once the account exists there's no going back to the start: the phone is linked.
    BackHandler {
        when (step) {
            Step.TRUST, Step.ACCOUNT -> onBack()
            Step.LOOK -> {}
            Step.MACHINE -> step = Step.LOOK
            Step.NETWORK -> step = Step.MACHINE
            Step.APPLY -> if (applied) a.phoneSetupFinished()
        }
    }

    LaunchedEffect(start) {
        busy = true
        try {
            val p = withContext(Dispatchers.IO) { NoCapApi.probe(start.server) }
            when {
                start.id.isNotBlank() && !p.info.nocapId.equals(start.id, ignoreCase = true) ->
                    error = t("That server's NoCap ID is ${p.info.nocapId}, not ${start.id}. Not touching it, that's sus.", "The server's NoCap ID is ${p.info.nocapId}, not ${start.id}. Setup stopped.")
                p.info.setup == "done" -> {
                    a.toast = t("It's already set up. Link it with a code instead.", "This server is already set up. Link it with a code instead.")
                    a.screen = Screen.LinkForm(server = p.server, id = p.info.nocapId)
                }
                else -> {
                    probe = p
                    hostname = p.info.name.lowercase().filter { it in 'a'..'z' || it in '0'..'9' || it == '-' }.take(63)
                }
            }
        } catch (e: kotlinx.coroutines.CancellationException) {
            throw e
        } catch (e: Exception) {
            error = MainActivity.friendly(e)
        } finally {
            busy = false
        }
    }

    fun createAccount() {
        val p = probe ?: return
        error = when {
            username.isBlank() -> t("Pick a username.", "Enter a username.")
            password.length < 10 -> t("Password needs at least 10 characters.", "The password must be at least 10 characters.")
            password != confirm -> t("Passwords don't match.", "The passwords don't match.")
            else -> null
        }
        if (error != null) return
        busy = true
        scope.launch {
            try {
                val s = SetupApi(p.server, p.pin)
                val name = deviceName()
                val c = withContext(Dispatchers.IO) {
                    s.createAdmin(username.trim(), password)
                    val c = s.linkSelf(name)
                    Vault.get(a).link = Link(p.server, c.token, c.deviceId, c.nocapId.ifBlank { p.info.nocapId }, c.serverName.ifBlank { p.info.name }, c.username, p.pin)
                    c
                }
                api = s
                password = ""
                confirm = ""
                a.linkedFromPhoneSetup()
                username = c.username.ifBlank { username }
                step = Step.LOOK
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: ApiError) {
                error = when (e.code) {
                    "setup_token_required" -> t(
                        "This server only lets phones on its own network set it up. Get on the same Wi-Fi as it and try again.",
                        "This server can only be set up from its own local network. Connect to the same network and try again.",
                    )
                    "setup_done" -> t("Someone just set it up. Link it with a code instead.", "This server was just set up. Link it with a code instead.")
                    else -> e.message?.ifBlank { null } ?: MainActivity.friendly(e)
                }
            } catch (e: Exception) {
                error = MainActivity.friendly(e)
            } finally {
                busy = false
            }
        }
    }

    fun apply() {
        val s = api ?: return
        val p = probe ?: return
        if (busy) return
        busy = true
        error = null
        tasks.clear()
        val wantName = hostname.trim().lowercase()
        val nameTask = if (wantName.isNotEmpty() && wantName != p.info.name.lowercase()) Task(t("Name: $wantName", "Name: $wantName")) else null
        val zoneTask = Task(t("Time zone: ${zone.replace('_', ' ')}", "Time zone: ${zone.replace('_', ' ')}"))
        val lookTask = Task(t("Your look and voice", "Appearance and language"))
        val doneTask = if (!changeNetwork) Task(t("Wrapping up", "Finishing setup")) else null
        listOfNotNull(nameTask, zoneTask, lookTask, doneTask).forEach { tasks += it }
        step = Step.APPLY
        scope.launch {
            suspend fun run(task: Task?, block: () -> Unit) {
                task ?: return
                task.state = "run"
                try {
                    withContext(Dispatchers.IO) { block() }
                    task.state = "ok"
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: ApiError) {
                    task.state = "fail"
                    task.note = if (e.status == 501) t("Not on this server, kept as is.", "Not supported on this server; left unchanged.") else e.message.orEmpty()
                } catch (e: Exception) {
                    task.state = "fail"
                    task.note = MainActivity.friendly(e)
                }
            }
            run(nameTask) { s.setHostname(wantName) }
            run(zoneTask) { s.setTimeZone(zone) }
            run(lookTask) {
                // The desktop's own prefs: the voice and look, and whether
                // its first-start setup still has to run (only to change
                // the network there).
                s.savePrefs(
                    JSONObject().put("lingo", if (genz) "genz" else "standard").put("uiTheme", style).put("theme", if (style == "classic") mode else "dark")
                        .put("welcomeDone", !changeNetwork).put("welcomeStep", 0)
                        .put("brand1", true).put("glass1", true).put("look2", true).put("welcome1", true),
                )
            }
            run(doneTask) { s.machineDone() }
            applied = true
            busy = false
        }
    }

    Wallpaper {
        Column(
            Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().imePadding().verticalScroll(rememberScrollState()).padding(20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Column(Modifier.widthIn(max = 560.dp).fillMaxWidth()) {
                if (step == Step.TRUST || step == Step.ACCOUNT) {
                    IconButton(onClick = onBack) { Icon(Icons.Filled.ArrowBack, contentDescription = t("Back", "Back"), tint = NoCap.Text) }
                } else {
                    Spacer(Modifier.height(16.dp))
                }
                Label(t("New server setup", "New server setup"), color = NoCap.Red)
                Text((probe?.info?.name ?: start.name).ifBlank { t("Your NoCap server", "NoCap server") }, style = MaterialTheme.typography.headlineSmall)
                Spacer(Modifier.height(10.dp))
                Progress(step)
                Spacer(Modifier.height(18.dp))

                when (step) {
                    Step.TRUST -> TrustStep(probe, busy) { step = Step.ACCOUNT }
                    Step.ACCOUNT -> {
                        Text(t("Make the admin account", "Create the administrator account"), style = MaterialTheme.typography.titleLarge)
                        Text(
                            t(
                                "This is the main account on the server: you sign in to NoCapOS with it, and this phone gets linked to it.",
                                "This is the server's main account. You'll use it to sign in to NoCapOS, and this phone will be linked to it.",
                            ),
                            style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted,
                        )
                        Spacer(Modifier.height(14.dp))
                        GlassCard(Modifier.fillMaxWidth()) {
                            Column(verticalArrangement = Arrangement.spacedBy(12.dp)) {
                                Field(t("Username", "Username"), username, { username = it.trim().take(32) }, KeyboardType.Text)
                                Field(t("Password", "Password"), password, { password = it.take(256) }, KeyboardType.Password, secret = true)
                                Field(t("Password again", "Confirm password"), confirm, { confirm = it.take(256) }, KeyboardType.Password, secret = true)
                            }
                        }
                        Spacer(Modifier.height(16.dp))
                        if (busy) Busy(t("Setting it up…", "Creating the account…")) else RedButton(t("Create account + link this phone", "Create account and link this phone"), { createAccount() }, Modifier.fillMaxWidth())
                    }
                    Step.LOOK -> {
                        Text(t("Your vibe", "Language and appearance"), style = MaterialTheme.typography.titleLarge)
                        Text(t("How NoCapOS talks and looks for ${username.ifBlank { "you" }}. Change it anytime in Settings.", "How NoCapOS reads and looks for ${username.ifBlank { "you" }}. You can change this later in Settings."), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
                        Spacer(Modifier.height(14.dp))
                        GlassCard(Modifier.fillMaxWidth()) {
                            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                                Label(t("Voice", "Language style"))
                                Choices(listOf(true to t("Gen Z", "Gen Z"), false to t("Standard", "Standard")), genz) { genz = it }
                                Label(t("Style", "Theme"))
                                Choices(listOf("glass" to t("Glass", "Glass"), "classic" to t("Classic", "Classic"), "cyberdeck" to t("Cyberdeck", "Cyberdeck")), style) { style = it }
                                if (style == "classic") {
                                    Label(t("Mode", "Mode"))
                                    Choices(listOf("dark" to t("Dark", "Dark"), "light" to t("Light", "Light"), "auto" to t("Auto", "Automatic")), mode) { mode = it }
                                }
                            }
                        }
                        Spacer(Modifier.height(16.dp))
                        RedButton(t("Next", "Next"), { step = Step.MACHINE }, Modifier.fillMaxWidth())
                    }
                    Step.MACHINE -> {
                        Text(t("Name + time zone", "Name and time zone"), style = MaterialTheme.typography.titleLarge)
                        Text(t("The name it shows on your network, and its clock.", "The server's name on your network, and its time zone."), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
                        Spacer(Modifier.height(14.dp))
                        GlassCard(Modifier.fillMaxWidth()) {
                            Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                                Field(t("Server name", "Hostname"), hostname, { v -> hostname = v.lowercase().filter { it in 'a'..'z' || it in '0'..'9' || it == '-' }.take(63) }, KeyboardType.Ascii)
                                Label(t("Time zone", "Time zone"))
                                Text(zone.replace('_', ' '), style = MaterialTheme.typography.titleMedium)
                                SearchField(zoneQuery, { zoneQuery = it }, t("Search a city", "Search for a city"))
                                if (zoneQuery.isNotBlank()) {
                                    val q = zoneQuery.trim().replace(' ', '_')
                                    zones().filter { it.contains(q, ignoreCase = true) }.take(8).forEach { z ->
                                        Text(
                                            z.replace('_', ' '),
                                            Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).clickable { zone = z; zoneQuery = "" }.padding(vertical = 8.dp, horizontal = 6.dp),
                                            style = MaterialTheme.typography.bodyMedium,
                                        )
                                    }
                                }
                            }
                        }
                        Spacer(Modifier.height(16.dp))
                        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            GhostButton(t("Back", "Back"), { step = Step.LOOK }, Modifier.weight(1f))
                            RedButton(t("Next", "Next"), { step = Step.NETWORK }, Modifier.weight(1f))
                        }
                    }
                    Step.NETWORK -> {
                        Text(t("Network", "Network"), style = MaterialTheme.typography.titleLarge)
                        Text(
                            t(
                                "It's on your network at ${probe?.server?.substringAfter("://").orEmpty()}. Keep that, or pick a fixed address in the full setup on the desktop (it checks the new address works before keeping it).",
                                "The server is reachable at ${probe?.server?.substringAfter("://").orEmpty()}. Keep this setup, or set a fixed address in the full setup on the desktop, which confirms the new address works before keeping it.",
                            ),
                            style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted,
                        )
                        Spacer(Modifier.height(14.dp))
                        Option(t("Keep it as is (recommended)", "Keep the current setup (recommended)"), !changeNetwork) { changeNetwork = false }
                        Spacer(Modifier.height(8.dp))
                        Option(t("Change it in the full setup on the desktop", "Change it in the full setup on the desktop"), changeNetwork) { changeNetwork = true }
                        Spacer(Modifier.height(16.dp))
                        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            GhostButton(t("Back", "Back"), { step = Step.MACHINE }, Modifier.weight(1f))
                            RedButton(t("Apply", "Apply"), { apply() }, Modifier.weight(1f))
                        }
                    }
                    Step.APPLY -> {
                        Text(if (applied) t("You're all set", "Setup complete") else t("Saving…", "Applying…"), style = MaterialTheme.typography.titleLarge)
                        Spacer(Modifier.height(12.dp))
                        GlassCard(Modifier.fillMaxWidth()) {
                            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                                Row(verticalAlignment = Alignment.CenterVertically) {
                                    Text(t("Admin account + this phone linked", "Administrator account created, phone linked"), Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
                                    Chip(t("Done", "Done"), NoCap.Ok)
                                }
                                tasks.forEach { task ->
                                    Row(verticalAlignment = Alignment.CenterVertically) {
                                        Column(Modifier.weight(1f)) {
                                            Text(task.label, style = MaterialTheme.typography.bodyMedium)
                                            if (task.note.isNotBlank()) Text(task.note, style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
                                        }
                                        when (task.state) {
                                            "run" -> CircularProgressIndicator(Modifier.size(18.dp), color = NoCap.Red, strokeWidth = 2.dp)
                                            "ok" -> Chip(t("Done", "Done"), NoCap.Ok)
                                            "fail" -> Chip(t("Skipped", "Not changed"), NoCap.Warn)
                                            else -> Chip(t("Up next", "Waiting"), NoCap.Muted)
                                        }
                                    }
                                }
                            }
                        }
                        if (applied) {
                            Spacer(Modifier.height(16.dp))
                            if (changeNetwork) {
                                Text(t("Next: the network, in the full setup on your desktop.", "Next: set up the network in the full setup on the desktop."), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
                                Spacer(Modifier.height(10.dp))
                                RedButton(t("Open the full setup", "Open the full setup"), { a.phoneSetupFinished(); a.openDesktop() }, Modifier.fillMaxWidth())
                                Spacer(Modifier.height(8.dp))
                                GhostButton(t("Later", "Later"), { a.phoneSetupFinished() }, Modifier.fillMaxWidth())
                            } else {
                                RedButton(t("Let's go", "Continue"), { a.phoneSetupFinished() }, Modifier.fillMaxWidth())
                                Spacer(Modifier.height(8.dp))
                                GhostButton(t("Open my NoCap desktop", "Open the NoCap desktop"), { a.phoneSetupFinished(); a.openDesktop() }, Modifier.fillMaxWidth())
                            }
                        }
                    }
                }
                error?.let {
                    Spacer(Modifier.height(12.dp))
                    Text(it, style = MaterialTheme.typography.bodyMedium, color = NoCap.Bad)
                }
            }
        }
    }
}

@Composable
private fun TrustStep(probe: Probe?, busy: Boolean, next: () -> Unit) {
    if (probe == null) {
        if (busy) Busy(t("Saying hi to the server…", "Connecting to the server…"))
        return
    }
    Text(t("Is this your server?", "Confirm the server"), style = MaterialTheme.typography.titleLarge)
    Text(
        t(
            "It's brand new and waiting for its first account. Check the NoCap ID matches the one on its screen.",
            "This server hasn't been set up yet. Check that the NoCap ID matches the one shown on the server's screen.",
        ),
        style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted,
    )
    Spacer(Modifier.height(14.dp))
    GlassCard(Modifier.fillMaxWidth()) {
        Column {
            Text(probe.server.substringAfter("://"), style = MaterialTheme.typography.titleMedium)
            if (probe.info.version.isNotBlank()) Text(t("NoCapOS v${probe.info.version}", "NoCapOS version ${probe.info.version}"), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
            Spacer(Modifier.height(8.dp))
            Text("NoCap ID: ${probe.info.nocapId}", style = MaterialTheme.typography.bodyMedium)
            Spacer(Modifier.height(8.dp))
            val pin = probe.pin
            if (pin != null) {
                Text(t("Certificate (SHA-256). This phone will only ever trust this one:", "Certificate (SHA-256). This phone will only trust this certificate:"), style = MaterialTheme.typography.bodySmall)
                Text(Pins.pretty(pin), style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace), color = NoCap.Text)
            } else {
                Text(t("Heads up: plain http, so it isn't encrypted. Only do this on your own Wi-Fi.", "Warning: this address is plain http, so the connection isn't encrypted. Only continue on your own network."), style = MaterialTheme.typography.bodyMedium, color = NoCap.Warn)
            }
        }
    }
    Spacer(Modifier.height(16.dp))
    RedButton(t("Trust + set it up", "Trust and set up"), next, Modifier.fillMaxWidth())
}

@Composable
private fun Progress(step: Step) {
    Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
        Step.entries.forEach { s ->
            val c = when {
                s == step -> NoCap.Text
                s.ordinal < step.ordinal -> NoCap.Ok
                else -> NoCap.Field
            }
            Box(Modifier.weight(1f).height(4.dp).clip(RoundedCornerShape(50)).background(c))
        }
    }
}

@Composable
private fun Busy(text: String) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        CircularProgressIndicator(Modifier.size(22.dp), color = NoCap.Red, strokeWidth = 2.dp)
        Text(text, Modifier.padding(start = 12.dp), style = MaterialTheme.typography.bodyMedium)
    }
}

@Composable
private fun <T> Choices(options: List<Pair<T, String>>, value: T, onPick: (T) -> Unit) {
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        options.forEach { (v, label) ->
            val on = v == value
            Text(
                label,
                Modifier.weight(1f).clip(RoundedCornerShape(12.dp)).background(if (on) NoCap.Red else NoCap.Field).clickable { onPick(v) }.padding(vertical = 10.dp),
                style = MaterialTheme.typography.labelLarge, color = if (on) Color.White else NoCap.Text,
                textAlign = androidx.compose.ui.text.style.TextAlign.Center,
            )
        }
    }
}

@Composable
private fun Option(text: String, on: Boolean, onPick: () -> Unit) {
    GlassCard(Modifier.fillMaxWidth().clip(RoundedCornerShape(18.dp)).clickable(onClick = onPick)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(18.dp).clip(RoundedCornerShape(50)).background(if (on) NoCap.Red else NoCap.Field))
            Text(text, Modifier.padding(start = 12.dp), style = MaterialTheme.typography.bodyLarge)
        }
    }
}

@Composable
private fun Field(label: String, value: String, onChange: (String) -> Unit, type: KeyboardType, secret: Boolean = false) {
    OutlinedTextField(
        value = value, onValueChange = onChange, singleLine = true, modifier = Modifier.fillMaxWidth(),
        label = { Text(label) },
        visualTransformation = if (secret) PasswordVisualTransformation() else VisualTransformation.None,
        keyboardOptions = KeyboardOptions(keyboardType = type, autoCorrectEnabled = false),
        textStyle = MaterialTheme.typography.bodyLarge,
        shape = RoundedCornerShape(12.dp),
        colors = OutlinedTextFieldDefaults.colors(
            focusedBorderColor = NoCap.Red, unfocusedBorderColor = NoCap.GlassEdge,
            focusedLabelColor = NoCap.Red, unfocusedLabelColor = NoCap.Muted, cursorColor = NoCap.Red,
        ),
    )
}

/** Time zones people pick from: Region/City names, plus UTC. */
private val zoneList: List<String> by lazy {
    (TimeZone.getAvailableIDs().filter { it.contains('/') && !it.startsWith("Etc/") && !it.startsWith("SystemV/") && it.first().isUpperCase() } + "UTC").distinct().sorted()
}

private fun zones() = zoneList
