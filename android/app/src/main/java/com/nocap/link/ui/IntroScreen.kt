package com.nocap.link.ui

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import com.nocap.link.R

/**
 * What NoCap OS uses and why. It asks for exactly two things, and only when
 * you tap Allow: notifications and the camera (for the QR code).
 */
@Composable
fun IntroScreen(onDone: () -> Unit) {
    val ctx = LocalContext.current
    var tick by remember { mutableIntStateOf(0) } // re-read grants after a request
    val granted = { p: String -> tick.let { ContextCompat.checkSelfPermission(ctx, p) == PackageManager.PERMISSION_GRANTED } }
    val ask = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { tick++ }
    val notifNeeded = Build.VERSION.SDK_INT >= 33

    Wallpaper {
        Column(
            Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().verticalScroll(rememberScrollState()).padding(22.dp),
        ) {
            Spacer(Modifier.height(24.dp))
            Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(64.dp))
            Spacer(Modifier.height(16.dp))
            Text("NOCAP OS", style = MaterialTheme.typography.displaySmall)
            Text("Your phone, but make it NoCap.", style = MaterialTheme.typography.bodyLarge, color = NoCap.Muted)
            Spacer(Modifier.height(22.dp))
            Text("Here's everything this app uses. Nothing else, fr.", style = MaterialTheme.typography.titleSmall)
            Spacer(Modifier.height(12.dp))

            PermissionCard(
                title = "Notifications",
                why = "Pings you when this phone gets unlinked from your server, or when a download from your NoCap desktop lands.",
                done = !notifNeeded || granted(Manifest.permission.POST_NOTIFICATIONS),
                doneText = if (notifNeeded) "Allowed" else "Already on",
                onAllow = { if (notifNeeded) ask.launch(Manifest.permission.POST_NOTIFICATIONS) },
                onSettings = { openAppSettings(ctx) },
            )
            Spacer(Modifier.height(12.dp))
            PermissionCard(
                title = "Camera",
                why = "Only to scan the NoCap Link QR code from Settings → Devices. No photos, no recording, nothing uploaded.",
                done = granted(Manifest.permission.CAMERA),
                doneText = "Allowed",
                onAllow = { ask.launch(Manifest.permission.CAMERA) },
                onSettings = { openAppSettings(ctx) },
            )
            Spacer(Modifier.height(12.dp))
            GlassCard(Modifier.fillMaxWidth()) {
                Column {
                    Text("Network", style = MaterialTheme.typography.titleMedium)
                    Text("Talks only to the NoCapOS server you link. Your key stays encrypted on this phone.", style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
                    Text("No prompt needed", Modifier.padding(top = 8.dp), style = MaterialTheme.typography.labelMedium, color = NoCap.Ok)
                }
            }
            Spacer(Modifier.height(24.dp))
            Text("You can skip these and change them any time in Settings.", style = MaterialTheme.typography.bodySmall)
            Spacer(Modifier.height(12.dp))
            RedButton("Let's go", onDone, Modifier.fillMaxWidth())
        }
    }
}

@Composable
private fun PermissionCard(title: String, why: String, done: Boolean, doneText: String, onAllow: () -> Unit, onSettings: () -> Unit) {
    GlassCard(Modifier.fillMaxWidth()) {
        Column {
            Text(title, style = MaterialTheme.typography.titleMedium)
            Text(why, style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
            Spacer(Modifier.height(10.dp))
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                if (done) {
                    Text(doneText, style = MaterialTheme.typography.labelMedium, color = NoCap.Ok)
                } else {
                    RedButton("Allow", onAllow)
                    TextButton(onClick = onSettings) { Text("Open settings", color = NoCap.Muted) }
                }
            }
        }
    }
}

private fun openAppSettings(ctx: android.content.Context) {
    ctx.startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:${ctx.packageName}")).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
}
