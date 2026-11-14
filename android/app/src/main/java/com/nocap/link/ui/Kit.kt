package com.nocap.link.ui

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.selection.toggleable
import androidx.compose.ui.semantics.Role
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithContent
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.layout.Layout
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

/*
 * The NoCap OS component kit: the same pieces as the NoCapOS web desktop
 * (glass sheets, grouped cards with hairline rows, colored icon tiles, pill
 * buttons, the NoCap red accent), so every screen the app draws looks like
 * the web OS. Screens use these instead of stock Material widgets.
 */

/** Web OS measurements (glass theme). */
object NcTokens {
    val Radius = 22.dp
    val RadiusSm = 11.dp
    val CardRadius = 18.dp
    val RowMin = 52.dp
    val RowPadH = 16.dp
    val TileRadiusRatio = 0.28f
}

/** A colored rounded-square icon, like the web Settings categories. */
@Composable
fun IconTile(icon: String, color: Color, size: Dp = 30.dp, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(size * NcTokens.TileRadiusRatio)
    Box(
        modifier.size(size).clip(shape)
            .background(Brush.verticalGradient(listOf(lerp(color, Color.White, 0.12f), lerp(color, Color.Black, 0.12f))))
            .border(0.5.dp, Color.White.copy(alpha = 0.18f), shape),
        contentAlignment = Alignment.Center,
    ) { NcIcon(icon, Color.White, size * 0.6f) }
}

/**
 * Children stacked with a hairline between each (inset like the web's
 * grouped rows); children that draw nothing get no line.
 */
@Composable
fun DividedColumn(modifier: Modifier = Modifier, inset: Dp = NcTokens.RowPadH, content: @Composable () -> Unit) {
    val lines = remember { FloatArray(64) }
    val count = remember { IntArray(1) }
    val edge = NoCap.GlassEdge
    Layout(
        content,
        modifier.drawWithContent {
            drawContent()
            val x = inset.toPx()
            val stroke = 1.dp.toPx().coerceAtMost(1.5f)
            for (i in 0 until count[0]) drawLine(edge, Offset(x, lines[i]), Offset(size.width, lines[i]), stroke)
        },
    ) { ms, c ->
        val ps = ms.map { it.measure(c.copy(minHeight = 0, minWidth = 0)) }
        val h = ps.sumOf { it.height }
        layout(c.maxWidth, h.coerceIn(c.minHeight, c.maxHeight)) {
            var y = 0
            var n = 0
            ps.forEach { p ->
                if (p.height > 0) {
                    if (y > 0 && n < lines.size) lines[n++] = y.toFloat()
                    p.place(0, y)
                    y += p.height
                }
            }
            count[0] = n
        }
    }
}

/** A grouped card (the web's settings groups): optional header above, rows inside, footer below. */
@Composable
fun SectionCard(
    header: String? = null,
    modifier: Modifier = Modifier,
    sub: String? = null,
    footer: String? = null,
    badge: String? = null,
    content: @Composable () -> Unit,
) {
    Column(modifier.fillMaxWidth()) {
        if (header != null) {
            Row(Modifier.fillMaxWidth().padding(start = 6.dp, end = 6.dp, bottom = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(header, style = MaterialTheme.typography.titleSmall.copy(fontWeight = FontWeight.Bold), color = NoCap.Text)
                    if (!sub.isNullOrBlank()) Text(sub, style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
                }
                if (badge != null) Chip(badge, NoCap.Red)
            }
        }
        DividedColumn(
            Modifier.fillMaxWidth().clip(RoundedCornerShape(NcTokens.CardRadius)).background(NoCap.Card)
                .border(1.dp, NoCap.GlassEdge, RoundedCornerShape(NcTokens.CardRadius)),
        ) { content() }
        if (!footer.isNullOrBlank()) Text(footer, Modifier.padding(start = 8.dp, end = 8.dp, top = 6.dp), style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
    }
}

/** A row that opens something: icon tile, title, optional subtitle and value, chevron. */
@Composable
fun NavRow(
    title: String,
    modifier: Modifier = Modifier,
    sub: String? = null,
    icon: String? = null,
    iconColor: Color = NoCap.Red,
    value: String? = null,
    badge: Boolean = false,
    danger: Boolean = false,
    chevron: Boolean = !danger,
    selected: Boolean = false,
    onClick: () -> Unit,
) {
    Row(
        modifier.fillMaxWidth().heightIn(min = NcTokens.RowMin)
            .background(if (selected) NoCap.Red.copy(alpha = 0.14f) else Color.Transparent)
            .clickable(onClick = onClick).padding(horizontal = NcTokens.RowPadH, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (icon != null) { IconTile(icon, iconColor, 30.dp); Spacer(Modifier.width(12.dp)) }
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.titleSmall, color = if (danger) NoCap.Bad else NoCap.Text, maxLines = 2, overflow = TextOverflow.Ellipsis)
            if (!sub.isNullOrBlank()) Text(sub, style = MaterialTheme.typography.bodySmall, color = NoCap.Muted, maxLines = 3, overflow = TextOverflow.Ellipsis)
        }
        if (value != null) Text(value, Modifier.padding(start = 8.dp).widthIn(max = 160.dp), style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
        if (badge) Box(Modifier.padding(start = 8.dp).size(8.dp).clip(CircleShape).background(NoCap.Red))
        if (chevron) NcIcon("chevronRight", NoCap.Muted, 18.dp, modifier = Modifier.padding(start = 6.dp))
    }
}

/** The web OS switch: a pill track in the accent when on. */
@Composable
fun NcSwitch(on: Boolean, modifier: Modifier = Modifier, enabled: Boolean = true, onChange: ((Boolean) -> Unit)?) {
    val k by animateFloatAsState(if (on) 1f else 0f, label = "switch")
    val track = lerp(if (NoCap.light) Color(0x29000000) else Color(0x29FFFFFF), NoCap.Red, k)
    Box(
        modifier.size(46.dp, 28.dp).clip(RoundedCornerShape(50)).background(track.copy(alpha = if (enabled) track.alpha else track.alpha * 0.5f))
            .then(if (onChange != null && enabled) Modifier.clickable { onChange(!on) } else Modifier)
            .padding(3.dp),
    ) {
        Box(Modifier.offset(x = 18.dp * k).size(22.dp).clip(CircleShape).background(Color.White))
    }
}

/** A switch row: title, optional explanation, the switch. Tapping anywhere flips it. */
@Composable
fun SwitchRow(title: String, sub: String?, on: Boolean, icon: String? = null, iconColor: Color = NoCap.Red, enabled: Boolean = true, onChange: (Boolean) -> Unit) {
    Row(
        Modifier.fillMaxWidth().heightIn(min = NcTokens.RowMin)
            .toggleable(on, enabled = enabled, role = Role.Switch) { onChange(it) }.padding(horizontal = NcTokens.RowPadH, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (icon != null) { IconTile(icon, iconColor, 30.dp); Spacer(Modifier.width(12.dp)) }
        Column(Modifier.weight(1f).padding(end = 10.dp)) {
            Text(title, style = MaterialTheme.typography.titleSmall, color = if (enabled) NoCap.Text else NoCap.Muted)
            if (!sub.isNullOrBlank()) Text(sub, style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
        }
        NcSwitch(on, enabled = enabled, onChange = null)
    }
}

/** Free content inside a card row (sliders, pickers), with the row's padding. */
@Composable
fun CardBlock(modifier: Modifier = Modifier, content: @Composable ColumnScope.() -> Unit) {
    Column(modifier.fillMaxWidth().padding(horizontal = NcTokens.RowPadH, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(8.dp), content = content)
}

/** The web's pill buttons: primary in NoCap red, secondary frosted. */
@Composable
fun PillButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier, primary: Boolean = true, enabled: Boolean = true, icon: String? = null) {
    val bg = when {
        primary -> NoCap.Red
        NoCap.light -> Color(0x14000000)
        else -> Color(0x1FFFFFFF)
    }
    Row(
        modifier.heightIn(min = 48.dp).clip(RoundedCornerShape(50)).background(if (enabled) bg else bg.copy(alpha = bg.alpha * 0.4f))
            .border(1.dp, if (primary) Color.White.copy(alpha = 0.14f) else NoCap.GlassEdge, RoundedCornerShape(50))
            .clickable(enabled = enabled, onClick = onClick).padding(horizontal = 22.dp, vertical = 12.dp),
        horizontalArrangement = Arrangement.Center,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        val ink = if (primary) Color.White else NoCap.Text
        if (icon != null) { NcIcon(icon, ink, 18.dp); Spacer(Modifier.width(8.dp)) }
        Text(text, style = MaterialTheme.typography.labelLarge, color = if (enabled) ink else ink.copy(alpha = 0.5f), maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

/** A NoCap glass dialog (title, text, pill buttons), instead of the stock alert. */
@Composable
fun NcDialog(
    title: String,
    text: String?,
    onDismiss: () -> Unit,
    confirm: Pair<String, () -> Unit>? = null,
    dismiss: Pair<String, () -> Unit>? = null,
    danger: Boolean = false,
    extra: List<Pair<String, () -> Unit>> = emptyList(),
    content: (@Composable ColumnScope.() -> Unit)? = null,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        containerColor = NoCap.GlassStrong,
        shape = RoundedCornerShape(NcTokens.Radius),
        title = { Text(title, style = MaterialTheme.typography.titleMedium, color = NoCap.Text) },
        text = if (text == null && content == null) null else {
            {
                Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
                    if (text != null) Text(text, style = MaterialTheme.typography.bodyMedium, color = NoCap.Muted)
                    content?.invoke(this)
                }
            }
        },
        confirmButton = {
            Column(horizontalAlignment = Alignment.End, verticalArrangement = Arrangement.spacedBy(8.dp)) {
                extra.forEach { (label, act) -> PillButton(label, act, primary = false) }
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    dismiss?.let { (label, act) -> PillButton(label, act, primary = false) }
                    confirm?.let { (label, act) ->
                        if (danger) {
                            Row(
                                Modifier.heightIn(min = 48.dp).clip(RoundedCornerShape(50)).background(NoCap.Bad).clickable(onClick = act).padding(horizontal = 22.dp, vertical = 12.dp),
                                verticalAlignment = Alignment.CenterVertically,
                            ) { Text(label, style = MaterialTheme.typography.labelLarge, color = Color.White) }
                        } else PillButton(label, act)
                    }
                }
            }
        },
    )
}

/** A big section title with an icon tile, like a web Settings page header. */
@Composable
fun PageHeader(title: String, sub: String?, icon: String, color: Color) {
    Row(Modifier.fillMaxWidth().padding(vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
        IconTile(icon, color, 46.dp)
        Column(Modifier.padding(start = 14.dp).weight(1f)) {
            Text(title, style = MaterialTheme.typography.titleMedium.copy(fontSize = 22.sp), color = NoCap.Text)
            if (!sub.isNullOrBlank()) Text(sub, style = MaterialTheme.typography.bodySmall, color = NoCap.Muted)
        }
    }
}

@Composable
fun RowScope.Grow() = Spacer(Modifier.weight(1f))

/** A small vertical gap. */
@Composable
fun Gap(h: Dp = 12.dp) = Spacer(Modifier.height(h))
