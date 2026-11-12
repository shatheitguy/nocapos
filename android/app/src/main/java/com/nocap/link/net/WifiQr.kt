package com.nocap.link.net

/**
 * The standard "join this Wi-Fi" QR payload phones understand:
 * `WIFI:T:WPA;S:<ssid>;P:<password>;H:<true|false>;;`, with backslash,
 * semicolon, comma, colon and double quote escaped by a backslash.
 * Same as the web OS (frontend/src/lib/wifiQr.ts).
 */
object WifiQr {
    fun escape(v: String): String = buildString {
        for (c in v) {
            if (c == '\\' || c == ';' || c == ',' || c == ':' || c == '"') append('\\')
            append(c)
        }
    }

    /** [type] is "WPA" (WPA/WPA2/WPA3 personal) or "nopass" (open network). */
    fun payload(ssid: String, password: String, type: String, hidden: Boolean): String {
        val pw = if (type == "nopass") "" else "P:${escape(password)};"
        return "WIFI:T:$type;S:${escape(ssid)};${pw}H:$hidden;;"
    }
}
