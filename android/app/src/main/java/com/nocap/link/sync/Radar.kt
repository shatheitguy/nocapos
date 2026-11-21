package com.nocap.link.sync

import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.spring
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.nocap.link.R
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.reducedMotion

/** One server on the radar. Linked ones glow green. */
data class RadarBlip(val id: String, val linked: Boolean)

private val BRAND = Color(NoCap.BRAND)

/**
 * The NoCap radar, drawn like the web one (Settings → Your Squad): the
 * mark in the middle, red pulses rolling out and a slow sweep while it
 * searches, and a blip that pops in for each server found, in the same
 * spot as on the web ([LinkAsk.blipSpot]). With Android's animations
 * removed it's just the rings.
 */
@Composable
fun NoCapRadar(scanning: Boolean, blips: List<RadarBlip>, label: String, size: Dp = 200.dp) {
    val still = reducedMotion()
    val moving = scanning && !still
    val tr = rememberInfiniteTransition(label = "radar")
    val sweep by if (moving) tr.animateFloat(0f, 360f, infiniteRepeatable(tween(4500, easing = LinearEasing)), label = "sweep") else remember { mutableFloatStateOf(0f) }
    val pulse by if (moving) tr.animateFloat(0f, 1f, infiniteRepeatable(tween(3000, easing = LinearEasing)), label = "pulse") else remember { mutableFloatStateOf(0f) }
    val breathe by if (moving) tr.animateFloat(1f, 1.06f, infiniteRepeatable(tween(1500), RepeatMode.Reverse), label = "breathe") else remember { mutableFloatStateOf(1f) }

    Box(Modifier.size(size).semantics { contentDescription = label }, contentAlignment = Alignment.Center) {
        Canvas(Modifier.size(size)) {
            val c = center
            val r = this.size.minDimension / 2f
            drawCircle(
                Brush.radialGradient(listOf(BRAND.copy(alpha = 0.16f), BRAND.copy(alpha = 0.05f), Color.Transparent), c, r),
                r, c,
            )
            for (f in listOf(0.33f, 0.66f, 0.98f)) drawCircle(BRAND.copy(alpha = 0.32f), r * f, c, style = Stroke(1.dp.toPx()))
            drawLine(BRAND.copy(alpha = 0.18f), Offset(c.x - r, c.y), Offset(c.x + r, c.y), 1.dp.toPx())
            drawLine(BRAND.copy(alpha = 0.18f), Offset(c.x, c.y - r), Offset(c.x, c.y + r), 1.dp.toPx())
            if (moving) {
                // Three pulses a third of a cycle apart.
                for (k in 0 until 3) {
                    val p = (pulse + k / 3f) % 1f
                    val eased = 1f - (1f - p) * (1f - p)
                    drawCircle(BRAND.copy(alpha = 0.85f * (1f - p)), r * (0.12f + 0.88f * eased), c, style = Stroke(2.dp.toPx()))
                }
                rotate(sweep, c) {
                    drawCircle(
                        Brush.sweepGradient(
                            0f to BRAND.copy(alpha = 0.42f), 0.14f to BRAND.copy(alpha = 0.10f), 0.25f to Color.Transparent, 1f to Color.Transparent,
                            center = c,
                        ),
                        r, c,
                    )
                }
            }
        }
        blips.forEachIndexed { i, b -> Blip(b, i, size, still) }
        Box(
            Modifier.scale(breathe).clip(CircleShape).background(NoCap.GlassStrong.copy(alpha = 0.7f)).padding(8.dp),
            contentAlignment = Alignment.Center,
        ) {
            Image(painterResource(R.drawable.nocap_mark), null, Modifier.size(size * 0.22f))
        }
    }
}

@Composable
private fun Blip(b: RadarBlip, index: Int, size: Dp, still: Boolean) {
    val pop = remember(b.id) { Animatable(if (still) 1f else 0f) }
    LaunchedEffect(b.id) {
        if (!still) {
            kotlinx.coroutines.delay(index * 90L)
            pop.animateTo(1f, spring(dampingRatio = Spring.DampingRatioMediumBouncy, stiffness = Spring.StiffnessMediumLow))
        }
    }
    val (fx, fy) = LinkAsk.blipSpot(b.id, index)
    val color = if (b.linked) NoCap.Ok else BRAND
    val dot = 10.dp
    Box(
        Modifier
            .offset(size * (fx - 0.5f), size * (fy - 0.5f))
            .scale(pop.value)
            .size(dot)
            .clip(CircleShape)
            .background(Brush.radialGradient(listOf(color, color.copy(alpha = 0.55f)))),
    )
}

