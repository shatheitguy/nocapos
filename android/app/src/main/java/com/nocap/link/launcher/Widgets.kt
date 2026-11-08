package com.nocap.link.launcher

import android.appwidget.AppWidgetHost
import android.appwidget.AppWidgetHostView
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProviderInfo
import android.content.Context
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.SizeF
import android.view.HapticFeedbackConstants
import android.view.MotionEvent
import android.view.ViewConfiguration
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.graphics.drawable.toBitmap
import com.nocap.link.t
import com.nocap.link.ui.Label
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.glass
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/** Android home-screen widgets, hosted by NoCap OS. */
object Widgets {
    private const val HOST_ID = 0x0C0F
    private var hostRef: NoCapWidgetHost? = null
    lateinit var manager: AppWidgetManager
        private set

    fun host(ctx: Context): NoCapWidgetHost {
        hostRef?.let { return it }
        manager = AppWidgetManager.getInstance(ctx.applicationContext)
        return NoCapWidgetHost(ctx.applicationContext, HOST_ID).also { hostRef = it }
    }

    fun info(ctx: Context, id: Int): AppWidgetProviderInfo? {
        host(ctx)
        return runCatching { manager.getAppWidgetInfo(id) }.getOrNull()
    }

    /** How many cells a widget wants, from its own minimum size. */
    fun span(ctx: Context, info: AppWidgetProviderInfo, cellW: Float, cellH: Float): Pair<Int, Int> {
        val cols = com.nocap.link.Prefs.columns
        val rows = com.nocap.link.Prefs.rows
        if (Build.VERSION.SDK_INT >= 31 && info.targetCellWidth > 0 && info.targetCellHeight > 0) {
            return info.targetCellWidth.coerceIn(1, cols) to info.targetCellHeight.coerceIn(1, rows)
        }
        val d = ctx.resources.displayMetrics.density
        val w = kotlin.math.ceil(info.minWidth / d / cellW).toInt()
        val h = kotlin.math.ceil(info.minHeight / d / cellH).toInt()
        return w.coerceIn(1, cols) to h.coerceIn(1, rows)
    }

    fun canResize(info: AppWidgetProviderInfo?) = info != null && info.resizeMode != AppWidgetProviderInfo.RESIZE_NONE

    fun delete(ctx: Context, id: Int) {
        runCatching { host(ctx).deleteAppWidgetId(id) }
    }
}

class NoCapWidgetHost(ctx: Context, id: Int) : AppWidgetHost(ctx, id) {
    override fun onCreateView(context: Context, appWidgetId: Int, appWidget: AppWidgetProviderInfo?): AppWidgetHostView = LongPressHostView(context)
}

/** A widget view that tells the launcher when it's long-pressed (to move, resize or remove it). */
class LongPressHostView(ctx: Context) : AppWidgetHostView(ctx) {
    var onLongPress: (() -> Unit)? = null
    private val handler = Handler(Looper.getMainLooper())
    private val slop = ViewConfiguration.get(ctx).scaledTouchSlop
    private var downX = 0f
    private var downY = 0f
    private var fired = false
    private val press = Runnable {
        fired = true
        performHapticFeedback(HapticFeedbackConstants.LONG_PRESS)
        onLongPress?.invoke()
    }

    override fun onInterceptTouchEvent(ev: MotionEvent): Boolean {
        when (ev.actionMasked) {
            MotionEvent.ACTION_DOWN -> {
                fired = false
                downX = ev.x; downY = ev.y
                handler.postDelayed(press, ViewConfiguration.getLongPressTimeout().toLong())
            }
            MotionEvent.ACTION_MOVE -> if (kotlin.math.hypot(ev.x - downX, ev.y - downY) > slop) handler.removeCallbacks(press)
            MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> handler.removeCallbacks(press)
        }
        return fired
    }

    override fun onTouchEvent(ev: MotionEvent): Boolean {
        if (fired) {
            if (ev.actionMasked == MotionEvent.ACTION_UP || ev.actionMasked == MotionEvent.ACTION_CANCEL) fired = false
            return true
        }
        return super.onTouchEvent(ev)
    }
}

/** One hosted Android widget, sized to its cells. */
@Composable
fun AndroidWidget(item: WidgetItem, width: Dp, height: Dp, onLongPress: () -> Unit) {
    val ctx = LocalContext.current
    val info = remember(item.widgetId) { Widgets.info(ctx, item.widgetId) }
    if (info == null) {
        Box(Modifier.fillMaxSize().glass(RoundedCornerShape(18.dp)).clickable(onClick = onLongPress), contentAlignment = Alignment.Center) {
            Text(t("This widget dipped. Tap to remove.", "This widget is unavailable. Tap to remove."), style = MaterialTheme.typography.bodySmall)
        }
        return
    }
    AndroidView(
        factory = { c ->
            Widgets.host(c).createView(c, item.widgetId, info).also { v ->
                (v as? LongPressHostView)?.onLongPress = onLongPress
                v.setPadding(0, 0, 0, 0)
            }
        },
        update = { v ->
            (v as? LongPressHostView)?.onLongPress = onLongPress
            val w = width.value.toInt()
            val h = height.value.toInt()
            if (Build.VERSION.SDK_INT >= 31) {
                v.updateAppWidgetSize(Bundle(), listOf(SizeF(w.toFloat(), h.toFloat())))
            } else {
                @Suppress("DEPRECATION")
                v.updateAppWidgetSize(null, w, h, w, h)
            }
        },
        modifier = Modifier.fillMaxSize(),
    )
}

private class ProviderRow(val info: AppWidgetProviderInfo, val label: String, val app: String, val preview: ImageBitmap?, val spanText: String)

/**
 * The widget picker: NoCap's own widgets first, then every app's widgets,
 * with previews.
 */
@Composable
fun WidgetPicker(cellW: Float, cellH: Float, onNoCap: (String) -> Unit, onPick: (AppWidgetProviderInfo) -> Unit, onClose: () -> Unit) {
    val ctx = LocalContext.current
    var rows by remember { mutableStateOf<List<ProviderRow>?>(null) }
    LaunchedEffect(Unit) {
        rows = withContext(Dispatchers.IO) {
            Widgets.host(ctx)
            val pm = ctx.packageManager
            val dpi = ctx.resources.displayMetrics.densityDpi
            val px = (140 * ctx.resources.displayMetrics.density).toInt()
            Widgets.manager.installedProviders.mapNotNull { info ->
                runCatching {
                    val app = pm.getApplicationLabel(pm.getApplicationInfo(info.provider.packageName, 0)).toString()
                    val prev = (info.loadPreviewImage(ctx, dpi) ?: info.loadIcon(ctx, dpi))?.let { d ->
                        val w = d.intrinsicWidth.takeIf { it > 0 } ?: px
                        val h = d.intrinsicHeight.takeIf { it > 0 } ?: px
                        val scale = minOf(1f, px.toFloat() / maxOf(w, h))
                        d.toBitmap((w * scale).toInt().coerceAtLeast(1), (h * scale).toInt().coerceAtLeast(1)).asImageBitmap()
                    }
                    val (sw, sh) = Widgets.span(ctx, info, cellW, cellH)
                    ProviderRow(info, info.loadLabel(pm), app, prev, "$sw×$sh")
                }.getOrNull()
            }.sortedWith(compareBy({ it.app.lowercase() }, { it.label.lowercase() }))
        }
    }
    Column(Modifier.fillMaxSize().background(Color(0xF207080B)).statusBarsPadding().navigationBarsPadding().padding(horizontal = 16.dp)) {
        Row(Modifier.fillMaxWidth().padding(top = 12.dp, bottom = 8.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(t("ADD WIDGETS", "WIDGETS"), style = MaterialTheme.typography.headlineSmall, modifier = Modifier.weight(1f))
            IconButton(onClick = onClose) { Icon(Icons.Filled.Close, t("Close", "Close"), tint = NoCap.Text) }
        }
        LazyColumn(contentPadding = PaddingValues(bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            item {
                Label(t("NoCap widgets", "NoCap widgets"), color = NoCap.Red)
                Spacer(Modifier.height(8.dp))
                LazyRow(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    items(NoCapKinds.all) { k ->
                        Column(
                            Modifier.width(150.dp).glass(RoundedCornerShape(16.dp)).clickable { onNoCap(k) }.padding(12.dp),
                        ) {
                            Text(noCapWidgetName(k), style = MaterialTheme.typography.titleSmall)
                            Text(noCapWidgetBlurb(k), style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
            }
            val list = rows
            if (list == null) {
                item { Text(t("Loading the widget lineup…", "Loading widgets…"), style = MaterialTheme.typography.bodyMedium) }
            } else if (list.isEmpty()) {
                item { Text(t("No app widgets on this phone.", "No app widgets on this phone."), style = MaterialTheme.typography.bodyMedium) }
            } else {
                list.groupBy { it.app }.forEach { (app, ws) ->
                    item(key = app) {
                        Column {
                            Label(app)
                            Spacer(Modifier.height(8.dp))
                            LazyRow(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                                items(ws) { r ->
                                    Column(Modifier.width(160.dp).glass(RoundedCornerShape(16.dp)).clickable { onPick(r.info) }.padding(10.dp)) {
                                        Box(Modifier.fillMaxWidth().heightIn(min = 70.dp).clip(RoundedCornerShape(10.dp)).background(Color(0x22FFFFFF)), contentAlignment = Alignment.Center) {
                                            r.preview?.let { Image(it, null, Modifier.size(120.dp, 90.dp), contentScale = ContentScale.Fit) }
                                        }
                                        Spacer(Modifier.height(6.dp))
                                        Text(r.label, style = MaterialTheme.typography.labelLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                        Text(r.spanText, style = MaterialTheme.typography.bodySmall)
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

fun noCapWidgetName(kind: String) = when (kind) {
    NoCapKinds.CLOCK -> t("Clock", "Clock")
    NoCapKinds.SERVER -> t("Server Vibes", "Server status")
    NoCapKinds.QUICK -> t("Quick Moves", "Quick actions")
    NoCapKinds.FILES -> t("Fresh Files", "Recent files")
    NoCapKinds.AI -> t("Ask NoCap AI", "NoCap AI")
    else -> kind
}

fun noCapWidgetBlurb(kind: String) = when (kind) {
    NoCapKinds.CLOCK -> t("Big time, big date", "Time and date")
    NoCapKinds.SERVER -> t("CPU, RAM, storage, online or not", "CPU, memory, storage and status")
    NoCapKinds.QUICK -> t("Desktop, AI, send, files", "Desktop, AI, send a file, files")
    NoCapKinds.FILES -> t("What changed lately on your server", "Recently changed files on your server")
    NoCapKinds.AI -> t("One tap to ask anything", "Ask the server's AI Assistant")
    else -> ""
}
