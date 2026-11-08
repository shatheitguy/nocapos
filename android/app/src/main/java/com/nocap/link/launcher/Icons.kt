package com.nocap.link.launcher

import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.ColorMatrix
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Shadow
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.nocap.link.IconTheme
import com.nocap.link.PhoneApp
import com.nocap.link.Prefs
import com.nocap.link.system.DotsService
import com.nocap.link.ui.NoCap

private val mono = ColorFilter.colorMatrix(ColorMatrix().apply { setToSaturation(0f) })

// Grayscale, then pushed toward the NoCapOS red.
private val tinted = ColorFilter.colorMatrix(
    ColorMatrix(
        floatArrayOf(
            0.299f, 0.587f, 0.114f, 0f, 60f,
            0.070f, 0.140f, 0.030f, 0f, 8f,
            0.075f, 0.150f, 0.030f, 0f, 10f,
            0f, 0f, 0f, 1f, 0f,
        ),
    ),
)

/** An icon in the chosen theme: original, NoCap tinted, or monochrome. */
@Composable
fun ThemedIcon(icon: ImageBitmap, size: Dp, desc: String? = null) {
    when (Prefs.iconTheme) {
        IconTheme.ORIGINAL -> Image(icon, desc, Modifier.size(size))
        IconTheme.MONO -> Box(
            Modifier.size(size).clip(RoundedCornerShape(size * 0.28f)).background(Color(0xCC15171C)).border(1.dp, Color(0x33FFFFFF), RoundedCornerShape(size * 0.28f)),
            contentAlignment = Alignment.Center,
        ) { Image(icon, desc, Modifier.size(size * 0.74f), colorFilter = mono) }
        IconTheme.TINTED -> Box(
            Modifier.size(size).clip(RoundedCornerShape(size * 0.28f))
                .background(Brush.linearGradient(listOf(Color(0xFF1A0A0D), Color(0xFF0B0B10))))
                .border(1.dp, NoCap.Red.copy(alpha = 0.45f), RoundedCornerShape(size * 0.28f)),
            contentAlignment = Alignment.Center,
        ) { Image(icon, desc, Modifier.size(size * 0.74f), colorFilter = tinted) }
    }
}

/** Labels sit on the wallpaper, so they get a soft shadow. */
val labelShadow = Shadow(Color.Black.copy(alpha = 0.7f), Offset(0f, 1.5f), 6f)

@Composable
fun AppTile(app: PhoneApp, size: Dp = Prefs.iconSize.dp, label: Boolean = Prefs.labels, modifier: Modifier = Modifier, dot: Boolean = true) {
    Column(modifier.padding(vertical = 4.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Box {
            ThemedIcon(app.icon, size, app.label)
            val n = if (dot && Prefs.dots) DotsService.counts[app.pkg] ?: 0 else 0
            if (n > 0) {
                Box(
                    Modifier.align(Alignment.TopEnd).offset(x = 3.dp, y = (-3).dp).size(12.dp).clip(CircleShape)
                        .background(NoCap.Red).border(1.5.dp, Color(0xFF07080B), CircleShape),
                )
            }
        }
        if (label) {
            Text(
                app.label, Modifier.padding(top = 5.dp).widthIn(max = size + 26.dp),
                style = MaterialTheme.typography.labelSmall.copy(shadow = labelShadow), color = Color.White,
                maxLines = 1, overflow = TextOverflow.Ellipsis, textAlign = TextAlign.Center,
            )
        }
    }
}

/** A folder: a glass square with up to four little icons. */
@Composable
fun FolderTile(name: String, apps: List<PhoneApp>, size: Dp = Prefs.iconSize.dp, label: Boolean = Prefs.labels) {
    Column(Modifier.padding(vertical = 4.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Box(
            Modifier.size(size).clip(RoundedCornerShape(size * 0.28f)).background(Color(0x8C14161C))
                .border(1.dp, Color(0x33FFFFFF), RoundedCornerShape(size * 0.28f)).padding(size * 0.12f),
        ) {
            val mini = size * 0.34f
            apps.take(4).forEachIndexed { i, app ->
                Box(Modifier.offset(x = (size * 0.4f) * (i % 2), y = (size * 0.4f) * (i / 2))) { Image(app.icon, null, Modifier.size(mini)) }
            }
            val dots = apps.any { Prefs.dots && (DotsService.counts[it.pkg] ?: 0) > 0 }
            if (dots) Box(Modifier.align(Alignment.TopEnd).offset(x = 6.dp, y = (-6).dp).size(10.dp).clip(CircleShape).background(NoCap.Red))
        }
        if (label) {
            Text(
                name, Modifier.padding(top = 5.dp).widthIn(max = size + 26.dp),
                style = MaterialTheme.typography.labelSmall.copy(shadow = labelShadow), color = Color.White,
                maxLines = 1, overflow = TextOverflow.Ellipsis, textAlign = TextAlign.Center,
            )
        }
    }
}

/** The dock's Launchpad button: a 3×3 dot grid with a red center. */
@Composable
fun RosterGlyph(size: Dp) {
    Canvas(Modifier.size(size)) {
        val r = this.size.minDimension / 12f
        for (i in 0..2) for (j in 0..2) {
            drawCircle(if (i == 1 && j == 1) NoCap.Red else NoCap.Text, r, Offset(this.size.width * (i * 2 + 1) / 6f, this.size.height * (j * 2 + 1) / 6f))
        }
    }
}

/** Placeholder while an icon isn't known (app removed, still loading). */
@Composable
fun GhostIcon(size: Dp) {
    Box(Modifier.size(size).clip(RoundedCornerShape(size * 0.28f)).background(Color(0x33FFFFFF)).fillMaxSize())
}
