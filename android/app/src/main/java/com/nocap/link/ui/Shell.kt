package com.nocap.link.ui

import android.os.Build
import android.provider.Settings
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

/*
 * The NoCapOS web desktop's shell pieces (glass theme), for the phone's own
 * home screen: the Shelf's glass pills, the launcher's search pill and page
 * dots, context menus, Control Center tiles and notification cards. Values
 * follow frontend/src/glass.css and styles.css.
 *
 * Phones can't blur what's behind a panel cheaply, so the glass here is a
 * little more opaque than the web's (it stays readable on any wallpaper and
 * costs nothing to draw).
 */

/** Glass levels of the web OS, from light to solid. */
enum class GlassLevel { WIDGET, SHELF, SHEET, MENU }

object Shell {
    /** The panel tint for a glass level (web value, nudged up because there's no backdrop blur). */
    fun tint(level: GlassLevel): Color = if (NoCap.light) {
        when (level) {
            GlassLevel.WIDGET -> Color(0xB8FFFFFF)
            GlassLevel.SHELF -> Color(0xC7FFFFFF)
            GlassLevel.SHEET -> Color(0xE6F4F5F7)
            GlassLevel.MENU -> Color(0xF2FAFAFB)
        }
    } else {
        when (level) {
            GlassLevel.WIDGET -> Color(0x801C1D22) // rgba(28,29,34,.4) on the web
            GlassLevel.SHELF -> Color(0x941C1D22) // rgba(28,29,34,.45)
            GlassLevel.SHEET -> Color(0xD9121318) // rgba(18,19,24,.66)
            GlassLevel.MENU -> Color(0xEB1E1F25) // rgba(30,31,37,.82)
        }
    }

    /** The hairline edge (rgba(255,255,255,.12) on the web). */
    val edge: Color get() = if (NoCap.light) Color(0x1F000000) else Color(0x1FFFFFFF)

    /** The faint inner highlight along the top edge. */
    val shine: Color get() = if (NoCap.light) Color(0x66FFFFFF) else Color(0x14FFFFFF)

    /** Hover / pressed rows (rgba(255,255,255,.1)). */
    val hover: Color get() = if (NoCap.light) Color(0x12000000) else Color(0x1AFFFFFF)

    /** Ink on the wallpaper (labels, the launcher's text): white, like the web. */
    val onWall = Color.White
}

/**
 * A web-OS glass panel: tint, a hairline edge and the soft top highlight
 * (the web's `inset 0 1px 0 rgba(255,255,255,.08)`).
 */
fun Modifier.shellGlass(shape: Shape, level: GlassLevel = GlassLevel.SHELF, edge: Color? = null, tint: Color? = null): Modifier =
    this.clip(shape)
        .background(tint ?: Shell.tint(level))
        .drawBehind {
            drawRect(Brush.verticalGradient(listOf(Shell.shine, Color.Transparent), endY = 2.dp.toPx().coerceAtLeast(1f) * 6))
        }
        .border(1.dp, edge ?: Shell.edge, shape)

/** True when the phone can blur while drawing (Android 12+) and animations aren't turned off. */
@Composable
fun canBlur(): Boolean = Build.VERSION.SDK_INT >= 31 && !reducedMotion()

/** "Remove animations" in Android's accessibility settings (animator scale 0). */
@Composable
fun reducedMotion(): Boolean {
    val ctx = LocalContext.current
    return remember(ctx) {
        runCatching { Settings.Global.getFloat(ctx.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) == 0f }.getOrDefault(false)
    }
}

/**
 * The roster button's art from the web OS: a dark
 * glass tile with a 3×3 grid of colored rounded squares.
 */
@Composable
fun RosterArt(size: Dp, modifier: Modifier = Modifier) {
    val colors = remember {
        val a = Color(0xFFEF4444) to Color(0xFFFF8A8A)
        val b = Color(0xFFF59E0B) to Color(0xFFFFD36B)
        val c = Color(0xFF16A34A) to Color(0xFF86EFAC)
        val d = Color(0xFF0284C7) to Color(0xFF7DD3FC)
        val e = Color(0xFF7C3AED) to Color(0xFFC4B5FD)
        val f = Color(0xFFDB2777) to Color(0xFFF9A8D4)
        listOf(a, b, c, d, e, f, c, d, a)
    }
    Canvas(modifier.size(size)) {
        val k = this.size.width / 64f
        val r = CornerRadius(15f * k)
        drawRoundRect(Brush.verticalGradient(listOf(Color(0xFF3B3E49), Color(0xFF1A1C22))), Offset(k, k), Size(62 * k, 62 * k), r)
        drawRoundRect(Brush.verticalGradient(listOf(Color.White.copy(alpha = 0.2f), Color.Transparent), startY = k, endY = 32 * k), Offset(k, k), Size(62 * k, 62 * k), r)
        drawRoundRect(Color.White.copy(alpha = 0.14f), Offset(1.5f * k, 1.5f * k), Size(61 * k, 61 * k), CornerRadius(14.5f * k), style = androidx.compose.ui.graphics.drawscope.Stroke(k))
        colors.forEachIndexed { i, (lo, hi) ->
            val x = (13 + (i % 3) * 14) * k
            val y = (13 + (i / 3) * 14) * k
            drawRoundRect(Brush.verticalGradient(listOf(hi, lo), startY = y, endY = y + 10.5f * k), Offset(x, y), Size(10.5f * k, 10.5f * k), CornerRadius(3 * k))
            drawRoundRect(Color.White.copy(alpha = 0.3f), Offset(x, y), Size(10.5f * k, 3.5f * k), CornerRadius(1.75f * k))
        }
    }
}

/** The thin divider between dock groups (.dock-sep). */
@Composable
fun ShelfSep(height: Dp) {
    Box(Modifier.padding(horizontal = 2.dp).width(1.dp).height(height).background(Shell.edge))
}

/** The launcher's "Search the roster" pill (.launcher-panel > .search). */
@Composable
fun RosterSearch(value: String, onChange: (String) -> Unit, hint: String, modifier: Modifier = Modifier) {
    Row(
        modifier.widthIn(max = 560.dp).fillMaxWidth().height(48.dp)
            .shellGlass(RoundedCornerShape(50), GlassLevel.SHEET)
            .padding(horizontal = 16.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        NcIcon("search", NoCap.Muted, 18.dp)
        Box(Modifier.weight(1f).padding(horizontal = 10.dp), contentAlignment = Alignment.CenterStart) {
            if (value.isEmpty()) Text(hint, style = MaterialTheme.typography.bodyLarge.copy(fontSize = 16.sp), color = NoCap.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
            BasicTextField(
                value, onChange, singleLine = true, modifier = Modifier.fillMaxWidth(),
                textStyle = MaterialTheme.typography.bodyLarge.copy(fontSize = 16.sp, color = NoCap.Text), cursorBrush = SolidColor(NoCap.Red),
            )
        }
        if (value.isNotEmpty()) {
            Box(Modifier.size(28.dp).clip(CircleShape).clickable { onChange("") }, contentAlignment = Alignment.Center) {
                NcIcon("close", NoCap.Muted, 16.dp, desc = "Clear")
            }
        }
    }
}

/** The launcher's page dots: white, 8 dp, with a 22 dp touch target each. */
@Composable
fun ShellDots(count: Int, current: Int, modifier: Modifier = Modifier, onPick: ((Int) -> Unit)? = null) {
    Row(modifier.fillMaxWidth(), horizontalArrangement = Arrangement.Center, verticalAlignment = Alignment.CenterVertically) {
        repeat(count) { i ->
            val on = i == current
            Box(
                Modifier.size(22.dp).clip(CircleShape).then(if (onPick != null) Modifier.clickable { onPick(i) } else Modifier),
                contentAlignment = Alignment.Center,
            ) {
                Box(Modifier.size(if (on) 9.dp else 8.dp).clip(CircleShape).background(if (on) Color.White else Color.White.copy(alpha = 0.38f)))
            }
        }
    }
}

/** One context-menu item (the web's MenuItem). */
data class MenuEntry(val label: String, val icon: String? = null, val danger: Boolean = false, val onClick: () -> Unit)

/** The web context menu's card: frosted, 14 dp corners, 6 dp padding. */
@Composable
fun ShellMenuCard(modifier: Modifier = Modifier, content: @Composable ColumnScope.() -> Unit) {
    Column(
        modifier.widthIn(min = 210.dp, max = 300.dp).shellGlass(RoundedCornerShape(14.dp), GlassLevel.MENU).padding(6.dp),
        content = content,
    )
}

/** A context-menu row: icon, label; red when it removes something. */
@Composable
fun ShellMenuRow(e: MenuEntry) {
    val ink = if (e.danger) NoCap.Bad else NoCap.Text
    Row(
        Modifier.fillMaxWidth().heightIn(min = 42.dp).clip(RoundedCornerShape(8.dp)).clickable(onClick = e.onClick).padding(horizontal = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (e.icon != null) { NcIcon(e.icon, if (e.danger) NoCap.Bad else NoCap.Muted, 17.dp); Spacer(Modifier.width(10.dp)) }
        Text(e.label, style = MaterialTheme.typography.bodyMedium.copy(fontSize = 15.sp), color = ink, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

/** A hairline between menu groups. */
@Composable
fun ShellMenuSep() {
    Box(Modifier.fillMaxWidth().padding(vertical = 4.dp, horizontal = 6.dp).height(1.dp).background(Shell.edge))
}

/** A small round frosted icon button (.ghost.icon-btn). */
@Composable
fun ShellIconButton(icon: String, desc: String?, modifier: Modifier = Modifier, size: Dp = 36.dp, tint: Color = NoCap.Text, on: Boolean = false, onClick: () -> Unit) {
    Box(
        modifier.size(size).clip(CircleShape).background(if (on) NoCap.Red else Shell.hover).clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) { NcIcon(icon, if (on) Color.White else tint, size * 0.5f, desc = desc) }
}

/** The user's initial in an accent circle (.avatar). */
@Composable
fun ShellAvatar(name: String, size: Dp = 42.dp) {
    val red = NoCap.Red
    Box(
        Modifier.size(size).clip(CircleShape).background(Brush.linearGradient(listOf(red, androidx.compose.ui.graphics.lerp(red, Color.Black, 0.45f)))),
        contentAlignment = Alignment.Center,
    ) {
        Text(name.trim().take(1).uppercase().ifBlank { "N" }, style = MaterialTheme.typography.titleMedium.copy(fontSize = (size.value * 0.42f).sp), color = Color.White)
    }
}
