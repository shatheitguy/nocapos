package com.nocap.link.drawer

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.GenericShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.ColorMatrix
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Shadow
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.sp
import com.nocap.link.data.FolderStyle
import com.nocap.link.data.IconShape
import com.nocap.link.data.IconTheme
import com.nocap.link.data.LabelColor
import com.nocap.link.data.LabelFont
import com.nocap.link.data.Opt
import com.nocap.link.data.PhoneApp
import com.nocap.link.system.DotsService
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.Orbitron
import com.nocap.link.ui.Rajdhani
import kotlin.math.abs
import kotlin.math.cos
import kotlin.math.pow
import kotlin.math.sign
import kotlin.math.sin

private val monoFilter = ColorFilter.colorMatrix(ColorMatrix().apply { setToSaturation(0f) })

/** Grayscale, then pushed toward a color (the accent). */
private fun towards(c: Color, lift: Float = 0.18f): ColorFilter {
    val r = c.red; val g = c.green; val b = c.blue
    return ColorFilter.colorMatrix(
        ColorMatrix(
            floatArrayOf(
                0.299f * r * 1.4f, 0.587f * r * 1.4f, 0.114f * r * 1.4f, 0f, 255f * lift * r,
                0.299f * g * 1.4f, 0.587f * g * 1.4f, 0.114f * g * 1.4f, 0f, 255f * lift * g,
                0.299f * b * 1.4f, 0.587f * b * 1.4f, 0.114f * b * 1.4f, 0f, 255f * lift * b,
                0f, 0f, 0f, 1f, 0f,
            ),
        ),
    )
}

/** A superellipse, between a circle and a rounded square. */
val Squircle: Shape = GenericShape { size, _ ->
    val n = 4.0
    val a = size.width / 2f
    val b = size.height / 2f
    for (i in 0..96) {
        val th = 2 * Math.PI * i / 96
        val c = cos(th); val s = sin(th)
        val x = a + a * (abs(c).pow(2 / n) * sign(c)).toFloat()
        val y = b + b * (abs(s).pow(2 / n) * sign(s)).toFloat()
        if (i == 0) moveTo(x, y) else lineTo(x, y)
    }
    close()
}

/** The shape icons are drawn in (NoCap tiles default to the web desktop's rounded square). */
fun iconShape(size: Dp, s: IconShape = Opt.iconShape.v, nocapDefault: Boolean = false): Shape? = when (s) {
    IconShape.CIRCLE -> CircleShape
    IconShape.SQUIRCLE -> Squircle
    IconShape.ROUNDED -> RoundedCornerShape(size * 0.24f)
    IconShape.ORIGINAL -> if (nocapDefault) RoundedCornerShape(size * 0.234f) else null
}

/**
 * The NoCapOS tile every app icon sits on, like the web desktop's: dark
 * glass gradient, a soft shine at the top, a hairline edge and a faint
 * accent rim.
 */
fun Modifier.nocapTile(shape: Shape, accent: Color, light: Boolean): Modifier =
    this.shadow(6.dp, shape, ambientColor = accent.copy(alpha = 0.35f), spotColor = Color.Black.copy(alpha = 0.6f))
        .clip(shape)
        .background(Brush.verticalGradient(if (light) listOf(Color(0xFFFFFFFF), Color(0xFFE6E7EC)) else listOf(Color(0xFF3B3E49), Color(0xFF1A1C22))))
        .drawBehind {
            drawRect(Brush.verticalGradient(listOf(Color.White.copy(alpha = 0.2f), Color.Transparent), endY = size.height / 2))
            drawRect(Brush.radialGradient(listOf(accent.copy(alpha = 0.22f), Color.Transparent), center = Offset(size.width / 2, size.height * 1.05f), radius = size.width * 0.7f))
        }
        .border(1.dp, Brush.verticalGradient(listOf(Color.White.copy(alpha = if (light) 0.6f else 0.16f), accent.copy(alpha = 0.45f))), shape)

/** An app's icon in the style picked in Settings. */
@Composable
fun AppIcon(app: PhoneApp, size: Dp, desc: String? = null) {
    val theme = Opt.iconTheme.v
    val accent = NoCap.Red
    when (theme) {
        IconTheme.NOCAP -> {
            val shape = iconShape(size, nocapDefault = true)!!
            Box(Modifier.size(size).nocapTile(shape, accent, NoCap.light), contentAlignment = Alignment.Center) {
                val mono = app.mono
                when {
                    Opt.nocapGlyph.v && mono != null -> Image(mono, desc, Modifier.fillMaxSize(), colorFilter = ColorFilter.tint(if (NoCap.light) accent else Color.White))
                    Opt.nocapGlyph.v -> Image(app.icon, desc, Modifier.size(size * 0.66f), colorFilter = towards(if (NoCap.light) accent else Color.White, 0.1f))
                    app.fg != null -> Image(app.fg, desc, Modifier.fillMaxSize())
                    app.custom -> Image(app.icon, desc, Modifier.fillMaxSize())
                    else -> Image(app.icon, desc, Modifier.size(size * 0.72f))
                }
            }
        }
        IconTheme.ORIGINAL -> {
            val shape = iconShape(size)
            val full = app.full
            when {
                shape == null -> Image(app.icon, desc, Modifier.size(size))
                full != null -> Image(full, desc, Modifier.size(size).clip(shape))
                else -> Box(Modifier.size(size).clip(shape).background(Color(0xFFF4F4F6)), contentAlignment = Alignment.Center) {
                    Image(app.icon, desc, Modifier.size(size * 0.74f))
                }
            }
        }
        IconTheme.THEMED -> {
            val shape = iconShape(size) ?: CircleShape
            val bg = if (NoCap.light) Color(0xFFF1F2F5) else Color(0xFF1B1D23)
            Box(Modifier.size(size).clip(shape).background(bg).border(1.dp, accent.copy(alpha = 0.35f), shape), contentAlignment = Alignment.Center) {
                val mono = app.mono
                if (mono != null) Image(mono, desc, Modifier.fillMaxSize(), colorFilter = ColorFilter.tint(accent))
                else Image(app.icon, desc, Modifier.size(size * 0.7f), colorFilter = towards(accent))
            }
        }
        IconTheme.TINTED, IconTheme.MONO -> {
            val shape = iconShape(size) ?: RoundedCornerShape(size * 0.28f)
            val tinted = theme == IconTheme.TINTED
            Box(
                Modifier.size(size).clip(shape)
                    .background(if (tinted) Brush.linearGradient(listOf(accent.copy(alpha = 0.25f).compositeOver(Color(0xFF0B0B10)), Color(0xFF0B0B10))) else Brush.linearGradient(listOf(Color(0xCC15171C), Color(0xCC15171C))))
                    .border(1.dp, if (tinted) accent.copy(alpha = 0.45f) else Color(0x33FFFFFF), shape),
                contentAlignment = Alignment.Center,
            ) { Image(app.icon, desc, Modifier.size(size * 0.74f), colorFilter = if (tinted) towards(accent) else monoFilter) }
        }
    }
}

private fun Color.compositeOver(bg: Color): Color {
    val a = alpha
    return Color(red * a + bg.red * (1 - a), green * a + bg.green * (1 - a), blue * a + bg.blue * (1 - a), 1f)
}

/** For places that only have a bitmap (server apps, shortcuts). */
@Composable
fun ThemedIcon(icon: ImageBitmap, size: Dp, desc: String? = null) {
    Image(icon, desc, Modifier.size(size))
}

/** Labels sit on the wallpaper, so they get a soft shadow. */
val labelShadow = Shadow(Color.Black.copy(alpha = 0.7f), Offset(0f, 1.5f), 6f)

/** The label style picked in Settings (font, size, color, shadow). */
@Composable
fun labelStyle(onWallpaper: Boolean = true): TextStyle {
    val fam: FontFamily = when (Opt.labelFont.v) {
        LabelFont.NOCAP -> Rajdhani
        LabelFont.SYSTEM -> FontFamily.Default
        LabelFont.ORBITRON -> Orbitron
        LabelFont.SERIF -> FontFamily.Serif
        LabelFont.MONO -> FontFamily.Monospace
    }
    val size = Opt.labelSize.v.let { if (Opt.labelFont.v == LabelFont.ORBITRON) it - 2 else it }
    val color = if (!onWallpaper) NoCap.Text else when (Opt.labelColor.v) {
        LabelColor.WHITE -> Color.White
        LabelColor.ACCENT -> NoCap.Red
        LabelColor.SOFT -> Color.White.copy(alpha = 0.72f)
        LabelColor.DARK -> Color(0xFF15171C)
    }
    val shadow = if (onWallpaper && Opt.labelShadow.v && Opt.labelColor.v != LabelColor.DARK) labelShadow else null
    return MaterialTheme.typography.labelSmall.copy(fontFamily = fam, fontSize = size.sp, fontWeight = FontWeight.SemiBold, color = color, shadow = shadow)
}

@Composable
fun AppTile(
    app: PhoneApp, size: Dp = Opt.iconSize.v.dp, label: Boolean = Opt.labels.v, modifier: Modifier = Modifier,
    dot: Boolean = true, onWallpaper: Boolean = true, labelWidth: Dp = size + 26.dp,
) {
    Column(modifier.padding(vertical = 4.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Box {
            AppIcon(app, size, app.label)
            val n = if (dot && Opt.dots.v) DotsService.counts[app.pkg] ?: 0 else 0
            if (n > 0) {
                Box(
                    Modifier.align(Alignment.TopEnd).offset(x = 3.dp, y = (-3).dp).size(12.dp).clip(CircleShape)
                        .background(NoCap.Red).border(1.5.dp, Color(0xFF07080B), CircleShape),
                )
            }
        }
        if (label) {
            Text(
                app.label, Modifier.padding(top = 5.dp).widthIn(max = labelWidth),
                style = labelStyle(onWallpaper), maxLines = 1, overflow = TextOverflow.Ellipsis, textAlign = TextAlign.Center,
            )
        }
    }
}

/** A folder: a glass tile with up to four little icons (grid) or a fanned stack. */
@Composable
fun FolderTile(name: String, apps: List<PhoneApp>, size: Dp = Opt.iconSize.v.dp, label: Boolean = Opt.labels.v, onWallpaper: Boolean = true, labelWidth: Dp = size + 26.dp) {
    Column(Modifier.padding(vertical = 4.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        val shape = iconShape(size, nocapDefault = true) ?: RoundedCornerShape(size * 0.28f)
        if (Opt.folderStyle.v == FolderStyle.STACK) {
            Box(Modifier.size(size), contentAlignment = Alignment.Center) {
                val shown = apps.take(3).reversed()
                shown.forEachIndexed { i, app ->
                    val k = shown.size - 1 - i
                    Box(Modifier.offset(x = (size * 0.1f) * k, y = -(size * 0.06f) * k).rotate(-8f * k)) { AppIcon(app, size * 0.78f) }
                }
            }
        } else {
            Box(Modifier.size(size).nocapTile(shape, NoCap.Red, NoCap.light).padding(size * 0.12f)) {
                val mini = size * 0.34f
                apps.take(4).forEachIndexed { i, app ->
                    Box(Modifier.offset(x = (size * 0.4f) * (i % 2), y = (size * 0.4f) * (i / 2))) { Image(app.icon, null, Modifier.size(mini)) }
                }
            }
        }
        val dots = apps.any { Opt.dots.v && (DotsService.counts[it.pkg] ?: 0) > 0 }
        if (dots) Box(Modifier.offset(x = size / 2, y = -size).size(10.dp).clip(CircleShape).background(NoCap.Red))
        if (label) {
            Text(
                name, Modifier.padding(top = 5.dp).widthIn(max = labelWidth),
                style = labelStyle(onWallpaper), maxLines = 1, overflow = TextOverflow.Ellipsis, textAlign = TextAlign.Center,
            )
        }
    }
}

/** The dock's Launchpad button: a 3×3 dot grid with a red center. */
@Composable
fun RosterGlyph(size: Dp) {
    val red = NoCap.Red
    val txt = NoCap.Text
    Canvas(Modifier.size(size)) {
        val r = this.size.minDimension / 12f
        for (i in 0..2) for (j in 0..2) {
            drawCircle(if (i == 1 && j == 1) red else txt, r, Offset(this.size.width * (i * 2 + 1) / 6f, this.size.height * (j * 2 + 1) / 6f))
        }
    }
}

/** Placeholder while an icon isn't known (app removed, still loading). */
@Composable
fun GhostIcon(size: Dp) {
    Box(Modifier.size(size).clip(RoundedCornerShape(size * 0.24f)).background(Color(0x33FFFFFF)))
}
