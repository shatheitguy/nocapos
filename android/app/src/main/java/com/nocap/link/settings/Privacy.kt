package com.nocap.link.settings

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.Icons
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import com.nocap.link.data.Opt
import com.nocap.link.data.t
import com.nocap.link.MainActivity
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap

/** Every host NoCap OS can talk to, and why. Kept in one place so the page can't drift from the code. */
object Hosts {
    class Host(val name: String, val what: () -> String, val when_: () -> String)

    fun all(server: String?): List<Host> = listOf(
        Host(
            server ?: t("Your NoCap server (once linked)", "Your NoCap server (once linked)"),
            { t("Linking, NoCap Link, files, AI, backups, notifications you choose to sync, and crash reports you choose to send.", "Pairing, NoCap Link, files, AI, backups, notifications you choose to sync, and crash reports you choose to send.") },
            { t("Only after you link it.", "Only after you link a server.") },
        ),
        Host(
            "api.github.com · github.com · *.githubusercontent.com",
            { t("Checking for and downloading NoCap OS updates. \"Report to NoCap developers\" opens github.com in your browser.", "Checking for and downloading NoCap OS updates. \"Report to NoCap developers\" opens github.com in your browser.") },
            { t("When you check (or automatic checks are on), or tap report.", "When you check for updates (or automatic checks are on), or choose to report.") },
        ),
        Host(
            "api.open-meteo.com · geocoding-api.open-meteo.com",
            { t("Weather. Gets a rough spot (rounded to about 1 km) or the city name you search. Nothing else.", "Weather forecasts. Receives an approximate location (rounded to about 1 km) or the city you search for. Nothing else.") },
            { t("Only if weather is on.", "Only when weather is enabled.") },
        ),
    )
}

/** Settings → Privacy, and the link from the setup's privacy card. */
@Composable
fun PrivacyScreen(a: MainActivity, onClose: () -> Unit) {
    BackHandler(onBack = onClose)
    Column(
        Modifier.fillMaxSize().background(NoCap.Sheet).clickable(remember { MutableInteractionSource() }, null) {}
            .statusBarsPadding().navigationBarsPadding(),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Column(Modifier.widthIn(max = 680.dp).fillMaxWidth()) {
            Row(Modifier.fillMaxWidth().padding(start = 18.dp, end = 8.dp, top = 10.dp, bottom = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                Text(t("PRIVACY", "PRIVACY"), style = MaterialTheme.typography.headlineSmall, modifier = Modifier.weight(1f))
                IconButton(onClick = onClose) { Icon(Icons.Filled.Close, t("Close", "Close"), tint = NoCap.Text) }
            }
            Column(Modifier.verticalScroll(rememberScrollState()).padding(horizontal = 18.dp).padding(bottom = 28.dp)) {
                Text(
                    t("NoCap OS collects nothing. Your data only goes to your own server.", "NoCap OS collects nothing. Your data only goes to your own server."),
                    style = MaterialTheme.typography.titleLarge,
                )
                Spacer(Modifier.height(8.dp))
                Text(
                    t(
                        "No tracking, no analytics, no ads, no advertising ID, no crash reporting services. Crash and bug reports only leave the phone when you tap send, and you see exactly what's in them first.",
                        "No tracking, analytics, advertising, advertising ID or third-party crash reporting. Crash and bug reports leave the phone only when you choose to send them, after you've seen their content.",
                    ),
                    style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted,
                )
                Spacer(Modifier.height(18.dp))
                Label(t("Everything NoCap OS talks to", "Every host the app connects to"))
                Hosts.all(a.link?.server).forEach { h ->
                    Column(Modifier.fillMaxWidth().padding(vertical = 8.dp)) {
                        Text(h.name, style = MaterialTheme.typography.titleSmall)
                        Text(h.what(), style = MaterialTheme.typography.bodySmall)
                        Text(h.when_(), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
                    }
                }
                Spacer(Modifier.height(10.dp))
                Label(t("Weather", "Weather"))
                Toggle(
                    t("Weather from Open-Meteo", "Weather (Open-Meteo)"),
                    t("Off = no weather requests at all.", "When off, no weather requests are made."),
                    Opt.weatherOn.v,
                ) { Opt.weatherOn.set(it) }
                Spacer(Modifier.height(10.dp))
                Label(t("On this phone", "On this phone"))
                Text(
                    t(
                        "Your device key and certificate pin are encrypted with Android's keystore. Android's cloud backup is off for NoCap OS, so none of it ends up in someone else's cloud. Settings backups only go to your server or a file you pick.",
                        "The device key and certificate pin are encrypted with the Android Keystore. Android cloud backup and device transfer are disabled for this app. Settings backups go only to your server or to a file you choose.",
                    ),
                    Modifier.padding(top = 4.dp), style = MaterialTheme.typography.bodySmall,
                )
            }
        }
    }
}
