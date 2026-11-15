package com.nocap.link.shield

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.net.NetworkCapabilities
import android.net.VpnService
import android.net.Uri
import android.net.wifi.WifiInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.core.content.ContextCompat
import com.nocap.link.data.Prefs
import com.nocap.link.data.t
import com.nocap.link.ui.NoCapTheme

/**
 * The Shield page (from Settings, the quick-settings tile, the widget and
 * Shield's notification). Its own screen, so the tile and widget can open
 * it straight over whatever is showing.
 */
class ShieldActivity : ComponentActivity() {
    /** Shows the "here's what Android will ask" explainer before its VPN prompt. */
    var explain by mutableStateOf(false)
    /** Explains why location is needed before asking (trusted Wi-Fi). */
    var askLocation by mutableStateOf(false)
    var resumeTick by mutableStateOf(0)

    private val consent = registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { r ->
        if (r.resultCode == RESULT_OK && Shield.start(this)) {
            toast(t("Shield's up.", "Shield is on."))
        } else {
            toast(t("Android didn't get the OK, so Shield stays off.", "VPN permission wasn't granted, so Shield is off."))
        }
    }

    private val location = registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { res ->
        if (res.values.any { it }) trustCurrentWifi()
        else toast(t("No location access, so NoCap can't see the Wi-Fi name.", "Without location access, Android hides the Wi-Fi network name."))
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        Prefs.init(this)
        ShieldStore.init(this)
        if (intent?.action == ACTION_TURN_ON && savedInstanceState == null) turnOn()
        setContent { NoCapTheme { ShieldScreen(this) } }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        if (intent.action == ACTION_TURN_ON) turnOn()
    }

    override fun onResume() {
        super.onResume()
        resumeTick++
    }

    /** On: straight away when Android already said yes, else the explainer, then Android's prompt. */
    fun turnOn() {
        if (Shield.start(this)) {
            toast(t("Shield's up.", "Shield is on."))
            return
        }
        explain = true
    }

    /** After the explainer: Android's own VPN prompt. */
    fun askAndroid() {
        explain = false
        val i = VpnService.prepare(this)
        if (i == null) {
            if (Shield.start(this)) toast(t("Shield's up.", "Shield is on."))
            return
        }
        runCatching { consent.launch(i) }.onFailure { toast(t("Couldn't open Android's VPN prompt.", "Couldn't open the VPN permission prompt.")) }
    }

    fun turnOff() {
        Shield.stop(this)
    }

    // ---------- trusted Wi-Fi ----------

    fun hasLocation(): Boolean =
        ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED

    fun hasBackgroundLocation(): Boolean =
        Build.VERSION.SDK_INT < 29 || ContextCompat.checkSelfPermission(this, Manifest.permission.ACCESS_BACKGROUND_LOCATION) == PackageManager.PERMISSION_GRANTED

    /** The Wi-Fi this phone is on now (null: not on Wi-Fi, or Android hides the name). */
    fun currentSsid(): String? {
        val cm = getSystemService(ConnectivityManager::class.java)
        val caps = runCatching { cm.getNetworkCapabilities(cm.activeNetwork) }.getOrNull()
        val wifi = caps?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true ||
            ShieldStore.net.kind == NetKind.WIFI
        if (!wifi) return null
        if (Build.VERSION.SDK_INT >= 29) {
            (caps?.transportInfo as? WifiInfo)?.ssid?.let { s -> ShieldRules.cleanSsid(s)?.let { return it } }
        }
        ShieldStore.net.ssid?.let { return it }
        @Suppress("DEPRECATION")
        return runCatching { ShieldRules.cleanSsid(applicationContext.getSystemService(WifiManager::class.java)?.connectionInfo?.ssid) }.getOrNull()
    }

    fun addCurrentWifi() {
        if (!hasLocation()) {
            askLocation = true
            return
        }
        trustCurrentWifi()
    }

    fun requestLocation() {
        askLocation = false
        location.launch(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION))
    }

    private fun trustCurrentWifi() {
        val ssid = currentSsid()
        if (ssid == null) {
            toast(t("Not on Wi-Fi rn, or Android hides its name (is Location on?).", "Not connected to Wi-Fi, or Android hides its name (check that Location is on)."))
            return
        }
        ShieldStore.trust(ssid, true)
        Shield.reload()
        toast(t("Shield chills on $ssid now.", "Shield is paused on $ssid."))
    }

    /** App info → Permissions, for "Allow all the time". */
    fun openAppSettings() {
        runCatching { startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:$packageName"))) }
    }

    fun toast(msg: String) = Toast.makeText(applicationContext, msg, Toast.LENGTH_SHORT).show()

    companion object {
        const val ACTION_TURN_ON = "com.nocap.link.shield.TURN_ON"
    }
}
