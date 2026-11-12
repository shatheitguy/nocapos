package com.nocap.link.update

import org.json.JSONObject

/** One file attached to a release. */
data class Asset(val name: String, val url: String, val size: Long)

/** The parts of a GitHub release the updater needs. */
data class Release(val tag: String, val name: String, val body: String, val page: String, val assets: List<Asset>)

/** An app version as far as it is known: either part may be missing. */
data class AppVersion(val code: Long?, val name: String?) {
    val known get() = code != null || !name.isNullOrBlank()
}

/**
 * Pure release logic (no Android): reading the release, picking the APK,
 * working out its version and comparing it with the installed one.
 */
object Releases {
    const val REPO = "shatheitguy/nocapos"
    const val LATEST = "https://api.github.com/repos/$REPO/releases/latest"
    const val APK = "nocap-os.apk"

    /** Small JSON published next to the APK: {"versionCode":5,"versionName":"3.2.0","sha256":"..."}. */
    const val META = "nocap-os.json"

    fun parse(json: String): Release {
        val o = JSONObject(json)
        val list = o.optJSONArray("assets")
        val assets = (0 until (list?.length() ?: 0)).mapNotNull { i ->
            val a = list?.optJSONObject(i) ?: return@mapNotNull null
            val name = a.optString("name")
            val url = a.optString("browser_download_url")
            if (name.isBlank() || !url.startsWith("https://")) null else Asset(name, url, a.optLong("size", -1))
        }
        return Release(o.optString("tag_name"), o.optString("name"), o.optString("body"), o.optString("html_url"), assets)
    }

    /**
     * The app's APK: exactly nocap-os.apk when it's there, otherwise a
     * versioned nocap-os-*.apk. Debug builds are never picked.
     */
    fun pickApk(assets: List<Asset>): Asset? =
        assets.firstOrNull { it.name.equals(APK, ignoreCase = true) }
            ?: assets.filter {
                val n = it.name.lowercase()
                n.startsWith("nocap-os") && n.endsWith(".apk") && "debug" !in n && "unsigned" !in n
            }.maxWithOrNull { x, y -> compareNames(versionFromAssetName(x.name)?.name ?: "", versionFromAssetName(y.name)?.name ?: "") }

    fun pickMeta(assets: List<Asset>): Asset? = assets.firstOrNull { it.name.equals(META, ignoreCase = true) }

    data class Meta(val version: AppVersion, val sha256: String?)

    fun parseMeta(json: String): Meta? = runCatching {
        val o = JSONObject(json)
        val code = if (o.has("versionCode")) o.optLong("versionCode", -1).takeIf { it > 0 } else null
        val name = o.optString("versionName").takeIf { it.isNotBlank() }
        val sha = o.optString("sha256").lowercase().takeIf { it.matches(Regex("[0-9a-f]{64}")) }
        Meta(AppVersion(code, name), sha)
    }.getOrNull()

    private val bodyCode = Regex("""versionCode\W{0,3}(\d{1,9})""", RegexOption.IGNORE_CASE)
    private val bodyName = Regex("""versionName\W{0,3}v?(\d+(?:\.\d+)*(?:-[0-9A-Za-z.]+)?)""", RegexOption.IGNORE_CASE)

    /** "versionCode: 5" / "versionName: 3.2.0" anywhere in the release notes. */
    fun versionFromBody(body: String): AppVersion? {
        val code = bodyCode.find(body)?.groupValues?.get(1)?.toLongOrNull()
        val name = bodyName.find(body)?.groupValues?.get(1)
        return AppVersion(code, name).takeIf { it.known }
    }

    private val assetFull = Regex("""^nocap-os-v?(\d+(?:\.\d+)*)-(\d+)\.apk$""", RegexOption.IGNORE_CASE)
    private val assetName = Regex("""^nocap-os-v?(\d+(?:\.\d+)+)\.apk$""", RegexOption.IGNORE_CASE)
    private val assetCode = Regex("""^nocap-os-(\d+)\.apk$""", RegexOption.IGNORE_CASE)

    /** nocap-os-3.2.0-5.apk, nocap-os-v3.2.0.apk or nocap-os-5.apk. */
    fun versionFromAssetName(file: String): AppVersion? {
        assetFull.find(file)?.let { return AppVersion(it.groupValues[2].toLongOrNull(), it.groupValues[1]) }
        assetName.find(file)?.let { return AppVersion(null, it.groupValues[1]) }
        assetCode.find(file)?.let { return AppVersion(it.groupValues[1].toLongOrNull(), null) }
        return null
    }

    /**
     * Compares version names like 3.10.0 and 3.9.2 by their numbers. A
     * pre-release (3.2.0-beta) sorts before the release with the same numbers.
     */
    fun compareNames(a: String, b: String): Int {
        fun split(v: String): Pair<List<Long>, String> {
            val s = v.trim().removePrefix("v").removePrefix("V")
            val main = s.substringBefore('-').substringBefore('+')
            val pre = if ('-' in s) s.substringAfter('-').substringBefore('+') else ""
            return main.split('.').map { part -> part.takeWhile { it.isDigit() }.toLongOrNull() ?: 0L } to pre
        }
        val (x, xp) = split(a)
        val (y, yp) = split(b)
        for (i in 0 until maxOf(x.size, y.size)) {
            val c = x.getOrElse(i) { 0L }.compareTo(y.getOrElse(i) { 0L })
            if (c != 0) return c
        }
        return when {
            xp == yp -> 0
            xp.isEmpty() -> 1
            yp.isEmpty() -> -1
            else -> xp.compareTo(yp)
        }
    }

    /**
     * Is the release newer than what's installed? The version code decides
     * when it's known (that's what Android compares); otherwise the version
     * name. null = can't tell without downloading the APK.
     */
    fun isNewer(remote: AppVersion?, localCode: Long, localName: String): Boolean? = when {
        remote == null -> null
        remote.code != null -> remote.code > localCode
        !remote.name.isNullOrBlank() -> compareNames(remote.name, localName) > 0
        else -> null
    }

    private val mdLink = Regex("""\[([^\]]*)]\([^)]*\)""")

    /**
     * A short "what's new" from the release notes: the first few bullet or
     * text lines, without headings or Markdown.
     */
    fun whatsNew(body: String, maxLines: Int = 5, maxChars: Int = 360): String {
        val lines = body.lineSequence().map { it.trim() }
            .filter { it.isNotEmpty() && !it.startsWith("#") && !it.startsWith("<!--") && !it.startsWith("---") && !it.startsWith("```") }
            .filter { !it.contains("versionCode", ignoreCase = true) && !it.startsWith("|") }
            .map { l ->
                val bullet = l.startsWith("- ") || l.startsWith("* ") || l.startsWith("+ ")
                val text = mdLink.replace(if (bullet) l.substring(2) else l) { it.groupValues[1] }
                    .replace("**", "").replace("__", "").replace("`", "").trim()
                if (bullet) "• $text" else text
            }
            .filter { it.isNotBlank() && it != "•" }
            .take(maxLines)
            .toList()
        val out = lines.joinToString("\n")
        return if (out.length > maxChars) out.take(maxChars - 1).trimEnd() + "…" else out
    }

    /** Best version info for a release: the meta file first, then the notes, then the file name. */
    fun versionOf(release: Release, apk: Asset, meta: Meta?): AppVersion? =
        meta?.version?.takeIf { it.known } ?: versionFromBody(release.body) ?: versionFromAssetName(apk.name)

    /**
     * Would Android accept this APK as an update? Its signer must be the
     * installed one, or (key rotation) the installed signer must be in its
     * signing history. Certificates are compared as SHA-256 hex strings.
     */
    fun signersMatch(installed: Set<String>, apkSigners: Set<String>, apkHistory: Set<String> = emptySet()): Boolean {
        if (installed.isEmpty() || apkSigners.isEmpty()) return false
        if (apkSigners == installed) return true
        return installed.size == 1 && apkSigners.size == 1 && installed.first() in apkHistory
    }
}
