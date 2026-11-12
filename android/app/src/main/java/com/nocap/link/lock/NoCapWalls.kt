package com.nocap.link.lock

import android.app.WallpaperManager
import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.ColorMatrix
import android.graphics.ColorMatrixColorFilter
import android.graphics.Paint
import android.graphics.RadialGradient
import android.graphics.Rect
import android.graphics.RectF
import android.graphics.Shader
import android.os.Build
import android.view.WindowManager
import com.nocap.link.data.Prefs
import com.nocap.link.home.Wallpapers
import com.nocap.link.R
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * NoCap-styled wallpapers for the phone itself: one of the NoCapOS
 * wallpapers (Tide, Nebula, Midnight) washed in NoCap red with a red glow,
 * and for the lock screen the NoCap mark too. Red is made the dominant,
 * most colorful part of the picture, so on Android 12+ the phone's own
 * Material You colors (picked from the wallpaper) come out NoCap red.
 */
object NoCapWalls {
    const val RED = 0xFFFF2A2A.toInt()
    private const val DEEP = 0xFF8A0E14.toInt()

    /** How strongly the picture is washed in red (chosen so every built-in wallpaper reads as red; see tests). */
    const val TINT = 0.38f

    /** Green and blue are turned down before the wash, so blues and purples land on red, not magenta. */
    const val GREEN_KEEP = 0.7f
    const val BLUE_KEEP = 0.55f

    /** What the wallpaper does to one color: cool channels down, then the red wash. Pure. */
    fun nocapify(argb: Int): Int {
        val r = (argb shr 16) and 0xFF
        val g = (((argb shr 8) and 0xFF) * GREEN_KEEP).toInt()
        val b = ((argb and 0xFF) * BLUE_KEEP).toInt()
        return blend((0xFF shl 24) or (r shl 16) or (g shl 8) or b, RED, TINT)
    }

    /** Mixes [over] on top of [base] at [alpha] (0..1), ARGB ints, alpha channel opaque. Pure. */
    fun blend(base: Int, over: Int, alpha: Float): Int {
        val k = alpha.coerceIn(0f, 1f)
        fun ch(shift: Int): Int {
            val b = (base shr shift) and 0xFF
            val o = (over shr shift) and 0xFF
            return (b + (o - b) * k).toInt().coerceIn(0, 255)
        }
        return (0xFF shl 24) or (ch(16) shl 16) or (ch(8) shl 8) or ch(0)
    }

    /** Hue in degrees (0..360) and saturation (0..1) of an ARGB color. Pure. */
    fun hueSat(argb: Int): Pair<Float, Float> {
        val r = ((argb shr 16) and 0xFF) / 255f
        val g = ((argb shr 8) and 0xFF) / 255f
        val b = (argb and 0xFF) / 255f
        val max = maxOf(r, g, b)
        val min = minOf(r, g, b)
        val d = max - min
        if (d == 0f) return 0f to 0f
        val h = when (max) {
            r -> 60f * (((g - b) / d) % 6f)
            g -> 60f * ((b - r) / d + 2f)
            else -> 60f * ((r - g) / d + 4f)
        }
        return (if (h < 0) h + 360f else h) to (if (max == 0f) 0f else d / max)
    }

    /** Close enough to NoCap red for Material You to pick a red seed. Pure. */
    fun isNoCapRed(argb: Int): Boolean {
        val (h, s) = hueSat(argb)
        return (h >= 330f || h <= 30f) && s >= 0.35f
    }

    private fun screen(ctx: Context): Pair<Int, Int> {
        if (Build.VERSION.SDK_INT >= 30) {
            val b = ctx.getSystemService(WindowManager::class.java)?.maximumWindowMetrics?.bounds
            if (b != null && b.width() > 0) return b.width() to b.height()
        }
        val dm = ctx.resources.displayMetrics
        return dm.widthPixels to dm.heightPixels
    }

    /** The picture: the NoCapOS wallpaper you use (Tide if yours is your own pic or the grid), in NoCap red. */
    fun render(ctx: Context, lock: Boolean): Bitmap {
        val (w, h) = screen(ctx)
        val out = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888)
        val c = Canvas(out)
        c.drawColor(0xFF07080B.toInt())
        val id = Prefs.wallpaper.takeIf { p -> Wallpapers.builtIn.any { it.id == p } } ?: "tide"
        Wallpapers.bitmap(ctx, id)?.let { src ->
            // Center-crop to the screen.
            val k = maxOf(w / src.width.toFloat(), h / src.height.toFloat())
            val sw = (w / k).toInt()
            val sh = (h / k).toInt()
            val sx = (src.width - sw) / 2
            val sy = (src.height - sh) / 2
            val warm = Paint(Paint.FILTER_BITMAP_FLAG).apply { colorFilter = ColorMatrixColorFilter(ColorMatrix().apply { setScale(1f, GREEN_KEEP, BLUE_KEEP, 1f) }) }
            c.drawBitmap(src, Rect(sx, sy, sx + sw, sy + sh), Rect(0, 0, w, h), warm)
        }
        // Red wash, then the NoCapOS glow (top right, deep red bottom left).
        c.drawColor((((TINT * 255).toInt()) shl 24) or (RED and 0xFFFFFF))
        val p = Paint(Paint.ANTI_ALIAS_FLAG)
        p.shader = RadialGradient(w * 0.85f, 0f, w * 1.0f, (0xA0 shl 24) or (RED and 0xFFFFFF), 0, Shader.TileMode.CLAMP)
        c.drawRect(0f, 0f, w.toFloat(), h.toFloat(), p)
        p.shader = RadialGradient(0f, h.toFloat(), w * 1.1f, (0x90 shl 24) or (DEEP and 0xFFFFFF), 0, Shader.TileMode.CLAMP)
        c.drawRect(0f, 0f, w.toFloat(), h.toFloat(), p)
        if (lock) {
            // The NoCap mark under where the clock sits, with a glow.
            val cx = w / 2f
            val cy = h * 0.6f
            val size = w * 0.3f
            p.shader = RadialGradient(cx, cy, size * 1.4f, (0xB0 shl 24) or (RED and 0xFFFFFF), 0, Shader.TileMode.CLAMP)
            c.drawCircle(cx, cy, size * 1.4f, p)
            p.shader = null
            BitmapFactory.decodeResource(ctx.resources, R.drawable.nocap_mark)?.let { mark ->
                c.drawBitmap(mark, null, RectF(cx - size / 2, cy - size / 2, cx + size / 2, cy + size / 2), Paint(Paint.FILTER_BITMAP_FLAG or Paint.ANTI_ALIAS_FLAG))
            }
        }
        return out
    }

    /** Sets the NoCap wallpaper on the phone's home and/or lock screen. False if Android refused. */
    suspend fun apply(ctx: Context, home: Boolean, lock: Boolean): Boolean = withContext(Dispatchers.IO) {
        runCatching {
            val wm = WallpaperManager.getInstance(ctx)
            // Home first: setting only the home picture can copy it to the lock screen, so lock goes last.
            if (home) wm.setBitmap(render(ctx, lock = false), null, true, WallpaperManager.FLAG_SYSTEM)
            if (lock) wm.setBitmap(render(ctx, lock = true), null, true, WallpaperManager.FLAG_LOCK)
        }.isSuccess
    }
}
