package com.nocap.link.shield

import android.content.Context
import android.text.format.DateUtils
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
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
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.graphics.drawable.toBitmap
import com.nocap.link.data.Vault
import com.nocap.link.data.t
import com.nocap.link.ui.CardBlock
import com.nocap.link.ui.Chip
import com.nocap.link.ui.NavRow
import com.nocap.link.ui.NcDialog
import com.nocap.link.ui.NcIcon
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.PageHeader
import com.nocap.link.ui.PillButton
import com.nocap.link.ui.SearchField
import com.nocap.link.ui.SectionCard
import com.nocap.link.ui.SwitchRow
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private val ShieldBlue = Color(0xFF30A0FF)

private enum class Tab { DNS, FIREWALL, ACTIVITY }

/** The Shield page: on/off, the DNS filter, the app firewall and the local logs. */
@Composable
fun ShieldScreen(a: ShieldActivity) {
    BackHandler { a.finish() }
    var tab by remember { mutableStateOf(Tab.DNS) }
    var server by remember { mutableStateOf<ServerShield?>(null) }
    var serverErr by remember { mutableStateOf<String?>(null) }
    var pick by remember { mutableStateOf<BlockedName?>(null) }
    var appPick by remember { mutableStateOf<Shield.App?>(null) }
    var query by remember { mutableStateOf("") }
    var showSystem by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    val link = remember { runCatching { Vault.get(a).link }.getOrNull() }
    val tick = ShieldStore.logTick
    val names = remember(tick) { ShieldStore.recentNames() }
    val attempts = remember(tick) { ShieldStore.recentAttempts() }
    val apps by produceState(emptyList<Shield.App>(), tab) {
        if (tab == Tab.FIREWALL && value.isEmpty()) value = withContext(Dispatchers.IO) { Shield.apps(a) }
    }

    fun serverCall(block: (ShieldApi) -> ServerShield) {
        val l = link ?: return
        scope.launch {
            busy = true
            try {
                server = withContext(Dispatchers.IO) { block(ShieldApi(l)) }
                serverErr = null
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                serverErr = when (e) {
                    is com.nocap.link.net.ServerTooOld -> t("Your server needs a NoCapOS update for this.", "Your server needs a NoCapOS update for this.")
                    else -> e.message ?: t("Couldn't reach your server.", "Couldn't reach your server.")
                }
            } finally {
                busy = false
            }
        }
    }
    LaunchedEffect(link, a.resumeTick) { if (link != null) serverCall { it.get() } }

    Column(Modifier.fillMaxSize().background(NoCap.Sheet).statusBarsPadding().navigationBarsPadding(), horizontalAlignment = Alignment.CenterHorizontally) {
        Row(Modifier.widthIn(max = 720.dp).fillMaxWidth().padding(start = 8.dp, end = 8.dp, top = 8.dp, bottom = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(44.dp).clip(CircleShape).clickable { a.finish() }, contentAlignment = Alignment.Center) {
                NcIcon("chevronLeft", NoCap.Text, 22.dp, desc = t("Back", "Back"))
            }
            Text("Shield", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium.copy(fontSize = 22.sp))
        }
        LazyColumn(
            Modifier.widthIn(max = 720.dp).fillMaxWidth(),
            contentPadding = androidx.compose.foundation.layout.PaddingValues(start = 14.dp, end = 14.dp, top = 6.dp, bottom = 32.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            item {
                PageHeader(
                    "Shield",
                    t("Ads, trackers and sketchy apps: blocked on this phone", "Ad and tracker blocking and an app firewall for this phone"),
                    "shield", ShieldBlue,
                )
            }
            item { StatusCard(a, server, link != null) }
            item {
                Tabs(tab) { tab = it }
            }
            when (tab) {
                Tab.DNS -> {
                    item { DnsCard(a, server, serverErr, link != null) }
                    item { TrustedCard(a) }
                    item {
                        SectionCard(t("Recently blocked", "Recently blocked"), sub = t("On this phone only. Tap one to let it through.", "Kept on this phone. Tap a name to allow it.")) {
                            if (names.isEmpty()) CardBlock { Text(t("Nothing blocked yet.", "Nothing blocked yet."), color = NoCap.Muted) }
                            names.take(40).forEach { n ->
                                NavRow(
                                    n.name,
                                    sub = listOfNotNull(n.app, ago(n.at), if (n.bySelf) t("blocked by the fallback", "blocked by the fallback") else null).joinToString(" · "),
                                    chevron = false,
                                ) { pick = n }
                            }
                        }
                    }
                    if (link != null) item { MyLists(server, busy, { d, act, sc -> serverCall { it.rule(d, act, sc) } }) }
                }
                Tab.FIREWALL -> {
                    item { FirewallCard(a) }
                    item {
                        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            SearchField(query, { query = it }, t("Search apps…", "Search apps"))
                            SectionCard {
                                SwitchRow(t("Show system apps", "Show system apps"), null, showSystem) { showSystem = it }
                            }
                        }
                    }
                    val shown = apps.filter { (showSystem || !it.system || ShieldStore.rules.containsKey(it.pkg)) && (query.isBlank() || it.label.contains(query, true) || it.pkg.contains(query, true)) }
                    if (apps.isEmpty()) item { Text(t("Loading your apps…", "Loading apps…"), color = NoCap.Muted, modifier = Modifier.padding(8.dp)) }
                    items(shown, key = { it.pkg }) { app ->
                        AppRow(a, app, ShieldStore.rules[app.pkg] ?: AppRule.ALLOW) { appPick = app }
                    }
                }
                Tab.ACTIVITY -> {
                    item {
                        SectionCard(
                            t("Blocked app attempts", "Blocked connection attempts"),
                            sub = t("Apps the firewall stopped. Only on this phone.", "Connections stopped by the firewall. Kept on this phone only."),
                        ) {
                            if (attempts.isEmpty()) CardBlock { Text(t("No attempts yet.", "No attempts yet."), color = NoCap.Muted) }
                            attempts.take(100).forEach { x -> NavRow(x.app, sub = "${x.dest} · ${ago(x.at)}", chevron = false) {} }
                        }
                    }
                    item {
                        SectionCard(footer = t(
                            "Shield's logs never leave this phone. Your server's own Shield log keeps what your phone asked it, like any device on your network.",
                            "Shield's logs stay on this phone. Your server's Shield query log records what this phone asked it, like any other device.",
                        )) {
                            NavRow(t("Clear the logs", "Clear logs"), danger = true) { ShieldStore.clearLogs() }
                        }
                    }
                }
            }
            item { AboutCard(a) }
        }
    }

    pick?.let { n ->
        NcDialog(
            n.name,
            t("Let it through? This goes on your own lists on your NoCap server.", "Allow this domain? It's added to your own lists on your NoCap server."),
            onDismiss = { pick = null },
            confirm = t("This phone", "On this phone") to { pick = null; serverCall { it.rule(n.name, "allow", "device") } },
            dismiss = t("Nah", "Cancel") to { pick = null },
            extra = listOf(t("All my devices", "On all my devices") to { pick = null; serverCall { it.rule(n.name, "allow", "user") } }),
        )
    }
    appPick?.let { app ->
        RuleDialog(app, ShieldStore.rules[app.pkg] ?: AppRule.ALLOW, { appPick = null }) { r ->
            ShieldStore.setRule(app.pkg, r)
            Shield.reload()
            appPick = null
        }
    }
    if (a.explain) {
        NcDialog(
            t("Shield needs Android's VPN slot", "Shield uses Android's VPN"),
            t(
                "Next, Android asks if NoCap OS can set up a VPN. That's how Shield sees DNS lookups and blocks apps: only lookups go to your own NoCap server, nothing else leaves your phone. You'll see a key icon up top while it's on, and it can't run next to another VPN app.",
                "Android will now ask to allow a VPN connection. Shield uses it to filter DNS lookups and block apps: only DNS lookups go to your own NoCap server, and nothing else leaves the phone. A key icon appears in the status bar while it's on, and it can't run at the same time as another VPN app.",
            ),
            onDismiss = { a.explain = false },
            confirm = t("Let's go", "Continue") to { a.askAndroid() },
            dismiss = t("Not now", "Cancel") to { a.explain = false },
        )
    }
    if (a.askLocation) {
        NcDialog(
            t("Android hides Wi-Fi names", "Location access needed"),
            t(
                "To know which Wi-Fi you're on, Android wants location access. NoCap OS only reads the Wi-Fi name, on this phone, and never your location. For Shield to notice while NoCap OS is closed, pick \"Allow all the time\" in App info → Permissions → Location.",
                "Android only shares the Wi-Fi network name with apps that have location access. NoCap OS reads just the network name, on this phone, never your location. For Shield to recognise the network while the app is closed, choose \"Allow all the time\" under App info → Permissions → Location.",
            ),
            onDismiss = { a.askLocation = false },
            confirm = t("OK", "Continue") to { a.requestLocation() },
            dismiss = t("Nah", "Cancel") to { a.askLocation = false },
        )
    }
}

@Composable
private fun Tabs(tab: Tab, onPick: (Tab) -> Unit) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(50)).background(NoCap.Field).padding(4.dp),
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        listOf(Tab.DNS to t("DNS filter", "DNS filter"), Tab.FIREWALL to t("Firewall", "Firewall"), Tab.ACTIVITY to t("Activity", "Activity")).forEach { (k, label) ->
            val on = k == tab
            Box(
                Modifier.weight(1f).heightIn(min = 40.dp).clip(RoundedCornerShape(50)).background(if (on) NoCap.Red else Color.Transparent).clickable { onPick(k) },
                contentAlignment = Alignment.Center,
            ) {
                Text(label, style = MaterialTheme.typography.labelLarge, color = if (on) Color.White else NoCap.Text, maxLines = 1)
            }
        }
    }
}

@Composable
private fun StatusCard(a: ShieldActivity, server: ServerShield?, linked: Boolean) {
    val on = ShieldStore.enabled
    val blocked = remember(ShieldStore.logTick, on) { ShieldStore.blockedToday() }
    val asked = remember(ShieldStore.logTick, on) { ShieldStore.askedToday() }
    val state = when {
        !on -> t("Off. Tap the switch to lock in.", "Off.")
        !ShieldStore.running -> t("Starting…", "Starting…")
        ShieldStore.mode == Mode.PASS -> t("Chilling on this trusted Wi-Fi.", "Paused on this trusted Wi-Fi network.")
        ShieldStore.mode == Mode.OFF -> t("Nothing to do on this network rn.", "Nothing to filter on this network.")
        ShieldStore.dnsPaused -> t("Hard block is on, so the DNS filter waits while apps are blocked here.", "Hard block is on: the DNS filter is paused while apps are blocked on this network.")
        ShieldStore.mode == Mode.FIREWALL -> t("Firewall only.", "Firewall only.")
        !linked -> t("No server linked: using normal DNS, nothing filtered.", "No server linked: lookups use normal DNS without filtering.")
        server?.available == false -> t("Your server's DNS blocker is off. Turn it on in Shield on NoCapOS.", "The DNS blocker is off on your server. Turn it on in NoCapOS Shield.")
        !ShieldStore.serverUp -> if (ShieldStore.fallback == Fallback.OPEN) t("Server's out of reach: normal DNS for now.", "Server unreachable: using normal DNS for now.") else t("Server's out of reach: blocking every lookup.", "Server unreachable: all lookups are blocked.")
        else -> t("Filtering through your NoCap server.", "Filtering through your NoCap server.")
    }
    SectionCard {
        Row(Modifier.fillMaxWidth().padding(16.dp), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(if (on) t("Shield's up", "Shield is on") else t("Shield's off", "Shield is off"), style = MaterialTheme.typography.titleMedium.copy(fontWeight = FontWeight.Bold))
                Text(state, style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
            }
            com.nocap.link.ui.NcSwitch(on, onChange = { v -> if (v) a.turnOn() else a.turnOff() })
        }
        Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Stat(t("Blocked today", "Blocked today"), "$blocked", Modifier.weight(1f))
            Stat(t("Lookups today", "Lookups today"), "$asked", Modifier.weight(1f))
            Stat(t("Apps blocked", "Blocked apps"), "${ShieldStore.rules.count { it.value != AppRule.ALLOW }}", Modifier.weight(1f))
        }
    }
}

@Composable
private fun Stat(label: String, value: String, modifier: Modifier) {
    Column(modifier) {
        Text(value, style = MaterialTheme.typography.titleLarge.copy(fontWeight = FontWeight.Bold), color = NoCap.Text)
        Text(label, style = MaterialTheme.typography.bodySmall, color = NoCap.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

@Composable
private fun DnsCard(a: ShieldActivity, server: ServerShield?, err: String?, linked: Boolean) {
    SectionCard(
        t("DNS filter", "DNS filter"),
        sub = t("Lookups go to your NoCap server's Shield, at home and away", "DNS lookups are answered by your NoCap server's Shield, at home and away"),
    ) {
        SwitchRow(t("Filter ads and trackers", "Filter ads and trackers"), null, ShieldStore.dns) { ShieldStore.saveDns(it); Shield.reload() }
        if (!linked) {
            CardBlock { Text(t("Link your NoCap server to filter. The firewall works without it.", "Link a NoCap server to use the DNS filter. The firewall works without one."), color = NoCap.Muted, style = MaterialTheme.typography.bodySmall) }
        } else if (err != null) {
            CardBlock { Text(err, color = NoCap.Warn, style = MaterialTheme.typography.bodySmall) }
        } else if (server?.available == false) {
            CardBlock { Text(t("Your server's DNS blocker is off: turn it on in Shield on NoCapOS.", "The DNS blocker is off on your server: turn it on in NoCapOS Shield."), color = NoCap.Warn, style = MaterialTheme.typography.bodySmall) }
        }
        CardBlock {
            Text(t("If your server can't be reached", "When your server can't be reached"), style = MaterialTheme.typography.titleSmall)
            Choice(
                ShieldStore.fallback == Fallback.OPEN,
                t("Let it all through (normal DNS)", "Allow everything (normal DNS)"),
                t("Your phone's usual DNS answers until the server's back. Nothing blocked meanwhile.", "The phone's normal DNS is used until the server is back. Nothing is blocked meanwhile."),
            ) { ShieldStore.saveFallback(Fallback.OPEN) }
            Choice(
                ShieldStore.fallback == Fallback.BLOCK,
                t("Block everything", "Block all lookups"),
                t("No internet by name until the server's back. Strict mode.", "No lookups work until the server is back."),
            ) { ShieldStore.saveFallback(Fallback.BLOCK) }
        }
    }
}

@Composable
private fun Choice(on: Boolean, title: String, sub: String, onClick: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).background(if (on) NoCap.Red.copy(alpha = 0.12f) else Color.Transparent)
            .border(1.dp, if (on) NoCap.Red.copy(alpha = 0.5f) else NoCap.GlassEdge, RoundedCornerShape(12.dp))
            .clickable(onClick = onClick).padding(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(18.dp).clip(CircleShape).border(2.dp, if (on) NoCap.Red else NoCap.Muted, CircleShape), contentAlignment = Alignment.Center) {
            if (on) Box(Modifier.size(9.dp).clip(CircleShape).background(NoCap.Red))
        }
        Column(Modifier.padding(start = 12.dp)) {
            Text(title, style = MaterialTheme.typography.titleSmall)
            Text(sub, style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
        }
    }
}

@Composable
private fun TrustedCard(a: ShieldActivity) {
    val bgOk = remember(a.resumeTick) { a.hasBackgroundLocation() }
    SectionCard(
        t("Trusted Wi-Fi", "Trusted Wi-Fi networks"),
        sub = t("The DNS filter chills on these (the firewall keeps going)", "The DNS filter is off on these networks (the firewall still applies)"),
    ) {
        if (ShieldStore.trusted.isEmpty()) CardBlock { Text(t("None yet.", "None."), color = NoCap.Muted) }
        ShieldStore.trusted.sorted().forEach { ssid ->
            NavRow(ssid, icon = "wifi", iconColor = ShieldBlue, value = t("Remove", "Remove"), chevron = false) {
                ShieldStore.trust(ssid, false)
                Shield.reload()
            }
        }
        NavRow(t("Add the Wi-Fi I'm on", "Add current Wi-Fi network"), icon = "plus", iconColor = NoCap.Ok) { a.addCurrentWifi() }
        if (ShieldStore.trusted.isNotEmpty() && !bgOk) {
            NavRow(
                t("Let Shield see Wi-Fi while closed", "Allow location all the time"),
                sub = t("Without it, Shield stays on everywhere once NoCap OS is in the background (the safe side).", "Without it, Shield can't recognise the network while the app is in the background, and stays on (the safe default)."),
                icon = "alert", iconColor = NoCap.Warn,
            ) { a.openAppSettings() }
        }
    }
}

@Composable
private fun FirewallCard(a: ShieldActivity) {
    SectionCard(
        t("App firewall", "App firewall"),
        sub = t("Pick what each app gets: everything, no Wi-Fi, no data, or nothing", "Allow each app everything, no Wi-Fi, no mobile data, or nothing"),
    ) {
        SwitchRow(t("Firewall", "Firewall"), null, ShieldStore.firewall) { ShieldStore.saveFirewall(it); Shield.reload() }
        SwitchRow(
            t("Block new apps by default", "Block new apps by default"),
            t("Apps installed from now on start fully blocked until you allow them.", "Apps installed from now on are blocked until you allow them."),
            ShieldStore.blockNew,
        ) { v ->
            val installed = Shield.installedApps(a)
            ShieldStore.setBlockNew(v, installed)
            Shield.reload()
        }
        SwitchRow(
            t("Hard block", "Hard block"),
            t(
                "Blocked apps get zero traffic, even apps that skip DNS. Android allows one VPN, so the DNS filter waits while apps are blocked on the network you're on.",
                "Blocked apps get no traffic at all, including apps that don't use DNS. Because Android allows one VPN, the DNS filter pauses while any app is blocked on the current network.",
            ),
            ShieldStore.hardBlock,
        ) { ShieldStore.saveHardBlock(it); Shield.reload() }
        CardBlock {
            Text(
                t(
                    "Without hard block, blocked apps just can't look anything up (Android 10+), and the DNS filter keeps running for everyone else.",
                    "Without hard block, blocked apps can't resolve any names (Android 10 and later) while the DNS filter keeps running for other apps.",
                ),
                style = MaterialTheme.typography.bodySmall, color = NoCap.Muted,
            )
        }
    }
}

@Composable
private fun AppRow(ctx: Context, app: Shield.App, rule: AppRule, onClick: () -> Unit) {
    val icon by produceState<ImageBitmap?>(AppIcons.cached(app.pkg), app.pkg) {
        if (value == null) value = withContext(Dispatchers.IO) { AppIcons.load(ctx, app.pkg) }
    }
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(14.dp)).background(NoCap.Card).border(1.dp, NoCap.GlassEdge, RoundedCornerShape(14.dp))
            .clickable(onClick = onClick).padding(horizontal = 12.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        val img = icon
        if (img != null) Image(img, null, Modifier.size(34.dp)) else Box(Modifier.size(34.dp).clip(RoundedCornerShape(9.dp)).background(NoCap.Field))
        Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
            Text(app.label, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(app.pkg, style = MaterialTheme.typography.bodySmall, color = NoCap.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        Chip(ruleName(rule), if (rule == AppRule.ALLOW) NoCap.Ok else if (rule == AppRule.BLOCK) NoCap.Bad else NoCap.Warn)
    }
}

private fun ruleName(r: AppRule) = when (r) {
    AppRule.ALLOW -> t("Allowed", "Allowed")
    AppRule.NO_WIFI -> t("No Wi-Fi", "No Wi-Fi")
    AppRule.NO_DATA -> t("No data", "No mobile data")
    AppRule.BLOCK -> t("Blocked", "Blocked")
}

@Composable
private fun RuleDialog(app: Shield.App, rule: AppRule, onDismiss: () -> Unit, onPick: (AppRule) -> Unit) {
    NcDialog(app.label, t("What can it use?", "Network access"), onDismiss = onDismiss, dismiss = t("Done", "Close") to onDismiss) {
        listOf(
            AppRule.ALLOW to t("Everything", "Allow everything"),
            AppRule.NO_WIFI to t("Block on Wi-Fi", "Block on Wi-Fi"),
            AppRule.NO_DATA to t("Block on mobile data", "Block on mobile data"),
            AppRule.BLOCK to t("Block everything", "Block all"),
        ).forEach { (r, label) ->
            Choice(r == rule, label, ruleName(r)) { onPick(r) }
        }
    }
}

@Composable
private fun MyLists(server: ServerShield?, busy: Boolean, rule: (String, String, String) -> Unit) {
    var text by remember { mutableStateOf("") }
    var everywhere by remember { mutableStateOf(false) }
    val clean = cleanDomain(text)
    SectionCard(
        t("Your lists", "Your allow and block lists"),
        sub = t("Saved on your NoCap server, just for you", "Stored on your NoCap server, for you only"),
    ) {
        CardBlock {
            Row(
                Modifier.fillMaxWidth().heightIn(min = 46.dp).clip(RoundedCornerShape(23.dp)).background(NoCap.Field).padding(horizontal = 16.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Box(Modifier.weight(1f)) {
                    if (text.isEmpty()) Text("example.com", color = NoCap.Muted)
                    BasicTextField(
                        text, { text = it.take(260) }, singleLine = true, modifier = Modifier.fillMaxWidth(),
                        textStyle = MaterialTheme.typography.bodyLarge.copy(color = NoCap.Text), cursorBrush = SolidColor(NoCap.Red),
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Done),
                        keyboardActions = KeyboardActions(onDone = {}),
                    )
                }
            }
            SwitchRowInline(t("For all my devices", "Apply to all my devices"), everywhere) { everywhere = it }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                PillButton(t("Block", "Block"), { clean?.let { rule(it, "block", if (everywhere) "user" else "device"); text = "" } }, enabled = clean != null && !busy)
                PillButton(t("Allow", "Allow"), { clean?.let { rule(it, "allow", if (everywhere) "user" else "device"); text = "" } }, primary = false, enabled = clean != null && !busy)
            }
        }
        val s = server
        if (s != null) {
            Entries(t("Blocked on this phone", "Blocked on this phone"), s.device.block, "device", rule)
            Entries(t("Allowed on this phone", "Allowed on this phone"), s.device.allow, "device", rule)
            Entries(t("Blocked on all my devices", "Blocked on all my devices"), s.user.block, "user", rule)
            Entries(t("Allowed on all my devices", "Allowed on all my devices"), s.user.allow, "user", rule)
        }
    }
}

@Composable
private fun SwitchRowInline(title: String, on: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().clickable { onChange(!on) }.padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(title, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
        com.nocap.link.ui.NcSwitch(on, onChange = null)
    }
}

@Composable
private fun Entries(title: String, list: List<String>, scope: String, rule: (String, String, String) -> Unit) {
    if (list.isEmpty()) return
    Column {
        Text(title, Modifier.padding(start = 16.dp, top = 10.dp), style = MaterialTheme.typography.labelLarge, color = NoCap.Muted)
        list.take(200).forEach { d -> NavRow(d, value = t("Remove", "Remove"), chevron = false) { rule(d, "clear", scope) } }
    }
}

@Composable
private fun AboutCard(a: ShieldActivity) {
    SectionCard(
        t("How Shield works", "How Shield works"),
        footer = t(
            "Nothing leaves your phone except DNS lookups to your own NoCap server. No one else sees them, and NoCap collects nothing.",
            "Nothing leaves the phone except DNS lookups sent to your own NoCap server. No data is collected.",
        ),
    ) {
        CardBlock {
            Bullet(t("It uses Android's one VPN slot, so it can't run next to another VPN app.", "It uses Android's VPN slot, so it can't run at the same time as another VPN app."))
            Bullet(t("Android shows a key icon while it's on. That's Shield.", "Android shows a key icon in the status bar while Shield is on."))
            Bullet(t("Only DNS lookups (or blocked apps) go through it; everything else skips it, full speed.", "Only DNS lookups (or blocked apps) pass through it; all other traffic goes directly."))
            Bullet(t("If Private DNS is set to a provider name in Android's settings, set it to Automatic so Shield sees lookups.", "If Private DNS is set to a provider hostname in Android settings, set it to Automatic so Shield can filter."))
        }
        NavRow(
            t("Keep it on: Always-on VPN", "Always-on VPN"),
            sub = t("Android settings → VPN → NoCap OS ⚙ → Always-on VPN. It starts by itself after a reboot.", "Android Settings → Network → VPN → NoCap OS (gear) → Always-on VPN. Shield then starts automatically, including after a restart."),
            icon = "settings", iconColor = Color(0xFF8E8E93),
        ) { Shield.vpnSettings(a) }
    }
}

@Composable
private fun Bullet(text: String) {
    Row {
        Text("•", Modifier.width(14.dp), color = NoCap.Red)
        Text(text, style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
    }
}

private fun ago(at: Long): String = DateUtils.getRelativeTimeSpanString(at, System.currentTimeMillis(), DateUtils.MINUTE_IN_MILLIS).toString()

/** App icons for the firewall list, cached (bounded). */
private object AppIcons {
    private val cache = android.util.LruCache<String, ImageBitmap>(160)

    fun cached(pkg: String): ImageBitmap? = cache.get(pkg)

    fun load(ctx: Context, pkg: String): ImageBitmap? = runCatching {
        ctx.packageManager.getApplicationIcon(pkg).toBitmap(96, 96).asImageBitmap()
    }.getOrNull()?.also { cache.put(pkg, it) }
}
