package com.nocap.link.widget

import android.annotation.SuppressLint
import android.content.Context
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Path
import android.graphics.RectF
import android.location.Location
import android.location.LocationManager
import android.Manifest
import androidx.compose.runtime.mutableStateMapOf
import androidx.core.content.ContextCompat
import com.nocap.link.data.Opt
import com.nocap.link.data.t
import java.net.URLEncoder
import java.util.concurrent.TimeUnit
import kotlin.math.cos
import kotlin.math.sin
import okhttp3.OkHttpClient
import okhttp3.Request
import org.json.JSONObject

/**
 * Weather from Open-Meteo (free, no account, no key). Uses the phone's
 * location only if you allow it; otherwise a city you search for.
 */
object Weather {
    data class Place(val name: String, val lat: Double, val lon: Double, val country: String = "") {
        fun encode() = "$name|$lat|$lon|$country"
        companion object {
            fun decode(s: String): Place? = s.split('|').takeIf { it.size >= 3 }?.let { p ->
                val lat = p[1].toDoubleOrNull() ?: return null
                val lon = p[2].toDoubleOrNull() ?: return null
                Place(p[0], lat, lon, p.getOrElse(3) { "" })
            }
        }
    }

    data class Day(val date: String, val code: Int, val hi: Double, val lo: Double, val rain: Int)
    data class Hour(val time: String, val code: Int, val temp: Double, val isDay: Boolean)
    data class Report(
        val place: Place, val temp: Double, val feels: Double, val code: Int, val isDay: Boolean, val wind: Double,
        val humidity: Int, val hi: Double, val lo: Double, val days: List<Day>, val hours: List<Hour>, val fetched: Long, val f: Boolean,
    )

    private val http = OkHttpClient.Builder().connectTimeout(10, TimeUnit.SECONDS).readTimeout(15, TimeUnit.SECONDS).build()

    /** Latest reports, keyed by place, shared by every weather widget. */
    val reports = mutableStateMapOf<String, Report>()

    private fun key(p: Place, f: Boolean) = "%.2f,%.2f,%s".format(p.lat, p.lon, if (f) "f" else "c")

    fun cached(p: Place, f: Boolean): Report? = reports[key(p, f)]

    /** City search (blocking). */
    fun search(q: String): List<Place> {
        val url = "https://geocoding-api.open-meteo.com/v1/search?count=8&language=en&format=json&name=" + URLEncoder.encode(q.trim(), "UTF-8")
        val o = JSONObject(get(url))
        val a = o.optJSONArray("results") ?: return emptyList()
        return (0 until a.length()).map {
            val r = a.getJSONObject(it)
            val admin = r.optString("admin1")
            Place(r.optString("name"), r.optDouble("latitude"), r.optDouble("longitude"), listOf(admin, r.optString("country")).filter { s -> s.isNotBlank() }.joinToString(", "))
        }
    }

    /** Forecast (blocking); cached for 20 minutes. */
    fun forecast(p: Place, f: Boolean, force: Boolean = false): Report {
        cached(p, f)?.let { if (!force && System.currentTimeMillis() - it.fetched < 20 * 60_000) return it }
        val unit = if (f) "&temperature_unit=fahrenheit&wind_speed_unit=mph" else ""
        val url = "https://api.open-meteo.com/v1/forecast?latitude=${rough(p.lat)}&longitude=${rough(p.lon)}" +
            "&current=temperature_2m,apparent_temperature,weather_code,is_day,wind_speed_10m,relative_humidity_2m" +
            "&hourly=temperature_2m,weather_code,is_day&daily=weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max" +
            "&timezone=auto&forecast_days=7&forecast_hours=24$unit"
        val o = JSONObject(get(url))
        val cur = o.getJSONObject("current")
        val d = o.getJSONObject("daily")
        val h = o.getJSONObject("hourly")
        val days = (0 until (d.optJSONArray("time")?.length() ?: 0)).map { i ->
            Day(
                d.getJSONArray("time").getString(i), d.getJSONArray("weather_code").optInt(i),
                d.getJSONArray("temperature_2m_max").optDouble(i), d.getJSONArray("temperature_2m_min").optDouble(i),
                d.optJSONArray("precipitation_probability_max")?.optInt(i) ?: 0,
            )
        }
        val hours = (0 until (h.optJSONArray("time")?.length() ?: 0)).map { i ->
            Hour(h.getJSONArray("time").getString(i), h.getJSONArray("weather_code").optInt(i), h.getJSONArray("temperature_2m").optDouble(i), h.getJSONArray("is_day").optInt(i) == 1)
        }
        val r = Report(
            p, cur.optDouble("temperature_2m"), cur.optDouble("apparent_temperature"), cur.optInt("weather_code"), cur.optInt("is_day", 1) == 1,
            cur.optDouble("wind_speed_10m"), cur.optInt("relative_humidity_2m"),
            days.firstOrNull()?.hi ?: 0.0, days.firstOrNull()?.lo ?: 0.0, days, hours, System.currentTimeMillis(), f,
        )
        reports[key(p, f)] = r
        return r
    }

    /** Weather is opt-in: nothing goes to Open-Meteo until you turn it on. */
    class Off : IllegalStateException("Weather is off")

    /** About 1 km: Open-Meteo never gets a precise location. */
    fun rough(v: Double): Double = Math.round(v * 100.0) / 100.0

    private fun get(url: String): String {
        if (!Opt.weatherOn.v) throw Off()
        return fetch(url)
    }

    private fun fetch(url: String): String = http.newCall(Request.Builder().url(url).header("User-Agent", "NoCapOS-phone").build()).execute().use { r ->
        if (!r.isSuccessful) error("weather service said HTTP ${r.code}")
        r.body?.string() ?: ""
    }

    fun hasLocation(ctx: Context) = ContextCompat.checkSelfPermission(ctx, Manifest.permission.ACCESS_COARSE_LOCATION) == PackageManager.PERMISSION_GRANTED

    /**
     * The phone's rough location (only with permission), rounded to about
     * 1 km. Not named through a geocoding service: that would send it to one
     * more company.
     */
    @SuppressLint("MissingPermission")
    fun here(ctx: Context): Place? {
        // Checked right before use: location access can be revoked at any time.
        if (!Opt.weatherOn.v || !hasLocation(ctx)) return null
        val lm = ctx.getSystemService(LocationManager::class.java) ?: return null
        val loc: Location = listOf(LocationManager.NETWORK_PROVIDER, LocationManager.PASSIVE_PROVIDER, LocationManager.GPS_PROVIDER)
            .mapNotNull { p -> runCatching { lm.getLastKnownLocation(p) }.getOrNull() }
            .maxByOrNull { it.time } ?: return null
        val p = Place(t("Right here", "My location"), rough(loc.latitude), rough(loc.longitude))
        Opt.weatherPlace.set(p.encode()) // remembered for home-screen widgets in other launchers
        return p
    }

    fun describe(code: Int): String = when (code) {
        0 -> t("Clear skies", "Clear")
        1 -> t("Mostly clear", "Mainly clear")
        2 -> t("Some clouds", "Partly cloudy")
        3 -> t("Cloudy era", "Overcast")
        45, 48 -> t("Foggy", "Fog")
        51, 53, 55, 56, 57 -> t("Drizzle", "Drizzle")
        61, 63, 66 -> t("Rain", "Rain")
        65, 67 -> t("Heavy rain", "Heavy rain")
        71, 73, 75, 77 -> t("Snow", "Snow")
        80, 81, 82 -> t("Showers", "Rain showers")
        85, 86 -> t("Snow showers", "Snow showers")
        95, 96, 99 -> t("Stormy", "Thunderstorm")
        else -> t("Weather", "Weather")
    }

    enum class Kind { SUN, MOON, PARTLY, PARTLY_NIGHT, CLOUD, FOG, RAIN, SNOW, STORM }

    fun kind(code: Int, day: Boolean): Kind = when (code) {
        0 -> if (day) Kind.SUN else Kind.MOON
        1, 2 -> if (day) Kind.PARTLY else Kind.PARTLY_NIGHT
        3 -> Kind.CLOUD
        45, 48 -> Kind.FOG
        71, 73, 75, 77, 85, 86 -> Kind.SNOW
        95, 96, 99 -> Kind.STORM
        in 51..67, in 80..82 -> Kind.RAIN
        else -> Kind.CLOUD
    }

    /** Draws a weather glyph (used by in-app widgets and by home-screen widgets in other launchers). */
    fun draw(c: Canvas, size: Float, k: Kind, accent: Int) {
        val p = Paint(Paint.ANTI_ALIAS_FLAG)
        val s = size
        fun sun(cx: Float, cy: Float, r: Float) {
            p.style = Paint.Style.FILL
            p.color = 0xFFFFC21A.toInt()
            c.drawCircle(cx, cy, r, p)
            p.style = Paint.Style.STROKE; p.strokeWidth = r * 0.22f; p.strokeCap = Paint.Cap.ROUND
            for (i in 0 until 8) {
                val a = Math.PI * 2 * i / 8
                c.drawLine(cx + (r * 1.35f * cos(a)).toFloat(), cy + (r * 1.35f * sin(a)).toFloat(), cx + (r * 1.75f * cos(a)).toFloat(), cy + (r * 1.75f * sin(a)).toFloat(), p)
            }
        }
        fun moon(cx: Float, cy: Float, r: Float) {
            p.style = Paint.Style.FILL
            p.color = 0xFFE8E9F2.toInt()
            val path = Path().apply {
                addCircle(cx, cy, r, Path.Direction.CW)
                val cut = Path().apply { addCircle(cx + r * 0.55f, cy - r * 0.35f, r * 0.85f, Path.Direction.CW) }
                op(cut, Path.Op.DIFFERENCE)
            }
            c.drawPath(path, p)
        }
        fun cloud(cx: Float, cy: Float, w: Float, color: Int) {
            p.style = Paint.Style.FILL
            p.color = color
            val h = w * 0.42f
            c.drawRoundRect(RectF(cx - w / 2, cy - h / 4, cx + w / 2, cy + h / 2), h / 2, h / 2, p)
            c.drawCircle(cx - w * 0.15f, cy - h * 0.15f, h * 0.48f, p)
            c.drawCircle(cx + w * 0.13f, cy - h * 0.32f, h * 0.62f, p)
        }
        fun drops(cx: Float, cy: Float, w: Float, color: Int, snow: Boolean) {
            p.color = color
            for (i in -1..1) {
                val x = cx + i * w * 0.28f
                if (snow) { p.style = Paint.Style.FILL; c.drawCircle(x, cy, w * 0.06f, p) } else {
                    p.style = Paint.Style.STROKE; p.strokeWidth = w * 0.07f; p.strokeCap = Paint.Cap.ROUND
                    c.drawLine(x + w * 0.04f, cy - w * 0.08f, x - w * 0.04f, cy + w * 0.1f, p)
                }
            }
        }
        val cloudC = 0xFFDDE1EA.toInt()
        val darkC = 0xFF9AA0AE.toInt()
        when (k) {
            Kind.SUN -> sun(s / 2, s / 2, s * 0.22f)
            Kind.MOON -> moon(s / 2, s / 2, s * 0.3f)
            Kind.PARTLY -> { sun(s * 0.38f, s * 0.38f, s * 0.16f); cloud(s * 0.56f, s * 0.6f, s * 0.6f, cloudC) }
            Kind.PARTLY_NIGHT -> { moon(s * 0.38f, s * 0.36f, s * 0.2f); cloud(s * 0.56f, s * 0.6f, s * 0.6f, cloudC) }
            Kind.CLOUD -> { cloud(s * 0.42f, s * 0.42f, s * 0.5f, darkC); cloud(s * 0.56f, s * 0.58f, s * 0.66f, cloudC) }
            Kind.FOG -> {
                cloud(s / 2, s * 0.4f, s * 0.62f, cloudC)
                p.style = Paint.Style.STROKE; p.strokeWidth = s * 0.05f; p.strokeCap = Paint.Cap.ROUND; p.color = darkC
                c.drawLine(s * 0.2f, s * 0.72f, s * 0.8f, s * 0.72f, p); c.drawLine(s * 0.28f, s * 0.84f, s * 0.72f, s * 0.84f, p)
            }
            Kind.RAIN -> { cloud(s / 2, s * 0.4f, s * 0.66f, cloudC); drops(s / 2, s * 0.78f, s, 0xFF4FB3FF.toInt(), false) }
            Kind.SNOW -> { cloud(s / 2, s * 0.4f, s * 0.66f, cloudC); drops(s / 2, s * 0.78f, s, 0xFFFFFFFF.toInt(), true) }
            Kind.STORM -> {
                cloud(s / 2, s * 0.38f, s * 0.66f, darkC)
                p.style = Paint.Style.FILL; p.color = accent
                val bolt = Path().apply {
                    moveTo(s * 0.52f, s * 0.52f); lineTo(s * 0.4f, s * 0.74f); lineTo(s * 0.5f, s * 0.74f)
                    lineTo(s * 0.44f, s * 0.94f); lineTo(s * 0.64f, s * 0.66f); lineTo(s * 0.53f, s * 0.66f); lineTo(s * 0.6f, s * 0.52f); close()
                }
                c.drawPath(bolt, p)
            }
        }
    }

    fun bitmap(px: Int, k: Kind, accent: Int): Bitmap = Bitmap.createBitmap(px, px, Bitmap.Config.ARGB_8888).also { draw(Canvas(it), px.toFloat(), k, accent) }

    fun deg(v: Double) = "${Math.round(v)}°"
}
