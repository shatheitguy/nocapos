package com.nocap.link.shield

import android.content.Context
import android.content.Intent
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import com.nocap.link.data.t
import com.nocap.link.ui.CardBlock
import com.nocap.link.ui.NavRow
import com.nocap.link.ui.NoCap
import com.nocap.link.ui.SwitchRow

/** Settings → Shield: the switch, today's count, and the way into the full page. */
@Composable
fun ShieldSettingsCard(ctx: Context, tick: Int) {
    remember(ctx) { ShieldStore.init(ctx); 0 }
    val on = ShieldStore.enabled
    val blocked = remember(tick, ShieldStore.logTick, on) { ShieldStore.blockedToday() }
    fun open(action: String? = null) {
        runCatching { ctx.startActivity(Intent(ctx, ShieldActivity::class.java).setAction(action).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) }
    }
    SwitchRow(
        "Shield",
        if (on) t("On · $blocked blocked today", "On · $blocked blocked today") else t("Off", "Off"),
        on,
        icon = "shield", iconColor = NoCap.Red,
    ) { v ->
        if (!v) Shield.stop(ctx) else if (!Shield.start(ctx)) open(ShieldActivity.ACTION_TURN_ON)
    }
    NavRow(t("DNS filter, firewall, activity", "DNS filter, firewall and activity"), sub = t("Block lists, trusted Wi-Fi, per-app rules", "Lists, trusted Wi-Fi networks and per-app rules")) { open() }
    NavRow(t("Always-on VPN", "Always-on VPN"), sub = t("Keeps Shield on, even after a reboot", "Keeps Shield running, including after a restart")) { Shield.vpnSettings(ctx) }
    CardBlock {
        Text(
            t(
                "Uses Android's VPN slot (key icon up top), so no other VPN at the same time. Only DNS lookups go to your own NoCap server.",
                "Uses Android's VPN, shown by a key icon, so it can't run alongside another VPN app. Only DNS lookups are sent, to your own NoCap server.",
            ),
            style = MaterialTheme.typography.bodySmall, color = NoCap.Muted,
        )
    }
}
