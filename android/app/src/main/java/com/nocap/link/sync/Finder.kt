package com.nocap.link.sync

import android.Manifest
import android.annotation.SuppressLint
import android.bluetooth.BluetoothManager
import android.bluetooth.le.ScanCallback
import android.bluetooth.le.ScanFilter
import android.bluetooth.le.ScanResult
import android.bluetooth.le.ScanSettings
import android.content.Context
import android.content.pm.PackageManager
import android.location.LocationManager
import android.os.Build
import android.os.ParcelUuid
import androidx.core.content.ContextCompat
import androidx.core.location.LocationManagerCompat
import com.nocap.link.MainActivity
import com.nocap.link.settings.Permissions
import java.util.UUID

/** A NoCap server found nearby. */
data class FoundServer(
    /** "" when it was only seen over Bluetooth. */
    val nocapId: String,
    val name: String,
    /** scheme://host:port on the home network; null when only seen over Bluetooth. */
    val origin: String?,
    val version: String,
    /** "new" (waiting for its first setup), "done", or "" (an older server that doesn't say). */
    val setup: String,
    val lan: Boolean,
    val bluetooth: Boolean,
) {
    val isNew get() = setup == "new"
}

/** One NoCap advertisement heard over Bluetooth. */
data class BleHit(val name: String, val setup: String)

/**
 * "Find NoCap servers": the home network (mDNS, see [Nearby]) and, if the
 * phone allows it, a Bluetooth LE scan for servers advertising the NoCap
 * service UUID. Bluetooth only finds them: everything after that (setting
 * up, linking) goes over the home network, and nothing is trusted until
 * the person checks the certificate and enters the code or creates the
 * account. Blocking calls: run them off the main thread.
 */
object Finder {
    /** The NoCap service UUID without its last byte (the server's internal/beacon). */
    const val UUID_BASE = "6e6f6361-7001-4e0c-a9b1-0d15c0f3b2"
    private val SERVICE: UUID = UUID.fromString(UUID_BASE + "00")
    /** The last byte is the setup state: matched loosely, read after. */
    private val MASK: UUID = UUID.fromString("ffffffff-ffff-ffff-ffff-ffffffffff00")

    /** "new" / "done" from an advertisement's service UUIDs, or null when it isn't NoCap's. */
    fun bleSetup(uuids: List<String>): String? {
        for (u in uuids) {
            val s = u.lowercase()
            if (s.length != 36 || !s.startsWith(UUID_BASE)) continue
            when (s.takeLast(2)) {
                "01" -> return "new"
                "00" -> return "done"
            }
        }
        return null
    }

    /** The server's short Bluetooth name for [name] (same rule as the server). */
    fun shortName(name: String): String {
        val b = StringBuilder()
        for (c in name) {
            if (b.length >= 10) break
            when {
                c in 'a'..'z' || c in 'A'..'Z' || c in '0'..'9' || c == '-' -> b.append(c)
                c == '.' && b.isNotEmpty() -> return b.toString()
            }
        }
        return if (b.isEmpty()) "NoCap" else b.toString()
    }

    /** A server from its mDNS answer, or null when it isn't a NoCap server. */
    fun fromMdns(serviceName: String, origin: String, attrs: Map<String, String>): FoundServer? {
        val id = attrs["nocap_id"].orEmpty()
        if (!Regex("^NC-[A-Za-z0-9-]{1,37}$").matches(id)) return null
        val setup = attrs["setup"].orEmpty().takeIf { it == "new" || it == "done" } ?: ""
        val version = attrs["version"].orEmpty().filter { it.code in 0x20..0x7e }.take(32)
        val name = serviceName.removePrefix("NoCap ").trim().ifBlank { id }.take(64)
        return FoundServer(id, name, origin, version, setup, lan = true, bluetooth = false)
    }

    /**
     * Network results plus Bluetooth ones: a Bluetooth hit with the same
     * short name marks the network entry; the rest are listed on their own
     * (nearby, but not on this network yet).
     */
    fun merge(lan: List<FoundServer>, ble: List<BleHit>): List<FoundServer> {
        val out = lan.distinctBy { it.nocapId }.toMutableList()
        for (hit in ble.distinct()) {
            val i = out.indexOfFirst { it.lan && shortName(it.name).equals(hit.name, ignoreCase = true) }
            if (i >= 0) {
                val e = out[i]
                out[i] = e.copy(bluetooth = true, setup = e.setup.ifEmpty { hit.setup })
            } else if (out.none { !it.lan && it.name.equals(hit.name, ignoreCase = true) }) {
                out += FoundServer("", hit.name, null, "", hit.setup, lan = false, bluetooth = true)
            }
        }
        // Reachable ones first, then by name.
        return out.sortedWith(compareBy<FoundServer>({ !it.lan }, { it.name.lowercase() }))
    }

    /** NoCap servers on the home network. */
    fun lan(ctx: Context, browseMs: Long = 3000): List<FoundServer> =
        Nearby.browse(ctx, browseMs, max = 16).mapNotNull { fromMdns(it.serviceName, it.origin, it.attrs) }

    // ---------- Bluetooth ----------

    enum class Bt { NO_HARDWARE, NEED_PERMISSION, OFF, LOCATION_OFF, READY }

    /** What scanning needs from Android: "Nearby devices" on 12+, location before. */
    fun blePermissions(): List<String> = when {
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.S -> listOf(Manifest.permission.BLUETOOTH_SCAN, Manifest.permission.BLUETOOTH_CONNECT)
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q -> listOf(Manifest.permission.ACCESS_FINE_LOCATION)
        else -> listOf(Manifest.permission.ACCESS_COARSE_LOCATION)
    }

    /**
     * Everything the "Nearby devices" prompt covers: the Bluetooth scan plus,
     * on Android 13+, nearby Wi-Fi devices (local network discovery on newer
     * Android). Only [blePermissions] are required for the Bluetooth half.
     */
    fun nearbyPermissions(): List<String> =
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) blePermissions() + Manifest.permission.NEARBY_WIFI_DEVICES else blePermissions()

    fun hasBle(ctx: Context): Boolean =
        ctx.packageManager.hasSystemFeature(PackageManager.FEATURE_BLUETOOTH_LE) &&
            ctx.getSystemService(BluetoothManager::class.java)?.adapter != null

    fun blePermitted(ctx: Context): Boolean =
        blePermissions().all { ContextCompat.checkSelfPermission(ctx, it) == PackageManager.PERMISSION_GRANTED }

    @SuppressLint("MissingPermission") // isEnabled needs no runtime permission; scanning is checked in bt()
    fun bt(ctx: Context): Bt {
        if (!hasBle(ctx)) return Bt.NO_HARDWARE
        if (!blePermitted(ctx)) return Bt.NEED_PERMISSION
        val adapter = ctx.getSystemService(BluetoothManager::class.java)?.adapter ?: return Bt.NO_HARDWARE
        if (!adapter.isEnabled) return Bt.OFF
        // Android 11 and older only return scan results with location switched on.
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) {
            val lm = ctx.getSystemService(LocationManager::class.java)
            if (lm != null && !LocationManagerCompat.isLocationEnabled(lm)) return Bt.LOCATION_OFF
        }
        return Bt.READY
    }

    /** Asks for what scanning needs, one prompt after the other. */
    fun askBle(a: MainActivity, done: (Boolean) -> Unit) {
        val required = blePermissions()
        // An optional one Android won't prompt for again is skipped, not sent to the settings page.
        val left = nearbyPermissions().filter {
            ContextCompat.checkSelfPermission(a, it) != PackageManager.PERMISSION_GRANTED && (it in required || Permissions.canPrompt(a, it))
        }
        fun next(i: Int) {
            if (i >= left.size) { done(blePermitted(a)); return }
            // A "no" to an optional one (nearby Wi-Fi) still lets the scan go ahead.
            Permissions.ask(a, left[i]) { ok -> if (ok || left[i] !in required) next(i + 1) else done(false) }
        }
        next(0)
    }

    /** NoCap servers advertising over Bluetooth nearby (empty unless [bt] is READY). */
    @SuppressLint("MissingPermission") // bt() checked the permissions just before
    fun bluetooth(ctx: Context, scanMs: Long = 6000): List<BleHit> {
        if (bt(ctx) != Bt.READY) return emptyList()
        val scanner = ctx.getSystemService(BluetoothManager::class.java)?.adapter?.bluetoothLeScanner ?: return emptyList()
        val hits = LinkedHashMap<String, BleHit>()
        fun add(r: ScanResult) {
            val rec = r.scanRecord ?: return
            val setup = bleSetup(rec.serviceUuids?.map { it.uuid.toString() }.orEmpty()) ?: return
            val name = rec.deviceName.orEmpty().filter { it.code in 0x20..0x7e }.take(16).ifBlank { "NoCap" }
            synchronized(hits) { hits[r.device.address] = BleHit(name, setup) }
        }
        val cb = object : ScanCallback() {
            override fun onScanResult(callbackType: Int, result: ScanResult) = add(result)
            override fun onBatchScanResults(results: MutableList<ScanResult>) = results.forEach(::add)
        }
        val filter = ScanFilter.Builder().setServiceUuid(ParcelUuid(SERVICE), ParcelUuid(MASK)).build()
        val settings = ScanSettings.Builder().setScanMode(ScanSettings.SCAN_MODE_LOW_LATENCY).build()
        try {
            scanner.startScan(listOf(filter), settings, cb)
        } catch (_: Exception) {
            return emptyList()
        }
        try {
            Thread.sleep(scanMs)
        } catch (_: InterruptedException) {
            Thread.currentThread().interrupt()
        } finally {
            runCatching { scanner.stopScan(cb) }
        }
        return synchronized(hits) { hits.values.distinct() }
    }
}
