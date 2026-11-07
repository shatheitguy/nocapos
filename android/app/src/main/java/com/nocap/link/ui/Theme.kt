package com.nocap.link.ui

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.nocap.link.R

/** NoCapOS desktop colors: near-black, dark glass, brand red. */
object NoCap {
    val Red = Color(0xFFFF2A2A)
    val RedDeep = Color(0xFFB3141B)
    val Bg = Color(0xFF07080B)
    val Bg2 = Color(0xFF12060A)
    val Glass = Color(0x9914161C)
    val GlassStrong = Color(0xD9101217)
    val GlassEdge = Color(0x26FFFFFF)
    val Text = Color(0xFFECEEF2)
    val Muted = Color(0xFF959AA5)
    val Ok = Color(0xFF2BD47A)
    val Warn = Color(0xFFFFB020)
    val Bad = Color(0xFFFF4D4D)
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

private val base = TextStyle(fontFamily = Rajdhani, color = NoCap.Text)

private val type = Typography(
    displayLarge = TextStyle(fontFamily = Orbitron, fontWeight = FontWeight.Bold, fontSize = 64.sp, letterSpacing = 2.sp),
    displaySmall = TextStyle(fontFamily = Orbitron, fontWeight = FontWeight.Bold, fontSize = 26.sp, letterSpacing = 1.5.sp),
    headlineSmall = TextStyle(fontFamily = Orbitron, fontWeight = FontWeight.Bold, fontSize = 18.sp, letterSpacing = 1.sp),
    titleMedium = base.copy(fontWeight = FontWeight.Bold, fontSize = 19.sp),
    titleSmall = base.copy(fontWeight = FontWeight.SemiBold, fontSize = 16.sp),
    bodyLarge = base.copy(fontSize = 18.sp),
    bodyMedium = base.copy(fontSize = 16.sp),
    bodySmall = base.copy(fontSize = 14.sp, color = NoCap.Muted),
    labelLarge = base.copy(fontWeight = FontWeight.Bold, fontSize = 16.sp, letterSpacing = 0.5.sp),
    labelMedium = base.copy(fontWeight = FontWeight.SemiBold, fontSize = 13.sp, letterSpacing = 1.2.sp),
    labelSmall = base.copy(fontWeight = FontWeight.SemiBold, fontSize = 12.sp),
)

@Composable
fun NoCapTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = darkColorScheme(
            primary = NoCap.Red, onPrimary = Color.White,
            secondary = NoCap.Red, background = NoCap.Bg, onBackground = NoCap.Text,
            surface = Color(0xFF15171C), onSurface = NoCap.Text, surfaceVariant = Color(0xFF1B1D23),
            onSurfaceVariant = NoCap.Muted, outline = NoCap.GlassEdge, error = NoCap.Bad,
        ),
        typography = type,
        content = content,
    )
}

/** The desktop's wallpaper: black with a red glow and a faint grid. */
@Composable
fun Wallpaper(modifier: Modifier = Modifier, content: @Composable BoxScope.() -> Unit) {
    Box(modifier.fillMaxSize().background(Brush.verticalGradient(listOf(NoCap.Bg2, NoCap.Bg, NoCap.Bg)))) {
        Canvas(Modifier.fillMaxSize()) {
            drawRect(Brush.radialGradient(listOf(NoCap.Red.copy(alpha = 0.22f), Color.Transparent), center = Offset(size.width * 0.85f, 0f), radius = size.width * 0.9f))
            drawRect(Brush.radialGradient(listOf(NoCap.RedDeep.copy(alpha = 0.14f), Color.Transparent), center = Offset(0f, size.height), radius = size.width))
            val step = 36.dp.toPx()
            val line = Color.White.copy(alpha = 0.025f)
            var x = 0f
            while (x < size.width) { drawLine(line, Offset(x, 0f), Offset(x, size.height)); x += step }
            var y = 0f
            while (y < size.height) { drawLine(line, Offset(0f, y), Offset(size.width, y)); y += step }
        }
        content()
    }
}

/** A dark glass panel like the desktop's windows and widgets. */
fun Modifier.glass(shape: Shape = RoundedCornerShape(20.dp), strong: Boolean = false, edge: Color = NoCap.GlassEdge): Modifier =
    this.clip(shape)
        .background(if (strong) NoCap.GlassStrong else NoCap.Glass)
        .background(Brush.verticalGradient(listOf(Color.White.copy(alpha = 0.06f), Color.Transparent)))
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
        Text(text, style = MaterialTheme.typography.labelLarge)
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
        Text(text, Modifier.padding(start = 6.dp), style = MaterialTheme.typography.labelSmall, color = color)
    }
}

@Composable
fun RowScope.Spacer1() = Box(Modifier.weight(1f))

fun Modifier.tap(onClick: () -> Unit) = this.clickable(onClick = onClick)
