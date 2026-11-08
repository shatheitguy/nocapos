package com.nocap.link

import android.app.WallpaperManager
import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import com.nocap.link.ui.Wallpaper
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File

/** The NoCapOS desktop's own wallpapers, a drawn classic, and your own picture. */
object Wallpapers {
    data class Wall(val id: String, val genz: String, val standard: String, val res: Int, val thumb: Int)

    val builtIn = listOf(
        Wall("tide", "Tide", "Tide", R.drawable.wall_tide, R.drawable.wall_tide_thumb),
        Wall("nebula", "Nebula", "Nebula", R.drawable.wall_nebula, R.drawable.wall_nebula_thumb),
        Wall("midnight", "Midnight", "Midnight", R.drawable.wall_midnight, R.drawable.wall_midnight_thumb),
    )
    const val GRID = "grid"
    const val CUSTOM = "custom"

    /** Bumped when the custom picture changes, so it's reloaded. */
    var version by mutableIntStateOf(0)

    fun customFile(ctx: Context) = File(ctx.filesDir, "wallpaper_custom.jpg")

    /** Copies a picked picture in (scaled to the screen) and shows it. */
    suspend fun setCustom(ctx: Context, uri: Uri): Boolean = withContext(Dispatchers.IO) {
        val bmp = decode(ctx, { ctx.contentResolver.openInputStream(uri) }) ?: return@withContext false
        customFile(ctx).outputStream().use { bmp.compress(Bitmap.CompressFormat.JPEG, 90, it) }
        withContext(Dispatchers.Main) { version++; Prefs.setWallpaper(CUSTOM) }
        true
    }

    /** The current wallpaper as a bitmap, or null for the drawn one. */
    fun bitmap(ctx: Context, id: String = Prefs.wallpaper): Bitmap? {
        builtIn.firstOrNull { it.id == id }?.let { w -> return decode(ctx, { ctx.resources.openRawResource(w.res) }) }
        if (id == CUSTOM) customFile(ctx).takeIf { it.exists() }?.let { f -> return decode(ctx, { f.inputStream() }) }
        return null
    }

    /** Also sets it as the phone's own wallpaper (home, lock or both). */
    suspend fun setSystem(ctx: Context, home: Boolean, lock: Boolean): Boolean = withContext(Dispatchers.IO) {
        val bmp = bitmap(ctx) ?: drawnGrid(ctx)
        runCatching {
            var which = 0
            if (home) which = which or WallpaperManager.FLAG_SYSTEM
            if (lock) which = which or WallpaperManager.FLAG_LOCK
            WallpaperManager.getInstance(ctx).setBitmap(bmp, null, true, which)
        }.isSuccess
    }

    private fun drawnGrid(ctx: Context): Bitmap {
        val dm = ctx.resources.displayMetrics
        val b = Bitmap.createBitmap(dm.widthPixels, dm.heightPixels, Bitmap.Config.ARGB_8888)
        b.eraseColor(android.graphics.Color.parseColor("#07080B"))
        return b
    }

    private fun decode(ctx: Context, open: () -> java.io.InputStream?, ): Bitmap? = runCatching {
        val dm = ctx.resources.displayMetrics
        val o = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        open()?.use { BitmapFactory.decodeStream(it, null, o) }
        var sample = 1
        while (o.outWidth / (sample * 2) >= dm.widthPixels && o.outHeight / (sample * 2) >= dm.heightPixels) sample *= 2
        open()?.use { BitmapFactory.decodeStream(it, null, BitmapFactory.Options().apply { inSampleSize = sample }) }
    }.getOrNull()
}

/** The home screen's background: the picked wallpaper, dimmed a little. */
@Composable
fun WallpaperLayer(modifier: Modifier = Modifier) {
    val ctx = LocalContext.current
    val id = Prefs.wallpaper
    val v = Wallpapers.version
    var img by remember { mutableStateOf<ImageBitmap?>(null) }
    LaunchedEffect(id, v) {
        img = withContext(Dispatchers.IO) { Wallpapers.bitmap(ctx, id)?.asImageBitmap() }
    }
    Box(modifier.fillMaxSize()) {
        val i = img
        if (i == null || id == Wallpapers.GRID) Wallpaper {} else Image(i, null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
        Box(Modifier.fillMaxSize().background(Color.Black.copy(alpha = Prefs.dim / 100f)))
    }
}
