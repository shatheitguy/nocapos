package com.nocap.link.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.Typography
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.sp
import com.nocap.link.data.AppFont
import com.nocap.link.data.Opt
import com.nocap.link.data.ThemeMode
import com.nocap.link.R

/**
 * NoCapOS desktop colors: near-black, dark glass, brand red. The accent and
 * light/dark come from Settings, so these are read as Compose state.
 */
object NoCap {
    const val BRAND = 0xFFFF2A2A

    /** Set by [NoCapTheme] from Settings (dark, light or follow the phone). */
    var light by mutableStateOf(false)
        internal set

    val Red: Color get() = parseAccent(Opt.accent.v)
    val RedDeep: Color get() = lerp(Red, Color.Black, 0.35f)
    val Bg: Color get() = if (light) Color(0xFFF2F3F6) else Color(0xFF07080B)
    val Bg2: Color get() = if (light) Color(0xFFFBEFF0) else Color(0xFF12060A)
    val Glass: Color get() = if (light) Color(0xB8FFFFFF) else Color(0x9914161C)
    val GlassStrong: Color get() = if (light) Color(0xEBFFFFFF) else Color(0xD9101217)
    val GlassEdge: Color get() = if (light) Color(0x1F000000) else Color(0x26FFFFFF)
    val Text: Color get() = if (light) Color(0xFF15171C) else Color(0xFFECEEF2)
    val Muted: Color get() = if (light) Color(0xFF5D6270) else Color(0xFF959AA5)
    /** Full-screen layers (drawer, settings, search). */
    val Sheet: Color get() = if (light) Color(0xF5F2F3F6) else Color(0xF207080B)
    /** Inputs, chips, segmented controls. */
    val Field: Color get() = if (light) Color(0x14000000) else Color(0x22FFFFFF)
    val Card: Color get() = if (light) Color(0xE6FFFFFF) else Color(0xCC15171C)
    val Ok = Color(0xFF2BD47A)
    val Warn = Color(0xFFFFB020)
    val Bad = Color(0xFFFF4D4D)

    val presets = listOf(
        0xFFFF2A2A, 0xFFFF7A1A, 0xFFFFC21A, 0xFF39E58C, 0xFF1AD1FF, 0xFF3D7BFF, 0xFF9B5CFF, 0xFFFF3DA8,
    )

    fun parseAccent(s: String): Color = runCatching { Color(android.graphics.Color.parseColor(s)) }.getOrDefault(Color(BRAND))

    fun hex(c: Color): String = "#%06X".format(0xFFFFFF and android.graphics.Color.argb(255, (c.red * 255).toInt(), (c.green * 255).toInt(), (c.blue * 255).toInt()))
}

val Orbitron = FontFamily(
    Font(R.font.orbitron_bold, FontWeight.Bold),
    Font(R.font.orbitron_black, FontWeight.Black),
)

val Rajdhani = FontFamily(
    Font(R.font.rajdhani_medium, FontWeight.Normal),
    Font(R.font.rajdhani_medium, FontWeight.Medium),
    Font(R.font.rajdhani_semibold, FontWeight.SemiBold),
    Font(R.font.rajdhani_bold, FontWeight.Bold),
)

/** Body text family for the font picked in Settings. */
fun bodyFamily(f: AppFont = Opt.font.v): FontFamily = when (f) {
    AppFont.NOCAP -> Rajdhani
    AppFont.SYSTEM -> FontFamily.Default
    AppFont.SERIF -> FontFamily.Serif
    AppFont.MONO -> FontFamily.Monospace
}

/** Titles keep Orbitron with the NoCap font; otherwise they follow the body font. */
fun titleFamily(f: AppFont = Opt.font.v): FontFamily = if (f == AppFont.NOCAP) Orbitron else bodyFamily(f)

private fun typography(font: AppFont, text: Color, muted: Color): Typography {
    val body = bodyFamily(font)
    val title = titleFamily(font)
    // Rajdhani runs small; other families get a slightly smaller size.
    val k = if (font == AppFont.NOCAP) 1f else 0.9f
    val base = TextStyle(fontFamily = body, color = text)
    return Typography(
        displayLarge = TextStyle(fontFamily = title, fontWeight = FontWeight.Bold, fontSize = 64.sp, letterSpacing = 2.sp, color = text),
        displaySmall = TextStyle(fontFamily = title, fontWeight = FontWeight.Bold, fontSize = 26.sp, letterSpacing = 1.5.sp, color = text),
        headlineSmall = TextStyle(fontFamily = title, fontWeight = FontWeight.Bold, fontSize = 18.sp, letterSpacing = 1.sp, color = text),
        titleMedium = base.copy(fontWeight = FontWeight.Bold, fontSize = (19 * k).sp),
        titleSmall = base.copy(fontWeight = FontWeight.SemiBold, fontSize = (16 * k).sp),
        bodyLarge = base.copy(fontSize = (18 * k).sp),
        bodyMedium = base.copy(fontSize = (16 * k).sp),
        bodySmall = base.copy(fontSize = (14 * k).sp, color = muted),
        labelLarge = base.copy(fontWeight = FontWeight.Bold, fontSize = (16 * k).sp, letterSpacing = 0.5.sp),
        labelMedium = base.copy(fontWeight = FontWeight.SemiBold, fontSize = (13 * k).sp, letterSpacing = 1.2.sp),
        labelSmall = base.copy(fontWeight = FontWeight.SemiBold, fontSize = (12 * k).sp),
    )
}

@Composable
fun NoCapTheme(content: @Composable () -> Unit) {
    val sysDark = isSystemInDarkTheme()
    val light = when (Opt.theme.v) {
        ThemeMode.DARK -> false
        ThemeMode.LIGHT -> true
        ThemeMode.AUTO -> !sysDark
    }
    SideEffect { NoCap.light = light }
    NoCap.light = light
    val accent = NoCap.Red
    val font = Opt.font.v
    val type = remember(font, light) { typography(font, NoCap.Text, NoCap.Muted) }
    val scheme = if (light) {
        lightColorScheme(
            primary = accent, onPrimary = Color.White, secondary = accent, background = NoCap.Bg, onBackground = NoCap.Text,
            surface = Color(0xFFFFFFFF), onSurface = NoCap.Text, surfaceVariant = Color(0xFFE9EAEE),
            onSurfaceVariant = NoCap.Muted, outline = NoCap.GlassEdge, error = NoCap.Bad,
        )
    } else {
        darkColorScheme(
            primary = accent, onPrimary = Color.White, secondary = accent, background = NoCap.Bg, onBackground = NoCap.Text,
            surface = Color(0xFF15171C), onSurface = NoCap.Text, surfaceVariant = Color(0xFF1B1D23),
            onSurfaceVariant = NoCap.Muted, outline = NoCap.GlassEdge, error = NoCap.Bad,
        )
    }
    MaterialTheme(colorScheme = scheme, typography = type, content = content)
}

/** The desktop's wallpaper: black with a red glow and a faint grid. */
@Composable
fun Wallpaper(modifier: Modifier = Modifier, content: @Composable BoxScope.() -> Unit) {
    val red = NoCap.Red
    val deep = NoCap.RedDeep
    val light = NoCap.light
    Box(modifier.fillMaxSize().background(Brush.verticalGradient(listOf(NoCap.Bg2, NoCap.Bg, NoCap.Bg)))) {
        Canvas(Modifier.fillMaxSize()) {
            drawRect(Brush.radialGradient(listOf(red.copy(alpha = 0.22f), Color.Transparent), center = Offset(size.width * 0.85f, 0f), radius = size.width * 0.9f))
            drawRect(Brush.radialGradient(listOf(deep.copy(alpha = 0.14f), Color.Transparent), center = Offset(0f, size.height), radius = size.width))
            val step = 36.dp.toPx()
            val line = (if (light) Color.Black else Color.White).copy(alpha = 0.03f)
            var x = 0f
            while (x < size.width) { drawLine(line, Offset(x, 0f), Offset(x, size.height)); x += step }
            var y = 0f
            while (y < size.height) { drawLine(line, Offset(0f, y), Offset(size.width, y)); y += step }
        }
        content()
    }
}

/** A glass panel like the desktop's windows and widgets. */
fun Modifier.glass(shape: Shape = RoundedCornerShape(20.dp), strong: Boolean = false, edge: Color = NoCap.GlassEdge): Modifier =
    this.clip(shape)
        .background(if (strong) NoCap.GlassStrong else NoCap.Glass)
        .background(Brush.verticalGradient(listOf(Color.White.copy(alpha = if (NoCap.light) 0.25f else 0.06f), Color.Transparent)))
        .border(BorderStroke(1.dp, edge), shape)

@Composable
fun GlassCard(modifier: Modifier = Modifier, padding: Dp = 18.dp, content: @Composable BoxScope.() -> Unit) {
    Box(modifier.glass().padding(padding), content = content)
}

@Composable
fun RedButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true, icon: (@Composable () -> Unit)? = null) {
    Button(
        onClick = onClick, enabled = enabled, modifier = modifier.height(50.dp),
        shape = RoundedCornerShape(14.dp),
        colors = ButtonDefaults.buttonColors(containerColor = NoCap.Red, contentColor = Color.White, disabledContainerColor = NoCap.Red.copy(alpha = 0.35f)),
        contentPadding = PaddingValues(horizontal = 20.dp),
    ) {
        if (icon != null) { icon(); Box(Modifier.padding(start = 8.dp)) }
        Text(text, style = MaterialTheme.typography.labelLarge, color = Color.White)
    }
}

@Composable
fun GhostButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true) {
    OutlinedButton(
        onClick = onClick, enabled = enabled, modifier = modifier.height(50.dp),
        shape = RoundedCornerShape(14.dp), border = BorderStroke(1.dp, NoCap.GlassEdge),
        colors = ButtonDefaults.outlinedButtonColors(contentColor = NoCap.Text),
    ) { Text(text, style = MaterialTheme.typography.labelLarge) }
}

/** Small uppercase section label, like the desktop's widget titles. */
@Composable
fun Label(text: String, modifier: Modifier = Modifier, color: Color = NoCap.Muted) {
    Text(text.uppercase(), modifier = modifier, style = MaterialTheme.typography.labelMedium, color = color)
}

@Composable
fun Chip(text: String, color: Color, modifier: Modifier = Modifier) {
    Row(
        modifier.clip(RoundedCornerShape(50)).background(color.copy(alpha = 0.14f)).padding(horizontal = 10.dp, vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(8.dp).clip(CircleShape).background(color))
        Text(text, Modifier.padding(start = 6.dp), style = MaterialTheme.typography.labelSmall, color = color, maxLines = 1)
    }
}

@Composable
fun RowScope.Spacer1() = Box(Modifier.weight(1f))

fun Modifier.tap(onClick: () -> Unit) = this.clickable(onClick = onClick)
