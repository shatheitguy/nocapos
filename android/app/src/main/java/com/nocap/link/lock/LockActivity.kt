package com.nocap.link.lock

import android.app.KeyguardManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.Build
import android.os.Bundle
import android.text.format.DateFormat
import android.view.WindowManager
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shadow
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import com.nocap.link.control.Notifs
import com.nocap.link.control.ServerGlance
import com.nocap.link.data.Opt
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.home.WallpaperLayer
import com.nocap.link.R
import com.nocap.link.system.DotsService
import com.nocap.link.ui.glass
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.NoCapTheme
import com.nocap.link.ui.Orbitron
import com.nocap.link.widget.GlyphButton
import com.nocap.link.widget.Media
import java.util.Date
import kotlinx.coroutines.delay

/**
 * The NoCap lock screen (opt-in): clock and logo, server status, music
 * controls and notifications, shown *over* Android's own lock screen.
 * Swipe up asks Android to dismiss the keyguard, so your PIN, pattern or
 * fingerprint still protects the phone; nothing here unlocks anything.
 */
class LockActivity : ComponentActivity() {
    private var dismissing = false

    private val unlocked = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) { finish() }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        Prefs.init(this)
        if (Build.VERSION.SDK_INT >= 27) {
            setShowWhenLocked(true)
        } else {
            @Suppress("DEPRECATION")
            window.addFlags(WindowManager.LayoutParams.FLAG_SHOW_WHEN_LOCKED)
        }
        // Unlocked some other way (fingerprint on the system lock screen, Smart Lock): get out of the way.
        ContextCompat.registerReceiver(this, unlocked, IntentFilter(Intent.ACTION_USER_PRESENT), ContextCompat.RECEIVER_NOT_EXPORTED)
        Media.start(this)
        setContent { NoCapTheme { LockFace(this) } }
    }

    override fun onDestroy() {
        runCatching { unregisterReceiver(unlocked) }
        super.onDestroy()
    }

    override fun onStop() {
        super.onStop()
        // Screen off or something else in front: the next screen-on starts a fresh one.
        if (!dismissing && !isChangingConfigurations) finish()
    }

    /** Asks Android to unlock (it shows its own PIN / fingerprint prompt), then runs [then]. */
    fun unlock(then: (() -> Unit)? = null) {
        val km = getSystemService(KeyguardManager::class.java)
        if (km == null || !km.isKeyguardLocked) { then?.invoke(); finish(); return }
        dismissing = true
        km.requestDismissKeyguard(this, object : KeyguardManager.KeyguardDismissCallback() {
            override fun onDismissSucceeded() { dismissing = false; then?.invoke(); finish() }
            override fun onDismissCancelled() { dismissing = false }
            override fun onDismissError() { dismissing = false; finish() }
        })
    }
}

@Composable
private fun LockFace(a: LockActivity) {
    var now by remember { mutableLongStateOf(System.currentTimeMillis()) }
    LaunchedEffect(Unit) { while (true) { delay(1000L * (60 - (now / 1000 % 60)).coerceAtLeast(1)); now = System.currentTimeMillis() } }
    var pull by remember { mutableFloatStateOf(0f) }
    Box(
        Modifier.fillMaxSize().background(Color.Black).pointerInput(Unit) {
            detectVerticalDragGestures(onDragStart = { pull = 0f }, onDragEnd = { if (pull < -80.dp.toPx()) a.unlock() }) { c, dy -> c.consume(); pull += dy }
        },
    ) {
        WallpaperLayer()
        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(listOf(Color.Black.copy(alpha = 0.35f), Color.Transparent, Color.Black.copy(alpha = 0.6f)))))
        Column(
            Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().padding(horizontal = 20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            val line = remember(now) { ServerGlance.read(a) }
            Row(Modifier.padding(top = 10.dp).glass(RoundedCornerShape(50)).padding(horizontal = 12.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.size(8.dp).clip(CircleShape).background(when (line.ok) { true -> NoCap.Ok; false -> NoCap.Warn; null -> NoCap.Muted }))
                Text(line.text, Modifier.padding(start = 8.dp), style = MaterialTheme.typography.labelMedium, color = Color.White, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            Spacer(Modifier.size(28.dp))
            Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(54.dp))
            val date = Date(now)
            Text(
                DateFormat.getTimeFormat(a).format(date),
                style = MaterialTheme.typography.displayLarge.copy(
                    fontFamily = Orbitron, fontWeight = FontWeight.Black, fontSize = 76.sp, color = Color.White,
                    shadow = Shadow(NoCap.Red.copy(alpha = 0.8f), Offset(0f, 0f), 36f),
                ),
                maxLines = 1,
            )
            Text(DateFormat.format("EEEE, MMMM d", date).toString().uppercase(), style = MaterialTheme.typography.labelMedium, color = Color.White.copy(alpha = 0.85f))
            Spacer(Modifier.size(24.dp))
            Column(Modifier.widthIn(max = 520.dp).fillMaxWidth().weight(1f), verticalArrangement = Arrangement.spacedBy(10.dp)) {
                LockMedia()
                LockNotifications(a)
            }
            Text(
                "⌃\n" + t("Swipe up to unlock", "Swipe up to unlock"),
                Modifier.padding(bottom = 18.dp).clip(RoundedCornerShape(12.dp)).clickable { a.unlock() }.padding(8.dp),
                style = MaterialTheme.typography.labelLarge, color = Color.White.copy(alpha = 0.85f),
                textAlign = androidx.compose.ui.text.style.TextAlign.Center,
            )
        }
    }
}

@Composable
private fun LockMedia() {
    val n = Media.now ?: return
    val art = remember(n.art) { n.art?.asImageBitmap() }
    Row(Modifier.fillMaxWidth().glass(RoundedCornerShape(20.dp), strong = true).padding(10.dp), verticalAlignment = Alignment.CenterVertically) {
        Box(Modifier.size(48.dp).clip(RoundedCornerShape(12.dp)).background(NoCap.Red.copy(alpha = 0.4f)), contentAlignment = Alignment.Center) {
            if (art != null) Image(art, null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop) else Text("♪", color = Color.White)
        }
        Column(Modifier.weight(1f).padding(horizontal = 10.dp)) {
            Text(n.title.ifBlank { t("Something's playing", "Now playing") }, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(n.artist.ifBlank { n.app }, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        GlyphButton("⏮", null, size = 32.dp) { Media.prev() }
        GlyphButton(if (n.playing) "❚❚" else "▶", null, on = true, size = 40.dp) { Media.toggle() }
        GlyphButton("⏭", null, size = 32.dp) { Media.next() }
    }
}

/** Notifications: per-app counts, or (if you allow it in Settings) their text. Tapping one unlocks first. */
@Composable
private fun LockNotifications(a: LockActivity) {
    if (!DotsService.allowed(a)) return
    val changes = DotsService.changes
    val groups = remember(changes) { Notifs.group(Notifs.active(a).map(Notifs::item)) }
    val showText = Opt.lockNotifText.v
    groups.take(5).forEach { g ->
        val top = g.items.first()
        Column(
            Modifier.fillMaxWidth().glass(RoundedCornerShape(18.dp)).clickable { a.unlock { Notifs.open(a, top.key) } }.padding(horizontal = 14.dp, vertical = 10.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(Notifs.appName(a, g.pkg), Modifier.weight(1f), style = MaterialTheme.typography.labelMedium, color = NoCap.Red, maxLines = 1)
                Text(Notifs.countLine(g.items.size, Prefs.genz), style = MaterialTheme.typography.labelSmall, color = NoCap.Muted)
            }
            if (showText) {
                if (top.title.isNotBlank()) Text(top.title, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (top.text.isNotBlank()) Text(top.text, style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}
