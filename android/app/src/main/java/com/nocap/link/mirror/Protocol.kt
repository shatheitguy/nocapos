package com.nocap.link.mirror

import org.json.JSONObject

/**
 * Phone mirroring's wire format (see backend/internal/mirror): pure logic,
 * no Android classes, so it's unit-tested.
 *
 * Binary frames to the server: [type][8-byte pts, µs, big-endian][payload].
 * Text messages are JSON objects with a "t" field.
 */
object Protocol {
    const val TYPE_CONFIG: Byte = 1
    const val TYPE_KEY: Byte = 2
    const val TYPE_DELTA: Byte = 3
    const val TYPE_JPEG: Byte = 5
    const val HEADER = 9
    /** The relay refuses anything bigger. */
    const val MAX_FRAME = 4 shl 20
    /** Typed text per message. */
    const val MAX_TEXT = 4000

    /** One frame: header + [len] bytes copied by [fill] (offset HEADER). */
    inline fun frame(type: Byte, ptsUs: Long, len: Int, fill: (ByteArray) -> Unit): ByteArray {
        val b = ByteArray(HEADER + len)
        b[0] = type
        var v = ptsUs
        for (i in 8 downTo 1) { b[i] = (v and 0xff).toByte(); v = v ushr 8 }
        fill(b)
        return b
    }

    fun frame(type: Byte, ptsUs: Long, payload: ByteArray): ByteArray =
        frame(type, ptsUs, payload.size) { System.arraycopy(payload, 0, it, HEADER, payload.size) }

    /** Reads a header back (for tests and sanity checks): type to pts. */
    fun header(b: ByteArray): Pair<Byte, Long>? {
        if (b.size < HEADER) return null
        var v = 0L
        for (i in 1..8) v = (v shl 8) or (b[i].toLong() and 0xff)
        return b[0] to v
    }

    /**
     * The encoder size for a [w]×[h] screen: the long edge at most [maxLong],
     * aspect kept, both even (H.264 encoders want that), at least 2.
     */
    fun fit(w: Int, h: Int, maxLong: Int): Pair<Int, Int> {
        if (w <= 0 || h <= 0) return 2 to 2
        val long = maxOf(w, h)
        val s = if (long > maxLong) maxLong.toDouble() / long else 1.0
        fun even(x: Double) = (Math.round(x).toInt() and 1.inv()).coerceAtLeast(2)
        return even(w * s) to even(h * s)
    }

    /** A 0..1 position on the picture → screen pixels (inside the screen). */
    fun toScreen(x: Double, y: Double, w: Int, h: Int): Pair<Float, Float> =
        (x * (w - 1)).toFloat().coerceIn(0f, (w - 1).coerceAtLeast(0).toFloat()) to
            (y * (h - 1)).toFloat().coerceIn(0f, (h - 1).coerceAtLeast(0).toFloat())

    /**
     * A wheel turn at (x, y) as a swipe in screen pixels: dy > 0 (wheel down)
     * moves the content up, so the finger goes up. About an eighth of the
     * screen per notch, at most 60 % of it.
     */
    fun scrollSwipe(c: Control.Scroll, w: Int, h: Int): FloatArray {
        val (sx, sy) = toScreen(c.x, c.y, w, h)
        val dxPx = (c.dx * w / 8).coerceIn(-w * 0.6, w * 0.6).toFloat()
        val dyPx = (c.dy * h / 8).coerceIn(-h * 0.6, h * 0.6).toFloat()
        val ex = (sx - dxPx).coerceIn(1f, (w - 2).coerceAtLeast(1).toFloat())
        val ey = (sy - dyPx).coerceIn(1f, (h - 2).coerceAtLeast(1).toFloat())
        return floatArrayOf(sx, sy, ex, ey)
    }

    /** What the server or a NoCap OS window asks the phone to do. */
    sealed class Control {
        data class Tap(val x: Double, val y: Double) : Control()
        data class LongPress(val x: Double, val y: Double) : Control()
        data class Swipe(val x1: Double, val y1: Double, val x2: Double, val y2: Double, val ms: Long) : Control()
        data class Scroll(val x: Double, val y: Double, val dx: Double, val dy: Double) : Control()
        data class Global(val action: String) : Control()
        data class Text(val s: String) : Control()
        data class Key(val k: String) : Control()
        data class Volume(val up: Boolean) : Control()
        data class Viewers(val n: Int) : Control()
        data class Mode(val jpeg: Boolean) : Control()
        data object KeyFrame : Control()
    }

    val GLOBALS = setOf("back", "home", "recents", "notifications")
    val KEYS = setOf("Enter", "Backspace", "Delete", "Tab", "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown")

    /** Parses one text message; null when it's unknown or malformed. */
    fun parse(text: String): Control? {
        if (text.length > 64 * 1024) return null
        val o = runCatching { JSONObject(text) }.getOrNull() ?: return null
        fun unit(k: String): Double? = o.optDouble(k, Double.NaN).takeIf { it.isFinite() }?.coerceIn(0.0, 1.0)
        fun delta(k: String): Double = o.optDouble(k, 0.0).takeIf { it.isFinite() }?.coerceIn(-10.0, 10.0) ?: 0.0
        return when (o.optString("t")) {
            "tap" -> Control.Tap(unit("x") ?: return null, unit("y") ?: return null)
            "long" -> Control.LongPress(unit("x") ?: return null, unit("y") ?: return null)
            "swipe" -> Control.Swipe(
                unit("x1") ?: return null, unit("y1") ?: return null, unit("x2") ?: return null, unit("y2") ?: return null,
                o.optDouble("ms", 300.0).takeIf { it.isFinite() }?.toLong()?.coerceIn(50, 3000) ?: 300,
            )
            "scroll" -> {
                val dx = delta("dx")
                val dy = delta("dy")
                if (dx == 0.0 && dy == 0.0) null else Control.Scroll(unit("x") ?: return null, unit("y") ?: return null, dx, dy)
            }
            "global" -> o.optString("a").takeIf { it in GLOBALS }?.let { Control.Global(it) }
            "text" -> o.optString("s").takeIf { it.isNotEmpty() }?.let { Control.Text(it.take(MAX_TEXT)) }
            "key" -> o.optString("k").takeIf { it in KEYS }?.let { Control.Key(it) }
            "vol" -> when (o.optInt("d", 0)) { 1 -> Control.Volume(true); -1 -> Control.Volume(false); else -> null }
            "viewers" -> Control.Viewers(o.optInt("n", 0).coerceAtLeast(0))
            "mode" -> when (o.optString("m")) { "jpeg" -> Control.Mode(true); "h264" -> Control.Mode(false); else -> null }
            "keyframe" -> Control.KeyFrame
            else -> null
        }
    }

    fun meta(w: Int, h: Int, rot: Int, jpeg: Boolean, bitrate: Int, fps: Int, control: Boolean): String =
        JSONObject().put("t", "meta").put("w", w).put("h", h).put("rot", rot and 3)
            .put("codec", if (jpeg) "jpeg" else "h264").put("bitrate", bitrate).put("fps", fps).put("control", control).toString()

    fun state(s: String): String = JSONObject().put("t", "state").put("state", s).toString()

    /** A text field's content after one edit, with where the cursor goes. */
    data class Edit(val text: String, val cursor: Int)

    /**
     * Applies typing to [text] with the selection [selStart]..[selEnd]
     * (negative = none, so the end). [insert] replaces the selection; [key]
     * is "Backspace", "Delete", an arrow, or null. Null when nothing changes.
     */
    fun edit(text: String, selStart: Int, selEnd: Int, insert: String?, key: String?): Edit? {
        val n = text.length
        var a = if (selStart < 0) n else selStart.coerceIn(0, n)
        var b = if (selEnd < 0) n else selEnd.coerceIn(0, n)
        if (a > b) { val x = a; a = b; b = x }
        if (insert != null) return Edit(text.substring(0, a) + insert + text.substring(b), a + insert.length)
        return when (key) {
            "Backspace" -> when {
                a != b -> Edit(text.substring(0, a) + text.substring(b), a)
                a == 0 -> null
                else -> {
                    val from = if (a >= 2 && Character.isLowSurrogate(text[a - 1]) && Character.isHighSurrogate(text[a - 2])) a - 2 else a - 1
                    Edit(text.substring(0, from) + text.substring(a), from)
                }
            }
            "Delete" -> when {
                a != b -> Edit(text.substring(0, a) + text.substring(b), a)
                a >= n -> null
                else -> {
                    val to = if (a + 1 < n && Character.isHighSurrogate(text[a]) && Character.isLowSurrogate(text[a + 1])) a + 2 else a + 1
                    Edit(text.substring(0, a) + text.substring(to), a)
                }
            }
            "ArrowLeft" -> Edit(text, if (a != b) a else (a - 1).coerceAtLeast(0))
            "ArrowRight" -> Edit(text, if (a != b) b else (b + 1).coerceAtMost(n))
            "ArrowUp" -> Edit(text, 0)
            "ArrowDown" -> Edit(text, n)
            else -> null
        }
    }

    /**
     * Splits an encoder buffer that holds SPS/PPS into its NAL types (for
     * tests and logs): the type of each NAL after a 00 00 01 start code.
     */
    fun nalTypes(b: ByteArray, off: Int = 0, len: Int = b.size - off): List<Int> {
        val out = ArrayList<Int>()
        var i = off
        val end = off + len
        while (i + 3 < end) {
            if (b[i].toInt() == 0 && b[i + 1].toInt() == 0 && b[i + 2].toInt() == 1) {
                out += b[i + 3].toInt() and 0x1f
                i += 3
            } else i++
        }
        return out
    }
}
