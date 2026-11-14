package com.nocap.link.sync

/**
 * The seamless clipboard's plain rules (no Android in here, so they're
 * unit-tested): what counts as a "Copy" tap, size limits, picture types and
 * how often the phone may read its clipboard in the background.
 */
object ClipRules {
    /** Text up to 1 MB and pictures up to 10 MB, as the server takes. */
    const val MAX_TEXT = 1 shl 20
    const val MAX_IMAGE = 10 shl 20
    /** At most one background read this often. */
    const val GAP_MS = 1_500L
    /** A copy waiting for the keyboard to close is dropped after this. */
    const val WAIT_MS = 60_000L

    private val copyLabels = setOf(
        "copy", "copy text", "copy link", "copy link address", "copy url", "copy address",
        "copy to clipboard", "copy message", "copy code", "copy number", "copy email",
    )

    /**
     * Whether a tapped control is a "Copy" button. [own] is this phone's own
     * word for it (Android's "Copy" in the phone's language).
     */
    fun isCopyLabel(label: CharSequence?, own: String? = null): Boolean {
        val l = label?.toString()?.trim()?.trimEnd('.', '…')?.lowercase() ?: return false
        if (l.isEmpty() || l.length > 40) return false
        if (!own.isNullOrBlank() && l == own.trim().lowercase()) return true
        return l in copyLabels
    }

    /** Whether a toast says something was copied ("Copied to clipboard", "Link copied"). */
    fun isCopiedToast(text: CharSequence?): Boolean {
        val l = text?.toString()?.lowercase() ?: return false
        return l.length <= 80 && "copied" in l
    }

    /** "image/png" or "image/jpeg" from the picture's first bytes, else null. */
    fun imageMime(b: ByteArray): String? = when {
        b.size >= 8 && b[0] == 0x89.toByte() && b[1] == 'P'.code.toByte() && b[2] == 'N'.code.toByte() && b[3] == 'G'.code.toByte() -> "image/png"
        b.size >= 3 && b[0] == 0xFF.toByte() && b[1] == 0xD8.toByte() && b[2] == 0xFF.toByte() -> "image/jpeg"
        else -> null
    }

    /** Text the server takes: some, and at most 1 MB. */
    fun textFits(text: String): Boolean = text.isNotEmpty() && text.toByteArray(Charsets.UTF_8).size <= MAX_TEXT

    /** Whether a background read may start now (the last one was at [last]). */
    fun due(now: Long, last: Long): Boolean = last == 0L || now - last >= GAP_MS
}
